// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	operatorv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	depclient "github.com/stolostron/kubernetes-dependency-watches/client"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	policyv1 "open-cluster-management.io/config-policy-controller/api/v1"
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

	// Check if specified Subscription exists
	cachedSubscription, err := r.DynamicWatcher.Get(watcher, subscriptionGVK, policy.Spec.Subscription.Namespace,
		policy.Spec.Subscription.SubscriptionSpec.Package)
	if err != nil {
		OpLog.Error(err, "Could not get subscription",
			"SubscriptionName", policy.Spec.Subscription.SubscriptionSpec.Package,
			"SubscriptionNamespace", policy.Spec.Subscription.Namespace)

		return reconcile.Result{}, err
	}

	subExists := cachedSubscription != nil
	shouldExist := policy.Spec.ComplianceType.IsMustHave()

	// Case 1: policy has just been applied and related resources have yet to be created
	if !subExists && shouldExist {
		OpLog.Info("The object does not exist but should exist")

		return r.createPolicyResources(ctx, policy, cachedSubscription)
	}

	// Case 2: Resources exist, but should not exist (i.e. mustnothave or deletion)
	if subExists && !shouldExist {
		// Future implementation: clean up resources and delete watches if mustnothave, otherwise inform
		OpLog.Info("The object exists but should not exist")
	}

	// Case 3: Resources do not exist, and should not exist
	if !subExists && !shouldExist {
		// Future implementation: Possibly emit a success event
		OpLog.Info("The object does not exist and is compliant with the mustnothave compliance type")
	}

	// Case 4: Resources exist, and should exist (i.e. update)
	if subExists && shouldExist {
		// Future implementation: Verify the specs of the object matches the one on the cluster
		OpLog.Info("The object already exists. Checking fields to verify matching specs")
	}

	return reconcile.Result{}, nil
}

