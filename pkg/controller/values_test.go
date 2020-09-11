package controller

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-helm/apis/release/v1alpha1"
)

func Test_composeValuesFromSpec(t *testing.T) {
	type args struct {
		kube client.Client
		spec v1alpha1.ValuesSpec
	}

	type want struct {
		out map[string]interface{}
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"OnlyInline": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: v1alpha1.ValuesSpec{
					Values: runtime.RawExtension{
						Raw: []byte(testReleaseConfigStr),
					},
					ValuesFrom: nil,
					Set:        nil,
				},
			},
			want: want{
				out: testReleaseConfig,
				err: nil,
			},
		},
		"FailedToMarshalInlineValues": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: v1alpha1.ValuesSpec{
					Values: runtime.RawExtension{
						Raw: []byte("invalid-yaml"),
					},
					ValuesFrom: nil,
					Set:        nil,
				},
			},
			want: want{
				err: errors.Wrap(errors.New("error unmarshaling JSON: while decoding JSON: "+
					"json: cannot unmarshal string into Go value of type map[string]interface {}"),
					errFailedToUnmarshalDesiredValues),
			},
		},
		"OnlyValuesFromCM": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							s := corev1.ConfigMap{
								Data: map[string]string{
									"values.yaml": testReleaseConfigStr,
								},
							}
							*obj.(*corev1.ConfigMap) = s
							return nil
						}
						return errBoom
					},
				},
				spec: v1alpha1.ValuesSpec{
					ValuesFrom: []v1alpha1.ValueFromSource{
						{
							ConfigMapKeyRef: &v1alpha1.DataKeySelector{
								NamespacedName: v1alpha1.NamespacedName{
									Name:      testCMName,
									Namespace: testNamespace,
								},
								Key:      "values.yaml",
								Optional: false,
							},
						},
					},
					Set: nil,
				},
			},
			want: want{
				out: testReleaseConfig,
				err: nil,
			},
		},
		"FailedToGetValuesFromSource": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							return errBoom
						}
						return nil
					},
				},
				spec: v1alpha1.ValuesSpec{
					ValuesFrom: []v1alpha1.ValueFromSource{
						{
							ConfigMapKeyRef: &v1alpha1.DataKeySelector{
								NamespacedName: v1alpha1.NamespacedName{
									Name:      testCMName,
									Namespace: testNamespace,
								},
								Key:      "values.yaml",
								Optional: false,
							},
						},
					},
					Set: nil,
				},
			},
			want: want{
				err: errors.Wrap(errors.Wrap(errors.Wrap(errBoom, fmt.Sprintf(errFailedToGetConfigMap, testNamespace)),
					errFailedToGetDataFromConfigMapRef),
					errFailedToGetValueFromSource),
			},
		},
		"FailedToUnmarshalValuesFromSource": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							s := corev1.ConfigMap{
								Data: map[string]string{
									"values.yaml": "invalid-yaml",
								},
							}
							*obj.(*corev1.ConfigMap) = s
							return nil
						}
						return errBoom
					},
				},
				spec: v1alpha1.ValuesSpec{
					ValuesFrom: []v1alpha1.ValueFromSource{
						{
							ConfigMapKeyRef: &v1alpha1.DataKeySelector{
								NamespacedName: v1alpha1.NamespacedName{
									Name:      testCMName,
									Namespace: testNamespace,
								},
								Key:      "values.yaml",
								Optional: false,
							},
						},
					},
					Set: nil,
				},
			},
			want: want{
				err: errors.Wrap(errors.New("error unmarshaling JSON: while decoding JSON: "+
					"json: cannot unmarshal string into Go value of type map[string]interface {}"),
					errFailedToUnmarshalDesiredValues),
			},
		},
		"InlineOverriddenWithSet": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: v1alpha1.ValuesSpec{
					Values: runtime.RawExtension{
						Raw: []byte(`
keyA: valA
keyB:
  subKeyA: subValA
`),
					},
					ValuesFrom: nil,
					Set: []v1alpha1.SetVal{
						{
							Name:  "keyA",
							Value: "valX",
						},
					},
				},
			},
			want: want{
				out: map[string]interface{}{
					"keyA": "valX",
					"keyB": map[string]interface{}{
						"subKeyA": "subValA",
					},
				},
				err: nil,
			},
		},
		"MissingValueForSet": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: v1alpha1.ValuesSpec{
					Set: []v1alpha1.SetVal{
						{
							Name: "keyA",
						},
					},
				},
			},
			want: want{
				err: errors.New(errMissingValueForSet),
			},
		},

		"InlineOverriddenWithSetFromSecret": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == testSecretName && key.Namespace == testNamespace {
							s := corev1.Secret{
								Data: map[string][]byte{
									"keyA": []byte("valY"),
								},
							}
							*obj.(*corev1.Secret) = s
							return nil
						}
						return errBoom
					},
				},
				spec: v1alpha1.ValuesSpec{
					Values: runtime.RawExtension{
						Raw: []byte(`
keyA: valA
keyB:
  subKeyA: subValA
`),
					},
					ValuesFrom: nil,
					Set: []v1alpha1.SetVal{
						{
							Name: "keyA",
							ValueFrom: &v1alpha1.ValueFromSource{
								SecretKeyRef: &v1alpha1.DataKeySelector{
									NamespacedName: v1alpha1.NamespacedName{
										Name:      testSecretName,
										Namespace: testNamespace,
									},
									Key:      "keyA",
									Optional: false,
								},
							},
						},
					},
				},
			},
			want: want{
				out: map[string]interface{}{
					"keyA": "valY",
					"keyB": map[string]interface{}{
						"subKeyA": "subValA",
					},
				},
				err: nil,
			},
		},
		"InlineOverriddenWithSetFromSecret_ErrGettingSecret": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == testSecretName && key.Namespace == testNamespace {
							return errBoom
						}
						return nil
					},
				},
				spec: v1alpha1.ValuesSpec{
					Values: runtime.RawExtension{
						Raw: []byte(`
keyA: valA
keyB:
  subKeyA: subValA
`),
					},
					ValuesFrom: nil,
					Set: []v1alpha1.SetVal{
						{
							Name: "keyA",
							ValueFrom: &v1alpha1.ValueFromSource{
								SecretKeyRef: &v1alpha1.DataKeySelector{
									NamespacedName: v1alpha1.NamespacedName{
										Name:      testSecretName,
										Namespace: testNamespace,
									},
									Key:      "keyA",
									Optional: false,
								},
							},
						},
					},
				},
			},
			want: want{
				err: errors.Wrap(errors.Wrap(errors.Wrap(errBoom, fmt.Sprintf(errFailedToGetSecret, testNamespace)),
					errFailedToGetDataFromSecretRef),
					errFailedToGetValueFromSource),
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, gotErr := composeValuesFromSpec(context.Background(), tc.args.kube, tc.args.spec)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("composeValuesFromSpec(...): -want error, +got error: %s", diff)
			}
			if diff := cmp.Diff(tc.want.out, got); diff != "" {
				t.Errorf("composeValuesFromSpec(...): -want result, +got result: %s", diff)
			}
		})
	}
}
