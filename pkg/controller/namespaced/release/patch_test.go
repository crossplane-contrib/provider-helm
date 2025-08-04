package release

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/resid"

	"github.com/crossplane-contrib/provider-helm/apis/namespaced/release/v1beta1"
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
	testMockPatchSha = "magicsha"
)

func Test_ShaOf(t *testing.T) {
	pd := types.Patch{
		Patch: "- op: add\n  path: /spec/template/spec/nodeSelector\n  value:\n    node.size: really-big\n    aws.az: us-west-2a\n    patch.name: " + testCMName,
		Target: &types.Selector{
			ResId: resid.ResId{
				Gvk: resid.Gvk{Kind: "Deployment"},
			},
		},
	}
	type args struct {
		patches []types.Patch
	}

	type want struct {
		sha string
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"SuccessSha": {
			args: args{
				patches: []types.Patch{
					pd,
				},
			},
			want: want{
				sha: "a38aee2ef839c3e754444bf80c625900d4b38102591083069bd8f5e55389e8c2",
				err: nil,
			},
		},
		"Success2Sha": {
			args: args{
				patches: []types.Patch{
					pd,
					pd,
				},
			},
			want: want{
				sha: "770183113f382b18c0a0c0343e735852023331b38db70de4f2111aa85d765ec9",
				err: nil,
			},
		},
		"SuccessEmptyPatchSha": {
			args: args{
				patches: []types.Patch{},
			},
			want: want{
				sha: "",
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := patchSha{}
			got, gotErr := p.shaOf(tc.args.patches)

			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("shaOf(...): -want error, +got error: %s", diff)
			}

			if diff := cmp.Diff(tc.want.sha, got); diff != "" {
				t.Errorf("shaOf(...): -want result, +got result: %s", diff)
			}
		})
	}
}

type mockShaOf struct {
	sum string
	err error
}

func (m mockShaOf) shaOf(patches []types.Patch) (string, error) {
	return m.sum, m.err
}

type mockPatchGet struct {
	patches []types.Patch
	err     error
}

func (m mockPatchGet) getFromSpec(ctx context.Context, kube client.Client, vals []v1beta1.ValueFromSource) ([]types.Patch, error) {
	return m.patches, m.err
}

func Test_PatchHasUpdates(t *testing.T) {
	pd := types.Patch{
		Patch: "- op: add\n  path: /spec/template/spec/nodeSelector\n  value:\n    node.size: really-big\n    aws.az: us-west-2a\n    patch.name: " + testCMName,
		Target: &types.Selector{
			ResId: resid.ResId{
				Gvk: resid.Gvk{Kind: "Deployment"},
			},
		},
	}
	type args struct {
		existingSha           string
		getPatchesFromSpec    []types.Patch
		getPatchesFromSpecErr error
		shaOf                 string
		shaOfErr              error
	}

	type want struct {
		updates bool
		err     error
	}
	cases := map[string]struct {
		args
		want
	}{
		"SuccessNoUpdates": {
			args: args{
				shaOf: testMockPatchSha,
				getPatchesFromSpec: []types.Patch{
					pd,
				},
				existingSha: testMockPatchSha,
			},
			want: want{
				updates: false,
				err:     nil,
			},
		},
		"SuccessUpdatesNewSha": {
			args: args{
				shaOf: testMockPatchSha,
				getPatchesFromSpec: []types.Patch{
					pd,
				},
				existingSha: "",
			},
			want: want{
				updates: true,
				err:     nil,
			},
		},
		"SuccessUpdatesDifferentSha": {
			args: args{
				shaOf: testMockPatchSha,
				getPatchesFromSpec: []types.Patch{
					pd,
				},
				existingSha: "nonMatchingSha",
			},
			want: want{
				updates: true,
				err:     nil,
			},
		},
		"ErrGetPatches": {
			args: args{
				getPatchesFromSpecErr: fmt.Errorf("boom"),
			},
			want: want{
				updates: false,
				err:     fmt.Errorf("boom"),
			},
		},
		"ErrGetSha": {
			args: args{
				shaOfErr: fmt.Errorf("boom"),
			},
			want: want{
				updates: false,
				err:     fmt.Errorf("boom"),
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := patch{
				patchHasher: mockShaOf{
					sum: tc.shaOf,
					err: tc.shaOfErr,
				},
				patchGetter: mockPatchGet{
					patches: tc.args.getPatchesFromSpec,
					err:     tc.args.getPatchesFromSpecErr,
				},
			}
			s := v1beta1.ReleaseStatus{
				PatchesSha: tc.existingSha,
			}
			got, gotErr := p.hasUpdates(context.Background(), nil, nil, s)

			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("Patch.hasUpdates(...): -want error, +got error: %s", diff)
			}
			if diff := cmp.Diff(tc.want.updates, got); diff != "" {
				t.Errorf("Patch.hasUpdates(...): -want result, +got result: %s", diff)
			}
		})
	}
}