func (r *OperatorPolicyReconciler) handleOpGroup(ctx context.Context, policy *policyv1beta1.OperatorPolicy) error {
	if policy.Spec.ComplianceType.IsMustNotHave() {
		// Currently, OperatorGroups are not checked in "mustnothave" mode. This is by design:
		// https://github.com/open-cluster-management-io/enhancements/blob/6682e82e2d7864a21e53b242e5d061b74791adbc/enhancements/sig-policy/89-operator-policy-kind/README.md?plain=1#L132
		return nil
	}

	watcher := opPolIdentifier(policy.Namespace, policy.Name)

	desiredOpGroup := buildOperatorGroup(policy)

	foundOpGroups, err := r.DynamicWatcher.List(
		watcher, operatorGroupGVK, desiredOpGroup.Namespace, labels.Everything())
	if err != nil {
		return fmt.Errorf("error listing OperatorGroups: %w", err)
	}

	switch len(foundOpGroups) {
	case 0:
		// Missing OperatorGroup: report NonCompliance
		err := r.updateStatus(ctx, policy, opGroupMissingCondition, opGroupMissingObj(desiredOpGroup))
		if err != nil {
			return fmt.Errorf("error updating the status for a missing OperatorGroup: %w", err)
		}

		if policy.Spec.RemediationAction.IsEnforce() {
			err := r.Create(ctx, desiredOpGroup)
			if err != nil {
				return fmt.Errorf("error creating an OperatorGroup: %w", err)
			}

			// Now the OperatorGroup should match, so report Compliance
			err = r.updateStatus(ctx, policy, opGroupCreatedCondition, opGroupCreatedObj(desiredOpGroup))
			if err != nil {
				return fmt.Errorf("error updating the status for a created OperatorGroup: %w", err)
			}
		}
	case 1:
		opGroup := foundOpGroups[0]

		if policy.Spec.OperatorGroup != nil {
			if opGroup.GetName() == desiredOpGroup.Name || opGroup.GetGenerateName() == desiredOpGroup.GenerateName {
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
						err := r.updateStatus(ctx, policy, opGroupMatchCondition, opGroupMatchObj(opGroup))
						if err != nil {
							return fmt.Errorf("error updating the status for an OperatorGroup that matches: %w", err)
						}
					}
				} else {
					// The names match, but the specs don't: report NonCompliance
					err := r.updateStatus(ctx, policy, opGroupMismatchCondition, opGroupMismatchObj(opGroup))
					if err != nil {
						return fmt.Errorf("error updating the status for an OperatorGroup that does not match: %w", err)
					}

					if policy.Spec.RemediationAction.IsEnforce() {
						err := r.Update(ctx, desiredOpGroup)
						if err != nil {
							return fmt.Errorf("error updating the OperatorGroup: %w", err)
						}

						// It was updated and should match now, so report Compliance
						err = r.updateStatus(ctx, policy, opGroupUpdatedCondition, opGroupUpdatedObj(desiredOpGroup))
						if err != nil {
							return fmt.Errorf("error updating the status after updating the OperatorGroup: %w", err)
						}
					}
				}
			} else {
				// There is an OperatorGroup in the namespace that does not match the name of what is in the policy.
				// Just creating a new one would cause the "TooManyOperatorGroups" failure.
				// So, just report a NonCompliant status.
				missing := opGroupMissingObj(desiredOpGroup)
				badExisting := opGroupMismatchObj(opGroup)

				err := r.updateStatus(ctx, policy, opGroupMismatchCondition, missing, badExisting)
				if err != nil {
					return fmt.Errorf("error updating the status fo an OperatorGroup with the wrong name: %w", err)
				}
			}
		} else {
			// There is one operator group in the namespace, but the policy doesn't specify what it should look like.

			// FUTURE: check if the one operator group is compatible with the desired subscription.

			// For an initial implementation, assume if an OperatorGroup already exists, then it's a good one.
			// Only update the condition if it reflects a NonCompliant state, to prevent repeats.
			idx, existingCond := policy.Status.GetCondition(opGroupConditionType)

			if idx == -1 || existingCond.Status == metav1.ConditionFalse {
				err := r.updateStatus(ctx, policy, opGroupMatchCondition, opGroupMatchObj(opGroup))
				if err != nil {
					return fmt.Errorf("error updating the status for an OperatorGroup that matches: %w", err)
				}
			}
		}
	default:
		// This situation will always lead to a "TooManyOperatorGroups" failure on the CSV.
		// Consider improving this in the future: perhaps this could suggest one of the OperatorGroups to keep.
		err := r.updateStatus(ctx, policy, opGroupTooManyCondition, opGroupTooManyObjs(foundOpGroups)...)
		if err != nil {
			return fmt.Errorf("error updating the status when there are multiple OperatorGroups: %w", err)
		}
	}

	return nil
}

const (
	compliantConditionType      = "Compliant"
	opGroupConditionType        = "OperatorGroupCorrect"
	reasonTooManyOperatorGroups = "There is more than one OperatorGroup in this namespace"
)

var (
	opGroupMissingCondition = metav1.Condition{
		Type:    opGroupConditionType,
		Status:  metav1.ConditionFalse,
		Reason:  "OperatorGroupMissing",
		Message: "the OperatorGroup required by the policy was not found",
	}
	opGroupCreatedCondition = metav1.Condition{
		Type:    opGroupConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  "OperatorGroupCreated",
		Message: "the OperatorGroup required by the policy was created",
	}
	opGroupMatchCondition = metav1.Condition{
		Type:    opGroupConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  "OperatorGroupMatches",
		Message: "the OperatorGroup matches the policy",
	}
	opGroupMismatchCondition = metav1.Condition{
		Type:    opGroupConditionType,
		Status:  metav1.ConditionFalse,
		Reason:  "OperatorGroupMismatch",
		Message: "the OperatorGroup found on the cluster does not match the policy",
	}
	opGroupUpdatedCondition = metav1.Condition{
		Type:    opGroupConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  "OperatorGroupUpdated",
		Message: "the OperatorGroup was updated to match the policy",
	}
	opGroupTooManyCondition = metav1.Condition{
		Type:    opGroupConditionType,
		Status:  metav1.ConditionFalse,
		Reason:  "TooManyOperatorGroups",
		Message: "there is more than one OperatorGroup in the namespace",
	}
)

