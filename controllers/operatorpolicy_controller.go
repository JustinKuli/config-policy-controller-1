// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	operatorv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	depclient "github.com/stolostron/kubernetes-dependency-watches/client"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	policyv1beta1 "open-cluster-management.io/config-policy-controller/api/v1beta1"
)

const (
	OperatorControllerName string = "operator-policy-controller"
)

var (
	subscriptionGVK  = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "Subscription"}
	operatorGroupGVK = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1", Kind: "OperatorGroup"}
)

// OperatorPolicyReconciler reconciles a OperatorPolicy object
type OperatorPolicyReconciler struct {
	client.Client
	DynamicWatcher depclient.DynamicWatcher
	InstanceName   string
}

// SetupWithManager sets up the controller with the Manager and will reconcile when the dynamic watcher
// sees that an object is updated
func (r *OperatorPolicyReconciler) SetupWithManager(mgr ctrl.Manager, depEvents *source.Channel) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(OperatorControllerName).
		For(
			&policyv1beta1.OperatorPolicy{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			depEvents,
			&handler.EnqueueRequestForObject{}).
		Complete(r)
}

// blank assignment to verify that OperatorPolicyReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &OperatorPolicyReconciler{}

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=operatorpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=operatorpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=operatorpolicies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// (user): Modify the Reconcile function to compare the state specified by
// the OperatorPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile
func (r *OperatorPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	OpLog := ctrl.LoggerFrom(ctx)
	policy := &policyv1beta1.OperatorPolicy{}
	watcher := opPolIdentifier(req.Namespace, req.Name)

	// Get the applied OperatorPolicy
	err := r.Get(ctx, req.NamespacedName, policy)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			OpLog.Info("Operator policy could not be found")

			err = r.DynamicWatcher.RemoveWatcher(watcher)
			if err != nil {
				OpLog.Error(err, "Error updating dependency watcher. Ignoring the failure.")
			}

			return reconcile.Result{}, nil
		}

		OpLog.Error(err, "Failed to get operator policy")

		return reconcile.Result{}, err
	}

	// Start query batch for caching and watching related objects
	err = r.DynamicWatcher.StartQueryBatch(watcher)
	if err != nil {
		OpLog.Error(err, "Could not start query batch for the watcher")

		return reconcile.Result{}, err
	}

	defer func() {
		err := r.DynamicWatcher.EndQueryBatch(watcher)
		if err != nil {
			OpLog.Error(err, "Could not end query batch for the watcher")
		}
	}()

	// handle the policy
	OpLog.Info("Reconciling OperatorPolicy")

	if err := r.handleOpGroup(ctx, policy); err != nil {
		OpLog.Error(err, "Error handling OperatorGroup")

		return reconcile.Result{}, err
	}

	if err := r.handleSubscription(ctx, policy); err != nil {
		OpLog.Error(err, "Error handling Subscription")

		return reconcile.Result{}, err
	}

	// FUTURE: more resource checks

	return reconcile.Result{}, nil
}