func Test_getPatchesFromSpec(t *testing.T) {
	type args struct {
		kube client.Client
		spec []v1beta1.ValueFromSource
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
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
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
				spec: []v1beta1.ValueFromSource{
					{
						ConfigMapKeyRef: &v1beta1.DataKeySelector{
							NamespacedName: v1beta1.NamespacedName{
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
							ResId: resid.ResId{
								Gvk: resid.Gvk{Kind: "Deployment"},
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
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						s := corev1.ConfigMap{
							Data: map[string]string{
								keyDefaultPatchFrom: fmt.Sprintf(testPatchConfig, key.Name),
							},
						}
						*obj.(*corev1.ConfigMap) = s
						return nil
					},
				},
				spec: []v1beta1.ValueFromSource{
					{
						ConfigMapKeyRef: &v1beta1.DataKeySelector{
							NamespacedName: v1beta1.NamespacedName{
								Name:      "1",
								Namespace: testNamespace,
							},
							Key:      keyDefaultPatchFrom,
							Optional: false,
						},
					},
					{
						ConfigMapKeyRef: &v1beta1.DataKeySelector{
							NamespacedName: v1beta1.NamespacedName{
								Name:      "2",
								Namespace: testNamespace,
							},
							Key:      keyDefaultPatchFrom,
							Optional: false,
						},
					},
					{
						ConfigMapKeyRef: &v1beta1.DataKeySelector{
							NamespacedName: v1beta1.NamespacedName{
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
							ResId: resid.ResId{
								Gvk: resid.Gvk{Kind: "Deployment"},
							},
						},
					},
					{
						Patch: "- op: add\n  path: /spec/template/spec/nodeSelector\n  value:\n    node.size: really-big\n    aws.az: us-west-2a\n    patch.name: 2",
						Target: &types.Selector{
							ResId: resid.ResId{
								Gvk: resid.Gvk{Kind: "Deployment"},
							},
						},
					},
					{
						Patch: "- op: add\n  path: /spec/template/spec/nodeSelector\n  value:\n    node.size: really-big\n    aws.az: us-west-2a\n    patch.name: 3",
						Target: &types.Selector{
							ResId: resid.ResId{
								Gvk: resid.Gvk{Kind: "Deployment"},
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
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						return kerrors.NewNotFound(schema.GroupResource{Group: "ConfigMap", Resource: key.Name}, key.Name)
					},
				},
				spec: []v1beta1.ValueFromSource{
					{
						ConfigMapKeyRef: &v1beta1.DataKeySelector{
							NamespacedName: v1beta1.NamespacedName{
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
			pg := patchGet{}
			got, gotErr := pg.getFromSpec(context.Background(), tc.args.kube, tc.args.spec)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("getFromSpec(...): -want error, +got error: %s", diff)
			}

			if diff := cmp.Diff(tc.want.out, got, cmpopts.IgnoreUnexported(resid.Gvk{})); diff != "" {
				t.Errorf("getFromSpec(...): -want result, +got result: %s", diff)
			}
		})
	}
}