func opGroupMissingObj(opGroup *operatorv1.OperatorGroup) policyv1.RelatedObject {
	return policyv1.RelatedObject{
		Object: policyv1.ObjectResource{
			Kind:       operatorGroupGVK.Kind,
			APIVersion: operatorGroupGVK.GroupVersion().String(),
			Metadata: policyv1.ObjectMetadata{
				Name:      opGroup.Name,
				Namespace: opGroup.Namespace,
			},
		},
		Compliant: string(policyv1.NonCompliant),
		Reason:    reasonWantFoundDNE,
	}
}

func opGroupCreatedObj(opGroup *operatorv1.OperatorGroup) policyv1.RelatedObject {
	created := true

	return policyv1.RelatedObject{
		Object: policyv1.ObjectResource{
			Kind:       operatorGroupGVK.Kind,
			APIVersion: operatorGroupGVK.GroupVersion().String(),
			Metadata: policyv1.ObjectMetadata{
				Name:      opGroup.Name,
				Namespace: opGroup.Namespace,
			},
		},
		Compliant: string(policyv1.Compliant),
		Reason:    reasonWantFoundCreated,
		Properties: &policyv1.ObjectProperties{
			CreatedByPolicy: &created,
			UID:             string(opGroup.UID),
		},
	}
}

func opGroupMatchObj(opGroup unstructured.Unstructured) policyv1.RelatedObject {
	return policyv1.RelatedObject{
		Object: policyv1.ObjectResource{
			Kind:       operatorGroupGVK.Kind,
			APIVersion: operatorGroupGVK.GroupVersion().String(),
			Metadata: policyv1.ObjectMetadata{
				Name:      opGroup.GetName(),
				Namespace: opGroup.GetNamespace(),
			},
		},
		Compliant: string(policyv1.Compliant),
		Reason:    reasonWantFoundExists,
		Properties: &policyv1.ObjectProperties{
			UID: string(opGroup.GetUID()),
		},
	}
}

func opGroupMismatchObj(opGroup unstructured.Unstructured) policyv1.RelatedObject {
	return policyv1.RelatedObject{
		Object: policyv1.ObjectResource{
			Kind:       operatorGroupGVK.Kind,
			APIVersion: operatorGroupGVK.GroupVersion().String(),
			Metadata: policyv1.ObjectMetadata{
				Name:      opGroup.GetName(),
				Namespace: opGroup.GetNamespace(),
			},
		},
		Compliant: string(policyv1.NonCompliant),
		Reason:    reasonWantFoundNoMatch,
		Properties: &policyv1.ObjectProperties{
			UID: string(opGroup.GetUID()),
		},
	}
}

func opGroupUpdatedObj(opGroup *operatorv1.OperatorGroup) policyv1.RelatedObject {
	return policyv1.RelatedObject{
		Object: policyv1.ObjectResource{
			Kind:       operatorGroupGVK.Kind,
			APIVersion: operatorGroupGVK.GroupVersion().String(),
			Metadata: policyv1.ObjectMetadata{
				Name:      opGroup.Name,
				Namespace: opGroup.Namespace,
			},
		},
		Compliant: string(policyv1.Compliant),
		Reason:    reasonUpdateSuccess,
		Properties: &policyv1.ObjectProperties{
			UID: string(opGroup.UID),
		},
	}
}

func opGroupTooManyObjs(opGroups []unstructured.Unstructured) []policyv1.RelatedObject {
	objs := make([]policyv1.RelatedObject, len(opGroups))

	for i, opGroup := range opGroups {
		objs[i] = policyv1.RelatedObject{
			Object: policyv1.ObjectResource{
				Kind:       operatorGroupGVK.Kind,
				APIVersion: operatorGroupGVK.GroupVersion().String(),
				Metadata: policyv1.ObjectMetadata{
					Name:      opGroup.GetName(),
					Namespace: opGroup.GetNamespace(),
				},
			},
			Compliant: string(policyv1.NonCompliant),
			Reason:    reasonTooManyOperatorGroups,
			Properties: &policyv1.ObjectProperties{
				UID: string(opGroup.GetUID()),
			},
		}
	}

	return objs
}

