// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"

	"open-cluster-management.io/config-policy-controller/test/utils"
)

var _ = Describe("Test OperatorPolicy", func() {
	Describe("Using a policy specifying an OperatorGroup definition", Ordered, func() {
		const (
			operatorNS = "test-op-pol-one"
			policyYAML = "../resources/case38_operator_policy/operatorpolicy.yaml"
			policyName = "test-operator-policy-one"
		)

		BeforeAll(func() {
			By("Creating a namespace for the test")
			utils.Kubectl("create", "namespace", operatorNS)
		})

		AfterAll(func() {
			By("Deleting the test's namespace")
			utils.Kubectl("delete", "namespace", operatorNS)

			utils.GetWithTimeout(clientManagedDynamic, gvrNS, operatorNS, "", false, defaultTimeoutSeconds)
		})

		It("installs the operator from scratch", func() {
			By("Applying the operator policy")
			utils.Kubectl("apply", "-f", policyYAML, "-n", operatorNS)

			By("Checking the specified operator group is created")
			utils.GetWithTimeout(clientManagedDynamic, gvrOperatorGroup,
				"og-single", operatorNS, true, defaultTimeoutSeconds*2)

			By("Checking the subscription is created")
			utils.GetWithTimeout(clientManagedDynamic, gvrSubscription,
				"project-quay", operatorNS, true, defaultTimeoutSeconds*2)
		})
	})

	Describe("Using a policy that omits the OperatorGroup definition", Ordered, func() {
		const (
			operatorNS = "test-op-pol-two"
			policyYAML = "../resources/case38_operator_policy/operatorpolicy2.yaml"
			policyName = "quay-example"
		)

		BeforeAll(func() {
			By("Creating a namespace for the test")
			utils.Kubectl("create", "namespace", operatorNS)
		})

		AfterAll(func() {
			By("Deleting the test's namespace")
			utils.Kubectl("delete", "namespace", operatorNS)

			utils.GetWithTimeout(clientManagedDynamic, gvrNS, operatorNS, "", false, defaultTimeoutSeconds)
		})

		It("installs the operator from scratch", func() {
			By("Applying the operator policy")
			utils.Kubectl("apply", "-f", policyYAML, "-n", operatorNS)

			By("Checking the specified operator group is created")
			utils.GetWithTimeout(clientManagedDynamic, gvrOperatorGroup,
				policyName+"-default-og", operatorNS, true, defaultTimeoutSeconds*2)

			By("Checking the subscription is created")
			utils.GetWithTimeout(clientManagedDynamic, gvrSubscription,
				"project-quay", operatorNS, true, defaultTimeoutSeconds*2)
		})
	})
})
