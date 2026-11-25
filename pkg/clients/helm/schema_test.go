package helm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/google/go-cmp/cmp"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"k8s.io/client-go/rest"
)

func TestInstallSupportsHTTPReferencedValuesSchema(t *testing.T) {
	schemaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/kubernetes-definitions.json" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/schema+json")
		_, _ = w.Write([]byte(`{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"definitions": {
				"io.k8s.api.core.v1.Affinity": {
					"type": "object",
					"properties": {
						"nodeAffinity": { "type": "object" }
					},
					"additionalProperties": true
				}
			}
		}`))
	}))
	t.Cleanup(schemaServer.Close)

	chrt := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:    "http-ref-schema",
			Version: "0.1.0",
		},
		Schema: []byte(fmt.Sprintf(`{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type": "object",
			"properties": {
				"affinity": {
					"$ref": "%s/kubernetes-definitions.json#/definitions/io.k8s.api.core.v1.Affinity"
				}
			}
		}`, schemaServer.URL)),
		Templates: []*common.File{
			{
				Name: "templates/configmap.yaml",
				Data: []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}
data:
  chart: {{ .Chart.Name }}
`),
			},
		},
	}

	cfg := &action.Configuration{}
	install := action.NewInstall(cfg)
	install.DryRunStrategy = action.DryRunClient
	install.Namespace = "default"

	hc := &client{
		installClient: install,
	}

	_, err := hc.Install("schema-ref-regression", chrt, map[string]interface{}{
		"affinity": map[string]interface{}{
			"nodeAffinity": map[string]interface{}{},
		},
	}, nil, DeployOptions{})
	if err != nil {
		t.Fatalf("expected install to resolve HTTP schema reference: %v", err)
	}
}

// TestSkipSchemaValidation installs values that the chart's values.schema.json
// rejects, with a client-only dry run so that no API server is needed.
func TestSkipSchemaValidation(t *testing.T) {
	chrt := &chart.Chart{
		Metadata: &chart.Metadata{
			APIVersion: chart.APIVersionV2,
			Name:       "strict-schema",
			Version:    "0.1.0",
		},
		Schema: []byte(`{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type": "object",
			"properties": {
				"replicas": { "type": "integer" }
			},
			"additionalProperties": false
		}`),
		Templates: []*common.File{
			{
				Name: "templates/configmap.yaml",
				Data: []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}
`),
			},
		},
	}
	vals := map[string]interface{}{"replicas": 1, "undeclared": "value"}

	type want struct {
		installFails                bool
		upgradeSkipSchemaValidation bool
	}
	cases := map[string]struct {
		skipSchemaValidation bool
		want                 want
	}{
		"ValidatedByDefault": {
			want: want{installFails: true},
		},
		"SkippedWhenSet": {
			skipSchemaValidation: true,
			want:                 want{upgradeSkipSchemaValidation: true},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// NewClient doesn't dial the API server, so a non-dialable host is safe.
			c, err := NewClient(logging.NewNopLogger(), &rest.Config{Host: "http://127.0.0.1:0"}, func(a *Args) {
				a.Namespace = "default"
				a.SkipSchemaValidation = tc.skipSchemaValidation
			})
			if err != nil {
				t.Fatalf("NewClient(...): unexpected error: %v", err)
			}
			cc, ok := c.(*client)
			if !ok {
				t.Fatalf("NewClient(...) did not return a *client")
			}
			cc.installClient.DryRunStrategy = action.DryRunClient

			_, installErr := cc.Install("strict-schema", chrt, vals, nil, DeployOptions{})

			got := want{
				installFails:                installErr != nil,
				upgradeSkipSchemaValidation: cc.upgradeClient.SkipSchemaValidation,
			}
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(want{})); diff != "" {
				t.Errorf("Install(...) with skipSchemaValidation=%t: -want, +got:\n%s\ninstall error: %v", tc.skipSchemaValidation, diff, installErr)
			}
		})
	}
}
