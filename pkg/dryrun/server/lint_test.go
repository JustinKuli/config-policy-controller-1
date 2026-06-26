// Copyright Contributors to the Open Cluster Management project

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testPolicyWithUnquotedTemplate = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata:
  name: test
  namespace: default
spec:
  remediationAction: inform
  namespaceSelector:
    include: ["default"]
  object-templates:
    - complianceType: musthave
      objectDefinition:
        apiVersion: v1
        kind: ConfigMap
        metadata:
          name: {{ fromConfigMap "default" "x" "y" }}
          namespace: default
`

func TestHandleLintClean(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{})
	body := marshalLintRequest(t, testPolicy)

	req := httptest.NewRequest(http.MethodPost, "/api/lint", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp lintResponse

	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	if len(resp.Issues) != 0 {
		t.Fatalf("expected no issues, got %d", len(resp.Issues))
	}
}

func TestHandleLintViolations(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{})
	body := marshalLintRequest(t, testPolicyWithUnquotedTemplate)

	req := httptest.NewRequest(http.MethodPost, "/api/lint", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp lintResponse

	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	if len(resp.Issues) == 0 {
		t.Fatal("expected lint issues")
	}

	found := false

	for _, issue := range resp.Issues {
		if issue.RuleID != "GTUL003" {
			continue
		}

		found = true

		if issue.Severity != "warning" {
			t.Fatalf("expected warning severity, got %q", issue.Severity)
		}

		if issue.Source != "Policy" {
			t.Fatalf("expected source Policy, got %q", issue.Source)
		}

		if issue.Line == 0 {
			t.Fatal("expected line number")
		}

		if issue.Message == "" {
			t.Fatal("expected message")
		}
	}

	if !found {
		t.Fatal("expected GTUL003 unquoted template value violation")
	}
}

func TestHandleLintInvalidJSON(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/lint", bytes.NewReader([]byte("{")))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp evaluateErrorResponse

	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	if resp.Error == "" {
		t.Fatal("expected error message in response")
	}
}

func TestHandleLintMethodNotAllowed(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/lint", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rec.Code)
	}
}

func marshalLintRequest(t *testing.T, policy string) []byte {
	t.Helper()

	body, err := json.Marshal(lintRequest{Policy: policy})
	if err != nil {
		t.Fatal(err)
	}

	return body
}
