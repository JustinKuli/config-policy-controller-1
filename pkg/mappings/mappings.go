// Copyright Contributors to the Open Cluster Management project

package mappings

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubectl/pkg/scheme"
	"k8s.io/kubernetes/pkg/apis/apidiscovery"
)

// APIMapping stores information required for mapping between GroupVersionKinds
// and GroupVersionResources, as well as whether the API is namespaced.
type APIMapping struct {
	Group    string
	Version  string
	Kind     string
	Singular string
	Plural   string
	Scope    string
}

// MapperFrom takes a list of APIMappings and uses them to populate a
// DefaultRESTMapper which can be used locally without connecting to a Discovery
// client.
func MapperFrom(mappings []APIMapping) meta.RESTMapper {
	mapper := meta.NewDefaultRESTMapper(scheme.Scheme.PreferredVersionAllGroups())

	for _, mapping := range mappings {
		gv := schema.GroupVersion{
			Group:   mapping.Group,
			Version: mapping.Version,
		}

		if mapping.Scope == "Cluster" {
			mapper.AddSpecific(
				gv.WithKind(mapping.Kind),
				gv.WithResource(mapping.Plural),
				gv.WithResource(mapping.Singular),
				clusterScoped,
			)
		} else if mapping.Scope == "Namespaced" {
			mapper.AddSpecific(
				gv.WithKind(mapping.Kind),
				gv.WithResource(mapping.Plural),
				gv.WithResource(mapping.Singular),
				namespaced,
			)
		}
	}

	return mapper
}

//go:embed default-mappings.json
var defaultMappingsJSON []byte

// DefaultMapper returns a RESTMapper initialized on default mappings that were
// embedded into this package. It should include built-in Kubernetes types.
func DefaultMapper() (meta.RESTMapper, error) {
	mappings := []APIMapping{}

	if err := json.Unmarshal(defaultMappingsJSON, &mappings); err != nil {
		return nil, err
	}

	return MapperFrom(mappings), nil
}

// GenerateMappings connects to a Kubernetes cluster and discovers the available
// api-resources, printing out that information as a JSON list of APIMappings.
// The cluster connected to follows the usual conventions, eg it can be set with
// the KUBECONFIG environment variable. This function is meant to be used inside
// of a cobra-style CLI.
func GenerateMappings(cmd *cobra.Command, args []string) error {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})

	kubeConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}

	cli, err := rest.HTTPClientFor(kubeConfig)
	if err != nil {
		return err
	}

	apiMappings := []APIMapping{}

	for _, endpoint := range []string{"/api", "/apis"} {
		path := kubeConfig.Host + kubeConfig.APIPath + endpoint

		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, path, bytes.NewReader([]byte{}))
		if err != nil {
			return err
		}

		req.Header.Set("Accept", "application/json;g=apidiscovery.k8s.io;v=v2beta1;as=APIGroupDiscoveryList")

		res, err := cli.Do(req)
		if err != nil {
			return err
		}

		// The group name is not decoded into the APIGroupDiscoveryList for some reason,
		// so we need to read the body twice
		var buf bytes.Buffer
		tee := io.TeeReader(res.Body, &buf)

		discoveryList := apidiscovery.APIGroupDiscoveryList{}
		if err := json.NewDecoder(tee).Decode(&discoveryList); err != nil {
			return err
		}

		foo := listWithNames{}
		if err := json.NewDecoder(&buf).Decode(&foo); err != nil {
			return err
		}

		for i, item := range discoveryList.Items {
			for _, versionItem := range item.Versions {
				for _, resourceItem := range versionItem.Resources {
					apiMappings = append(apiMappings, APIMapping{
						Group:    foo.Items[i].Metadata.Name,
						Version:  versionItem.Version,
						Kind:     resourceItem.ResponseKind.Kind,
						Singular: resourceItem.SingularResource,
						Plural:   resourceItem.Resource,
						Scope:    string(resourceItem.Scope),
					})
				}
			}
		}
	}

	out, err := json.MarshalIndent(apiMappings, "", "  ")
	if err != nil {
		return err
	}

	//nolint:forbidigo
	fmt.Println(string(out))

	return nil
}

type listWithNames struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	} `json:"items"`
}

var (
	namespaced    scope = scope(meta.RESTScopeNameNamespace)
	clusterScoped scope = scope(meta.RESTScopeNameRoot)
)

type scope string

func (s scope) Name() meta.RESTScopeName {
	return meta.RESTScopeName(s)
}