// updateStatus takes one condition to update, and related objects for that condition. The related
// objects given will replace all existing relatedObjects with the same gvk. If a condition is
// changed, the compliance will be recalculated and a compliance event will be emitted. The
// condition and related objects can match what is already in the status - in that case, no API
// calls are made. The `lastTransitionTime` on a condition is not considered when checking if the
// condition has changed. If not provided, the `lastTransitionTime` will use "now". It also handles
// preserving the `CreatedByPolicy` property on relatedObjects.
//
// This function assumes that all given related objects are of the same kind.
//
// Note that only changing the related objects will not emit a new compliance event, but will update
// the status.
func (r *OperatorPolicyReconciler) updateStatus(
	ctx context.Context,
	policy *policyv1beta1.OperatorPolicy,
	updatedCondition metav1.Condition,
	updatedRelatedObjs ...policyv1.RelatedObject,
) error {
	condChanged := false

	if updatedCondition.LastTransitionTime.IsZero() {
		updatedCondition.LastTransitionTime = metav1.Now()
	}

	condIdx, existingCondition := policy.Status.GetCondition(updatedCondition.Type)
	if condIdx == -1 {
		condChanged = true

		// Just append, the conditions will be sorted later.
		policy.Status.Conditions = append(policy.Status.Conditions, updatedCondition)
	} else if conditionChanged(updatedCondition, existingCondition) {
		condChanged = true

		policy.Status.Conditions[condIdx] = updatedCondition
	}

	if condChanged {
		updatedComplianceCondition := calculateComplianceCondition(policy)

		compCondIdx, _ := policy.Status.GetCondition(updatedComplianceCondition.Type)
		if compCondIdx == -1 {
			policy.Status.Conditions = append(policy.Status.Conditions, updatedComplianceCondition)
		} else {
			policy.Status.Conditions[compCondIdx] = updatedComplianceCondition
		}

		// Sort the conditions based on their type.
		sort.SliceStable(policy.Status.Conditions, func(i, j int) bool {
			return policy.Status.Conditions[i].Type < policy.Status.Conditions[j].Type
		})

		if updatedComplianceCondition.Status == metav1.ConditionTrue {
			policy.Status.ComplianceState = policyv1.Compliant
		} else {
			policy.Status.ComplianceState = policyv1.NonCompliant
		}

		err := r.emitComplianceEvent(ctx, policy, updatedComplianceCondition)
		if err != nil {
			return err
		}
	}

	relObjsChanged := false

	prevRelObjs := policy.Status.RelatedObjsOfKind(updatedRelatedObjs[0].Object.Kind)
	if len(prevRelObjs) == len(updatedRelatedObjs) {
		for _, prevObj := range prevRelObjs {
			nameFound := false

			for i, updatedObj := range updatedRelatedObjs {
				if prevObj.Object.Metadata.Name == updatedObj.Object.Metadata.Name {
					nameFound = true

					if updatedObj.Properties != nil && prevObj.Properties != nil {
						if updatedObj.Properties.UID != prevObj.Properties.UID {
							relObjsChanged = true
						} else if prevObj.Properties.CreatedByPolicy != nil {
							// There is an assumption here that it will never need to transition to false.
							updatedRelatedObjs[i].Properties.CreatedByPolicy = prevObj.Properties.CreatedByPolicy
						}
					}

					if prevObj.Compliant != updatedObj.Compliant {
						relObjsChanged = true
					} else if prevObj.Reason != updatedObj.Reason {
						relObjsChanged = true
					}
				}
			}

			if !nameFound {
				relObjsChanged = true
			}
		}
	} else {
		relObjsChanged = true
	}

	if relObjsChanged {
		// start with the related objects which do not match the currently considered kind
		newRelObjs := make([]policyv1.RelatedObject, 0)

		for idx, relObj := range policy.Status.RelatedObjects {
			if _, matchedIdx := prevRelObjs[idx]; !matchedIdx {
				newRelObjs = append(newRelObjs, relObj)
			}
		}

		// add the new related objects
		newRelObjs = append(newRelObjs, updatedRelatedObjs...)

		// sort the related objects by kind and name
		sort.SliceStable(newRelObjs, func(i, j int) bool {
			if newRelObjs[i].Object.Kind != newRelObjs[j].Object.Kind {
				return newRelObjs[i].Object.Kind < newRelObjs[j].Object.Kind
			}

			return newRelObjs[i].Object.Metadata.Name < newRelObjs[j].Object.Metadata.Name
		})

		policy.Status.RelatedObjects = newRelObjs
	}

	if condChanged || relObjsChanged {
		return r.Status().Update(ctx, policy)
	}

	return nil
}

