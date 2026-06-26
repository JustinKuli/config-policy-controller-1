// Copyright Contributors to the Open Cluster Management project

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	policyv1 "open-cluster-management.io/config-policy-controller/api/v1"
	"open-cluster-management.io/config-policy-controller/pkg/dryrun"
)

const testPolicy = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata:
  name: hello
  namespace: default
spec:
  remediationAction: inform
  namespaceSelector:
    include: ["default"]
  object-templates:
    - complianceType: musthave
      objectDefinition:
        apiVersion: v1
        kind: Pod
        metadata:
          name: nginx-pod-e2e
          namespace: default
        spec:
          containers:
            - image: nginx:1.7.9
              name: nginx
              ports:
                - containerPort: 80
`

const testResources = `apiVersion: v1
kind: Pod
metadata:
  name: nginx-pod-e2e
  namespace: default
spec:
  containers:
    - image: nginx:1.7.9
      name: nginx
      ports:
        - containerPort: 80
`

func TestHandleEvaluateCompliant(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{})
	body := marshalEvaluateRequest(t, evaluateRequest{
		Policy:    testPolicy,
		Resources: testResources,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp dryrun.EvaluateResult

	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	if resp.ComplianceState != policyv1.Compliant {
		t.Fatalf("expected Compliant, got %q", resp.ComplianceState)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected compliance messages")
	}

	if len(resp.Status.RelatedObjects) == 0 {
		t.Fatal("expected related objects in status")
	}
}

func TestHandleEvaluateInvalidPolicy(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{})
	body := marshalEvaluateRequest(t, evaluateRequest{
		Policy:    "not yaml: [",
		Resources: testResources,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/evaluate", bytes.NewReader(body))
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

func TestHandleEvaluateInvalidResources(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{})
	invalidResources := testResources + `
---
apiVersion: v1
kind: Pod
metadata:
  name: bad-pod
  namespace: default
spec:
  containers:
    - image: nginx:1.7.9
      name: nginx
      ports:
        - containerPort: "not-a-number"
`
	body := marshalEvaluateRequest(t, evaluateRequest{
		Policy:    testPolicy,
		Resources: invalidResources,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/evaluate", bytes.NewReader(body))
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

func TestHandleEvaluateAdditionalMappings(t *testing.T) {
	t.Parallel()

	const additionalMappings = `- Group: ""
  Kind: Fake
  Plural: fakes
  Scope: root
  Singular: fake
  Version: v1
`
	const policy = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata:
  name: fake-policy
  namespace: default
spec:
  remediationAction: inform
  namespaceSelector:
    include: ["default"]
  object-templates:
    - complianceType: musthave
      objectDefinition:
        apiVersion: v1
        kind: Fake
        metadata:
          name: test-fake
`
	const resources = `apiVersion: v1
kind: Fake
metadata:
  name: test-fake
`

	handler := NewHandler(Config{})
	body := marshalEvaluateRequest(t, evaluateRequest{
		Policy:             policy,
		Resources:          resources,
		AdditionalMappings: additionalMappings,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp dryrun.EvaluateResult

	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	if resp.ComplianceState != policyv1.Compliant {
		t.Fatalf("expected Compliant, got %q", resp.ComplianceState)
	}
}

func TestHandleEvaluateInvalidAdditionalMappings(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{})
	body := marshalEvaluateRequest(t, evaluateRequest{
		Policy:             testPolicy,
		Resources:          testResources,
		AdditionalMappings: "not yaml: [",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleEvaluateMethodNotAllowed(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/evaluate", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rec.Code)
	}
}

func TestNewHandlerStaticFS(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Config{StaticFS: os.DirFS(dir)})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}

func TestNewHandlerAPIOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Config{StaticFS: os.DirFS(dir), APIOnly: true})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
}

func marshalEvaluateRequest(t *testing.T, req evaluateRequest) []byte {
	t.Helper()

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	return body
}
