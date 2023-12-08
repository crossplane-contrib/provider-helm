package helm

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/errors"
	"sigs.k8s.io/kustomize/kyaml/resid"
)

const testDeployment = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-deployment
spec:
  selector:
    matchLabels:
      app: nginx
      env: dev
  template:
    metadata:
      labels:
        app: nginx
        env: dev
    spec:
      containers:
      - name: nginx
        image: nginx:1.14.2
        ports:
        - containerPort: 80
`

func TestKustomize(t *testing.T) {
	type want struct {
		result string
		err    error
	}

	tests := []struct {
		name    string
		base    string
		patches []types.Patch
		want    want
	}{
		{
			name: "BasicPatch",
			base: testDeployment,
			patches: []types.Patch{
				{
					Target: &types.Selector{
						ResId: resid.ResId{
							Gvk: resid.Gvk{Kind: "Deployment"},
						},
						AnnotationSelector: "",
						LabelSelector:      "",
					},
					Patch: "- op: add\n  path: /spec/template/spec/nodeSelector\n  value:\n    node.size: really-big\n    aws.az: us-west-2a",
				},
			},
			want: want{
				result: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: nginx-deployment\nspec:\n  selector:\n    matchLabels:\n      app: nginx\n      env: dev\n  template:\n    metadata:\n      labels:\n        app: nginx\n        env: dev\n    spec:\n      containers:\n      - image: nginx:1.14.2\n        name: nginx\n        ports:\n        - containerPort: 80\n      nodeSelector:\n        aws.az: us-west-2a\n        node.size: really-big\n",
			},
		},
		{
			name: "InvalidPatch",
			base: testDeployment,
			patches: []types.Patch{
				{
					Target: &types.Selector{ResId: resid.ResId{
						Gvk: resid.Gvk{Kind: "Deployment"},
					}},
					Patch: "- bad patch",
				},
			},
			want: want{
				result: "",
				err:    errors.WrapPrefixf(errors.Errorf("unable to parse SM or JSON patch from [- bad patch]"), "trouble configuring builtin PatchTransformer with config: `\npatch: '- bad patch'\ntarget:\n  kind: Deployment\n`"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := KustomizationRender{
				patches: tt.patches,
			}

			buf := bytes.NewBuffer([]byte(tt.base))
			resBuffer, gotErr := k.Run(buf)
			gotResult := ""
			if gotErr == nil {
				gotResult = resBuffer.String()
			}

			if diff := cmp.Diff(tt.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Errorf("Reconcile() -want error %s, +got error %s:\n%s", reflect.ValueOf(tt.want.err).Type(), reflect.ValueOf(gotErr).Type(), diff)
			}

			if diff := cmp.Diff(tt.want.result, gotResult); diff != "" {
				t.Errorf("Reconcile() -want, +got:\n%s", diff)
			}
		})
	}
}
