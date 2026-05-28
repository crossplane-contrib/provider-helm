package helm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
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
		Templates: []*chart.File{
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

	cfg := &action.Configuration{
		Log: func(format string, v ...interface{}) {
			t.Logf(format, v...)
		},
	}
	install := action.NewInstall(cfg)
	install.ClientOnly = true
	install.DryRun = true
	install.Namespace = "default"

	hc := &client{
		installClient: install,
	}

	_, err := hc.Install("schema-ref-regression", chrt, map[string]interface{}{
		"affinity": map[string]interface{}{
			"nodeAffinity": map[string]interface{}{},
		},
	}, nil)
	if err != nil {
		t.Fatalf("expected install to resolve HTTP schema reference: %v", err)
	}
}