func (r *OperatorPolicyReconciler) handleOpGroup(ctx context.Context, policy *policyv1beta1.OperatorPolicy) error {
	watcher := opPolIdentifier(policy.Namespace, policy.Name)

	desiredOpGroup, err := buildOperatorGroup(policy)
	if err != nil {
		return fmt.Errorf("error building operator group: %w", err)
	}

	foundOpGroups, err := r.DynamicWatcher.List(
		watcher, operatorGroupGVK, desiredOpGroup.Namespace, labels.Everything())
	if err != nil {
		return fmt.Errorf("error listing OperatorGroups: %w", err)
	}

	switch len(foundOpGroups) {
	case 0:
		// Missing OperatorGroup: report NonCompliance
		err := r.updateStatus(ctx, policy, missingWantedCond("OperatorGroup"), missingWantedObj(desiredOpGroup))
		if err != nil {
			return fmt.Errorf("error updating the status for a missing OperatorGroup: %w", err)
		}

		if policy.Spec.RemediationAction.IsEnforce() {
			err = r.Create(ctx, desiredOpGroup)
			if err != nil {
				return fmt.Errorf("error creating the OperatorGroup: %w", err)
			}

			desiredOpGroup.SetGroupVersionKind(operatorGroupGVK) // Create stripped this information

			// Now the OperatorGroup should match, so report Compliance
			err = r.updateStatus(ctx, policy, createdCond("OperatorGroup"), createdObj(desiredOpGroup))
			if err != nil {
				return fmt.Errorf("error updating the status for a created OperatorGroup: %w", err)
			}
		}
	case 1:
		opGroup := foundOpGroups[0]

		if policy.Spec.OperatorGroup != nil {
			emptyNameMatch := desiredOpGroup.Name == "" && opGroup.GetGenerateName() == desiredOpGroup.GenerateName

			if opGroup.GetName() == desiredOpGroup.Name || emptyNameMatch {
				// check whether the specs match
				desiredUnstruct, err := runtime.DefaultUnstructuredConverter.ToUnstructured(desiredOpGroup)
				if err != nil {
					return fmt.Errorf("error converting desired OperatorGroup to an Unstructured: %w", err)
				}

				if reflect.DeepEqual(desiredUnstruct["spec"], opGroup.Object["spec"]) {
					// Everything relevant matches! This is a "happy path".
					// Only update the condition if it reflects a NonCompliant state because emitting a
					// Compliant -> Compliant 'change' is noisy and would hide *why* this part of the
					// policy is currently compliant (eg that the policy previously updated the object).
					idx, existingCond := policy.Status.GetCondition(opGroupConditionType)

					if idx == -1 || existingCond.Status == metav1.ConditionFalse {
						err := r.updateStatus(ctx, policy, matchesCond("OperatorGroup"), matchedObj(&opGroup))
						if err != nil {
							return fmt.Errorf("error updating the status for an OperatorGroup that matches: %w", err)
						}
					}
				} else {
					// The names match, but the specs don't: report NonCompliance
					err := r.updateStatus(ctx, policy, mismatchCond("OperatorGroup"), mismatchedObj(&opGroup))
					if err != nil {
						return fmt.Errorf("error updating the status for an OperatorGroup that does not match: %w", err)
					}

					if policy.Spec.RemediationAction.IsEnforce() {
						desiredOpGroup.ResourceVersion = opGroup.GetResourceVersion()

						err := r.Update(ctx, desiredOpGroup)
						if err != nil {
							return fmt.Errorf("error updating the OperatorGroup: %w", err)
						}
						desiredOpGroup.SetGroupVersionKind(operatorGroupGVK) // Update stripped this information

						// It was updated and should match now, so report Compliance
						err = r.updateStatus(ctx, policy, updatedCond("OperatorGroup"), updatedObj(desiredOpGroup))
						if err != nil {
							return fmt.Errorf("error updating the status after updating the OperatorGroup: %w", err)
						}
					}
				}
			} else {
				// There is an OperatorGroup in the namespace that does not match the name of what is in the policy.
				// Just creating a new one would cause the "TooManyOperatorGroups" failure.
				// So, just report a NonCompliant status.
				missing := missingWantedObj(desiredOpGroup)
				badExisting := mismatchedObj(&opGroup)

				err := r.updateStatus(ctx, policy, mismatchCond("OperatorGroup"), missing, badExisting)
				if err != nil {
					return fmt.Errorf("error updating the status for an OperatorGroup with the wrong name: %w", err)
				}
			}
		} else {
			// There is one operator group in the namespace, but the policy doesn't specify what it should look like.

			// FUTURE: check if the one operator group is compatible with the desired subscription.

			// For an initial implementation, assume if an OperatorGroup already exists, then it's a good one.
			// Only update the condition if it reflects a NonCompliant state, to prevent repeats.
			idx, existingCond := policy.Status.GetCondition(opGroupConditionType)

			if idx == -1 || existingCond.Status == metav1.ConditionFalse {
				err := r.updateStatus(ctx, policy, matchesCond("OperatorGroup"), matchedObj(&opGroup))
				if err != nil {
					return fmt.Errorf("error updating the status for an OperatorGroup that matches: %w", err)
				}
			}
		}
	default:
		// This situation will always lead to a "TooManyOperatorGroups" failure on the CSV.
		// Consider improving this in the future: perhaps this could suggest one of the OperatorGroups to keep.
		err := r.updateStatus(ctx, policy, opGroupTooManyCond, opGroupTooManyObjs(foundOpGroups)...)
		if err != nil {
			return fmt.Errorf("error updating the status when there are multiple OperatorGroups: %w", err)
		}
	}

	return nil
}

// buildOperatorGroup bootstraps the OperatorGroup spec defined in the operator policy
// with the apiversion and kind in preparation for resource creation
func buildOperatorGroup(
	policy *policyv1beta1.OperatorPolicy,
) (*operatorv1.OperatorGroup, error) {
	operatorGroup := new(operatorv1.OperatorGroup)

	operatorGroup.Status.LastUpdated = &metav1.Time{} // without this, some conversions can panic
	operatorGroup.SetGroupVersionKind(operatorGroupGVK)

	sub := make(map[string]interface{})

	err := json.Unmarshal(policy.Spec.Subscription.Raw, &sub)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling subscription: %w", err)
	}

	subNamespace, ok := sub["namespace"].(string)
	if !ok {
		return nil, fmt.Errorf("namespace is required in spec.subscription")
	}

	// Create a default OperatorGroup if one wasn't specified in the policy
	if policy.Spec.OperatorGroup == nil {
		operatorGroup.ObjectMeta.SetNamespace(subNamespace)
		operatorGroup.ObjectMeta.SetGenerateName(subNamespace + "-") // This matches what the console creates
		operatorGroup.Spec.TargetNamespaces = []string{}

		return operatorGroup, nil
	}

	opGroup := make(map[string]interface{})

	err = json.Unmarshal(policy.Spec.OperatorGroup.Raw, &opGroup)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling operatorGroup: %w", err)
	}

	// Fallback to the Subscription namespace if the OperatorGroup namespace is not specified in the policy.
	ogNamespace := subNamespace

	if specifiedNS, ok := opGroup["namespace"].(string); ok {
		ogNamespace = specifiedNS
	}

	name, ok := opGroup["name"].(string)
	if !ok {
		return nil, fmt.Errorf("name is required in operatorGroup.spec")
	}

	spec := new(operatorv1.OperatorGroupSpec)

	err = json.Unmarshal(policy.Spec.OperatorGroup.Raw, spec)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling subscription: %w", err)
	}

	operatorGroup.ObjectMeta.SetName(name)
	operatorGroup.ObjectMeta.SetNamespace(ogNamespace)
	operatorGroup.Spec = *spec

	return operatorGroup, nil
}