func (r *OperatorPolicyReconciler) emitComplianceEvent(
	ctx context.Context,
	policy *policyv1beta1.OperatorPolicy,
	complianceCondition metav1.Condition,
) error {
	if len(policy.OwnerReferences) == 0 {
		return nil // there is nothing to do, since no owner is set
	}

	ownerRef := policy.OwnerReferences[0]
	now := time.Now()
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			// This event name matches the convention of recorders from client-go
			Name:      fmt.Sprintf("%v.%x", ownerRef.Name, now.UnixNano()),
			Namespace: policy.Namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       ownerRef.Kind,
			Namespace:  policy.Namespace, // k8s ensures owners are always in the same namespace
			Name:       ownerRef.Name,
			UID:        ownerRef.UID,
			APIVersion: ownerRef.APIVersion,
		},
		Reason:  fmt.Sprintf(eventFmtStr, policy.Namespace, policy.Name),
		Message: complianceCondition.Message,
		Source: corev1.EventSource{
			Component: ControllerName,
			Host:      r.InstanceName,
		},
		FirstTimestamp: metav1.NewTime(now),
		LastTimestamp:  metav1.NewTime(now),
		Count:          1,
		Type:           "Normal",
		Action:         "ComplianceStateUpdate",
		Related: &corev1.ObjectReference{
			Kind:       policy.Kind,
			Namespace:  policy.Namespace,
			Name:       policy.Name,
			UID:        policy.UID,
			APIVersion: policy.APIVersion,
		},
		ReportingController: ControllerName,
		ReportingInstance:   r.InstanceName,
	}

	if policy.Status.ComplianceState != policyv1.Compliant {
		event.Type = "Warning"
	}

	return r.Create(ctx, event)
}

func conditionChanged(updatedCondition, existingCondition metav1.Condition) bool {
	if updatedCondition.Message != existingCondition.Message {
		return true
	}

	if updatedCondition.Reason != existingCondition.Reason {
		return true
	}

	if updatedCondition.Status != existingCondition.Status {
		return true
	}

	return false
}

// The Compliance condition is calculated by going through the known conditions in a consistent
// order, checking if there are any reasons the policy should be NonCompliant, and accumulating
// the reasons into one string to reflect the whole status.
func calculateComplianceCondition(policy *policyv1beta1.OperatorPolicy) metav1.Condition {
	foundNonCompliant := false
	messages := make([]string, 0)

	idx, cond := policy.Status.GetCondition(opGroupConditionType)
	if idx == -1 {
		messages = append(messages, "the status of the OperatorGroup is unknown")
		foundNonCompliant = true
	} else {
		messages = append(messages, cond.Message)

		if cond.Status != metav1.ConditionTrue {
			foundNonCompliant = true
		}
	}

	// TODO: check additional conditions

	if foundNonCompliant {
		return metav1.Condition{
			Type:    compliantConditionType,
			Status:  metav1.ConditionFalse,
			Reason:  "NonCompliant",
			Message: "NonCompliant; " + strings.Join(messages, ", "),
		}
	}

	return metav1.Condition{
		Type:    compliantConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  "Compliant",
		Message: "Compliant; " + strings.Join(messages, ", "),
	}
}

