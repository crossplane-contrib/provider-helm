package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/resid"
	"sigs.k8s.io/kustomize/api/types"

	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
)

const (
	testPatchConfig = `
patches:
- patch: |-
    - op: add
      path: /spec/template/spec/nodeSelector
      value:
        node.size: really-big
        aws.az: us-west-2a
        patch.name: %s
  target:
    kind: Deployment
`
)

func Test_getPatchesFromSpec(t *testing.T) {
	type args struct {
		kube client.Client
		spec []v1alpha1.ValueFromSource
	}

	type want struct {
		out []types.Patch
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"loadOneRequiredPatch": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							s := corev1.ConfigMap{
								Data: map[string]string{
									keyDefaultPatchFrom: fmt.Sprintf(testPatchConfig, key.Name),
								},
							}
							*obj.(*corev1.ConfigMap) = s
							return nil
						}
						return errBoom
					},
				},
				spec: []v1alpha1.ValueFromSource{
					{
						ConfigMapKeyRef: &v1alpha1.DataKeySelector{
							NamespacedName: v1alpha1.NamespacedName{
								Name:      testCMName,
								Namespace: testNamespace,
							},
							Key:      keyDefaultPatchFrom,
							Optional: false,
						},
					},
				},
			},
			want: want{
				out: []types.Patch{
					{
						Patch: "- op: add\n  path: /spec/template/spec/nodeSelector\n  value:\n    node.size: really-big\n    aws.az: us-west-2a\n    patch.name: " + testCMName,
						Target: &types.Selector{
							Gvk: resid.Gvk{
								Kind: "Deployment",
							},
						},
					},
				},
				err: nil,
			},
		},
		"loadThreePatches": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						s := corev1.ConfigMap{
							Data: map[string]string{
								keyDefaultPatchFrom: fmt.Sprintf(testPatchConfig, key.Name),
							},
						}
						*obj.(*corev1.ConfigMap) = s
						return nil
					},
				},
				spec: []v1alpha1.ValueFromSource{
					{
						ConfigMapKeyRef: &v1alpha1.DataKeySelector{
							NamespacedName: v1alpha1.NamespacedName{
								Name:      "1",
								Namespace: testNamespace,
							},
							Key:      keyDefaultPatchFrom,
							Optional: false,
						},
					},
					{
						ConfigMapKeyRef: &v1alpha1.DataKeySelector{
							NamespacedName: v1alpha1.NamespacedName{
								Name:      "2",
								Namespace: testNamespace,
							},
							Key:      keyDefaultPatchFrom,
							Optional: false,
						},
					},
					{
						ConfigMapKeyRef: &v1alpha1.DataKeySelector{
							NamespacedName: v1alpha1.NamespacedName{
								Name:      "3",
								Namespace: testNamespace,
							},
							Key:      keyDefaultPatchFrom,
							Optional: false,
						},
					},
				},
			},
			want: want{
				out: []types.Patch{
					{
						Patch: "- op: add\n  path: /spec/template/spec/nodeSelector\n  value:\n    node.size: really-big\n    aws.az: us-west-2a\n    patch.name: 1",
						Target: &types.Selector{
							Gvk: resid.Gvk{
								Kind: "Deployment",
							},
						},
					},
					{
						Patch: "- op: add\n  path: /spec/template/spec/nodeSelector\n  value:\n    node.size: really-big\n    aws.az: us-west-2a\n    patch.name: 2",
						Target: &types.Selector{
							Gvk: resid.Gvk{
								Kind: "Deployment",
							},
						},
					},
					{
						Patch: "- op: add\n  path: /spec/template/spec/nodeSelector\n  value:\n    node.size: really-big\n    aws.az: us-west-2a\n    patch.name: 3",
						Target: &types.Selector{
							Gvk: resid.Gvk{
								Kind: "Deployment",
							},
						},
					},
				},
				err: nil,
			},
		},
		"noPatchLoadedOptional": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						return kerrors.NewNotFound(schema.GroupResource{Group: "ConfigMap", Resource: key.Name}, key.Name)
					},
				},
				spec: []v1alpha1.ValueFromSource{
					{
						ConfigMapKeyRef: &v1alpha1.DataKeySelector{
							NamespacedName: v1alpha1.NamespacedName{
								Name:      "1",
								Namespace: testNamespace,
							},
							Key:      keyDefaultPatchFrom,
							Optional: true,
						},
					},
				},
			},
			want: want{
				out: nil,
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, gotErr := getPatchesFromSpec(context.Background(), tc.args.kube, tc.args.spec)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("getPatchesFromSpec(...): -want error, +got error: %s", diff)
			}
			if diff := cmp.Diff(tc.want.out, got); diff != "" {
				t.Errorf("getPatchesFromSpec(...): -want result, +got result: %s", diff)
			}
		})
	}
}
