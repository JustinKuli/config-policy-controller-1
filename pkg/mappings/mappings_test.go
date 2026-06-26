// Copyright Contributors to the Open Cluster Management project

package mappings

import "testing"

func TestMergeAPIMappings(t *testing.T) {
	t.Parallel()

	base := []APIMapping{
		{Group: "", Version: "v1", Kind: "Pod", Plural: "pods", Singular: "pod", Scope: "namespace"},
		{Group: "", Version: "v1", Kind: "ConfigMap", Plural: "configmaps", Singular: "configmap", Scope: "namespace"},
	}

	additional := []APIMapping{
		{Group: "", Version: "v1", Kind: "Fake", Plural: "fakes", Singular: "fake", Scope: "root"},
		{Group: "", Version: "v1", Kind: "ConfigMap", Plural: "configmaps", Singular: "configmap", Scope: "root"},
	}

	merged := MergeAPIMappings(base, additional)

	if len(merged) != 3 {
		t.Fatalf("expected 3 mappings, got %d", len(merged))
	}

	if merged[2].Kind != "Fake" {
		t.Fatalf("expected appended Fake mapping, got %q", merged[2].Kind)
	}

	if merged[1].Scope != "root" {
		t.Fatalf("expected ConfigMap scope to be replaced with root, got %q", merged[1].Scope)
	}
}

func TestParseAPIMappingsYAML(t *testing.T) {
	t.Parallel()

	mappings, err := ParseAPIMappingsYAML([]byte(`- group: example.com
  version: v1
  kind: Widget
  plural: widgets
  singular: widget
  scope: namespace
`))
	if err != nil {
		t.Fatal(err)
	}

	if len(mappings) != 1 || mappings[0].Kind != "Widget" {
		t.Fatalf("unexpected mappings: %#v", mappings)
	}

	empty, err := ParseAPIMappingsYAML(nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(empty) != 0 {
		t.Fatalf("expected empty mappings, got %#v", empty)
	}
}
