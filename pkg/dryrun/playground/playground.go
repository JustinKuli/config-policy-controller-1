// Copyright Contributors to the Open Cluster Management project

package playground

import (
	"context"
	"errors"
	"strings"

	"github.com/stolostron/go-template-utils/v7/pkg/lint"
	"open-cluster-management.io/config-policy-controller/pkg/dryrun"
)

// LintIssue is a policy template lint finding for the web playground.
type LintIssue struct {
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	RuleID   string `json:"ruleId"`
	Source   string `json:"source"`
}

// Evaluate runs a ConfigurationPolicy against simulated cluster resources.
func Evaluate(
	ctx context.Context,
	policyYAML, resourcesYAML, additionalMappingsYAML string,
) (dryrun.EvaluateResult, error) {
	return dryrun.Evaluate(ctx, dryrun.EvaluateInput{
		PolicyYAML:             policyYAML,
		ResourcesYAML:          resourcesYAML,
		AdditionalMappingsYAML: additionalMappingsYAML,
	})
}

// IsEvaluateSuccess reports whether err is only a non-compliance result.
func IsEvaluateSuccess(err error) bool {
	return err == nil || errors.Is(err, dryrun.ErrNonCompliant)
}

// EvaluateErrorStatus maps evaluation errors to HTTP status codes.
func EvaluateErrorStatus(err error) int {
	msg := err.Error()

	if strings.Contains(msg, "unable to read input policy") ||
		strings.Contains(msg, "unable to read input resources") ||
		strings.Contains(msg, "unable to apply input resources") ||
		strings.Contains(msg, "unable to read additional API mappings") {
		return 400
	}

	return 500
}

// LintPolicy runs go-template-utils policy template lint on the policy spec.
func LintPolicy(policyYAML string) []LintIssue {
	return violationsToIssues(lint.Lint(policyYAML))
}

func violationsToIssues(violations []lint.LinterRuleViolation) []LintIssue {
	if len(violations) == 0 {
		return []LintIssue{}
	}

	issues := make([]LintIssue, 0, len(violations))

	for _, violation := range violations {
		severity := "warning"

		if metadata := lint.GetRuleMetadata(violation.RuleID); metadata != nil {
			severity = metadata.Level
		}

		column := violation.Column
		if column == 0 {
			column = 1
		}

		issues = append(issues, LintIssue{
			Line:     violation.LineNumber,
			Column:   column,
			Severity: severity,
			Message:  violation.Message,
			RuleID:   violation.RuleID,
			Source:   "Policy",
		})
	}

	return issues
}