// createPolicyResources encapsulates the logic for creating resources specified within an operator policy.
// This should normally happen when the policy is initially applied to the cluster. Creates an OperatorGroup
// if specified, otherwise defaults to using an allnamespaces OperatorGroup.
func (r *OperatorPolicyReconciler) createPolicyResources(
	ctx context.Context,
	policy *policyv1beta1.OperatorPolicy,
	cachedSubscription *unstructured.Unstructured,
) (ctrl.Result, error) {
	OpLog := ctrl.LoggerFrom(ctx)

	// Create new subscription
	if cachedSubscription == nil {
		if policy.Spec.RemediationAction.IsEnforce() {
			subscriptionSpec := buildSubscription(policy)

			OpLog.Info("Creating the subscription",
				"SubscriptionName", subscriptionSpec.Name,
				"SubscriptionNamespace", subscriptionSpec.Namespace)

			err := r.Create(ctx, subscriptionSpec)
			if err != nil {
				OpLog.Error(err, "Could not handle missing musthave object")
				r.setCompliance(ctx, policy, policyv1.NonCompliant)

				return reconcile.Result{}, err
			}

			// Future work: Check availability/status of all resources
			// managed by the OperatorPolicy before setting compliance state
			r.setCompliance(ctx, policy, policyv1.Compliant)

			return reconcile.Result{}, nil
		}
		// inform
		r.setCompliance(ctx, policy, policyv1.NonCompliant)
	}

	// Will only reach this if in inform mode
	return reconcile.Result{}, nil
}

// updatePolicyStatus updates the status of the operatorPolicy.
// In the future, a condition should be added as well, and this should generate events.
func (r *OperatorPolicyReconciler) updatePolicyStatus(
	ctx context.Context,
	policy *policyv1beta1.OperatorPolicy,
) error {
	OpLog := ctrl.LoggerFrom(ctx)
	updatedStatus := policy.Status

	err := r.Get(ctx, types.NamespacedName{Namespace: policy.Namespace, Name: policy.Name}, policy)
	if err != nil {
		OpLog.Info("Failed to refresh policy; using previously fetched version", "IgnoredError", err)
	} else {
		policy.Status = updatedStatus
	}

	err = r.Status().Update(ctx, policy)
	if err != nil {
		return err
	}

	return nil
}

// buildSubscription bootstraps the subscription spec defined in the operator policy
// with the apiversion and kind in preparation for resource creation
func buildSubscription(
	policy *policyv1beta1.OperatorPolicy,
) *operatorv1alpha1.Subscription {
	subscription := new(operatorv1alpha1.Subscription)

	subscription.SetGroupVersionKind(subscriptionGVK)
	subscription.ObjectMeta.Name = policy.Spec.Subscription.Package
	subscription.ObjectMeta.Namespace = policy.Spec.Subscription.Namespace
	subscription.Spec = policy.Spec.Subscription.SubscriptionSpec.DeepCopy()

	return subscription
}

// Sets the compliance of the policy
func (r *OperatorPolicyReconciler) setCompliance(
	ctx context.Context,
	policy *policyv1beta1.OperatorPolicy,
	compliance policyv1.ComplianceState,
) {
	OpLog := ctrl.LoggerFrom(ctx)

	policy.Status.ComplianceState = compliance

	err := r.updatePolicyStatus(ctx, policy)
	if err != nil {
		OpLog.Error(err, "error while updating policy status")
	}
}

// buildOperatorGroup bootstraps the OperatorGroup spec defined in the operator policy
// with the apiversion and kind in preparation for resource creation
func buildOperatorGroup(
	policy *policyv1beta1.OperatorPolicy,
) *operatorv1.OperatorGroup {
	operatorGroup := new(operatorv1.OperatorGroup)

	// Fallback to the Subscription namespace if the OperatorGroup namespace is not specified in the policy.
	ogNamespace := policy.Spec.Subscription.Namespace
	if policy.Spec.OperatorGroup != nil && policy.Spec.OperatorGroup.Namespace != "" {
		ogNamespace = policy.Spec.OperatorGroup.Namespace
	}

	// Create a default OperatorGroup if one wasn't specified in the policy
	if policy.Spec.OperatorGroup == nil {
		operatorGroup.SetGroupVersionKind(operatorGroupGVK)
		operatorGroup.ObjectMeta.SetNamespace(ogNamespace)
		operatorGroup.ObjectMeta.SetGenerateName(ogNamespace + "-") // This matches what the console creates
		operatorGroup.Spec.TargetNamespaces = []string{}
	} else {
		operatorGroup.SetGroupVersionKind(operatorGroupGVK)
		operatorGroup.ObjectMeta.SetName(policy.Spec.OperatorGroup.Name)
		operatorGroup.ObjectMeta.SetNamespace(ogNamespace)
		operatorGroup.Spec = policy.Spec.OperatorGroup.OperatorGroupSpec
	}

	return operatorGroup
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