func (r *OperatorPolicyReconciler) handleSubscription(ctx context.Context, policy *policyv1beta1.OperatorPolicy) error {
	watcher := opPolIdentifier(policy.Namespace, policy.Name)

	desiredSub, err := buildSubscription(policy)
	if err != nil {
		return fmt.Errorf("error building subscription: %w", err)
	}

	foundSub, err := r.DynamicWatcher.Get(watcher, subscriptionGVK, desiredSub.Namespace, desiredSub.Name)
	if err != nil {
		return fmt.Errorf("error listing OperatorGroups: %w", err)
	}

	if foundSub == nil {
		// Missing Subscription: report NonCompliance
		err := r.updateStatus(ctx, policy, missingWantedCond("Subscription"), missingWantedObj(desiredSub))
		if err != nil {
			return fmt.Errorf("error updating status for a missing Subscription: %w", err)
		}

		if policy.Spec.RemediationAction.IsEnforce() {
			err := r.Create(ctx, desiredSub)
			if err != nil {
				return fmt.Errorf("error creating the Subscription: %w", err)
			}

			desiredSub.SetGroupVersionKind(subscriptionGVK) // Create stripped this information

			// Now it should match, so report Compliance
			err = r.updateStatus(ctx, policy, createdCond("Subscription"), createdObj(desiredSub))
			if err != nil {
				return fmt.Errorf("error updating the status for a created Subscription: %w", err)
			}
		}
	} else {
		// Subscription found; check if specs match
		desiredUnstruct, err := runtime.DefaultUnstructuredConverter.ToUnstructured(desiredSub)
		if err != nil {
			return fmt.Errorf("error converting desired Subscription to an Unstructured: %w", err)
		}

		if reflect.DeepEqual(desiredUnstruct["spec"], foundSub.Object["spec"]) {
			// FUTURE: Check more details about the *status* of the Subscription
			// For now, (conditionally) mark it as compliant
			idx, existingCond := policy.Status.GetCondition(subConditionType)

			if idx == -1 || existingCond.Status == metav1.ConditionFalse {
				err := r.updateStatus(ctx, policy, matchesCond("Subscription"), matchedObj(foundSub))
				if err != nil {
					return fmt.Errorf("error updating the status for an OperatorGroup that matches: %w", err)
				}
			}
		} else {
			err := r.updateStatus(ctx, policy, mismatchCond("Subscription"), mismatchedObj(foundSub))
			if err != nil {
				return fmt.Errorf("error updating status for a mismatched Subscription: %w", err)
			}

			if policy.Spec.RemediationAction.IsEnforce() {
				desiredSub.ResourceVersion = foundSub.GetResourceVersion()

				err := r.Update(ctx, desiredSub)
				if err != nil {
					return fmt.Errorf("error updating the Subscription: %w", err)
				}
				desiredSub.SetGroupVersionKind(subscriptionGVK) // Update stripped this information

				err = r.updateStatus(ctx, policy, updatedCond("Subscription"), updatedObj(desiredSub))
				if err != nil {
					return fmt.Errorf("error updating status after updating the Subscription: %w", err)
				}
			}
		}
	}

	return nil
}

// buildSubscription bootstraps the subscription spec defined in the operator policy
// with the apiversion and kind in preparation for resource creation
func buildSubscription(
	policy *policyv1beta1.OperatorPolicy,
) (*operatorv1alpha1.Subscription, error) {
	subscription := new(operatorv1alpha1.Subscription)

	sub := make(map[string]interface{})

	err := json.Unmarshal(policy.Spec.Subscription.Raw, &sub)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling subscription: %w", err)
	}

	ns, ok := sub["namespace"].(string)
	if !ok {
		return nil, fmt.Errorf("namespace is required in spec.subscription")
	}

	spec := new(operatorv1alpha1.SubscriptionSpec)

	err = json.Unmarshal(policy.Spec.Subscription.Raw, spec)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling subscription: %w", err)
	}

	subscription.SetGroupVersionKind(subscriptionGVK)
	subscription.ObjectMeta.Name = spec.Package
	subscription.ObjectMeta.Namespace = ns
	subscription.Spec = spec

	return subscription, nil
}

func opPolIdentifier(namespace, name string) depclient.ObjectIdentifier {
	return depclient.ObjectIdentifier{
		Group:     policyv1beta1.GroupVersion.Group,
		Version:   policyv1beta1.GroupVersion.Version,
		Kind:      "OperatorPolicy",
		Namespace: namespace,
		Name:      name,
	}
}
