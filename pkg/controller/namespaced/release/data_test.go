package release

import (
	"context"
	"fmt"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-helm/apis/namespaced/release/v1beta1"
)

const (
	testSecretName = "testcreds"
	testNamespace  = "testns"
	testCMName     = "testcm"
)

var (
	testSecretData = map[string][]byte{"test": []byte("ok")}
	testCMData     = map[string]string{"test": "ok"}
)

func Test_getSecretData(t *testing.T) {
	type args struct {
		kube client.Client
		nn   types.NamespacedName
	}
	type want struct {
		out map[string][]byte
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"FailedToGetSecret": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testSecretName && key.Namespace == testNamespace {
							return kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testSecretName)
						}
						return errBoom
					},
				},
				nn: types.NamespacedName{
					Namespace: testNamespace,
					Name:      testSecretName,
				},
			},
			want: want{
				out: nil,
				err: errors.Wrap(kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testSecretName),
					fmt.Sprintf(errFailedToGetSecret, testNamespace)),
			},
		},
		"SecretDataIsNil": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testSecretName && key.Namespace == testNamespace {
							s := corev1.Secret{
								Data: nil,
							}
							*obj.(*corev1.Secret) = s
							return nil
						}
						return errBoom
					},
				},
				nn: types.NamespacedName{
					Namespace: testNamespace,
					Name:      testSecretName,
				},
			},
			want: want{
				out: nil,
				err: errors.New(errSecretDataIsNil),
			},
		},
		"Success": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testSecretName && key.Namespace == testNamespace {
							s := corev1.Secret{
								Data: testSecretData,
							}
							*obj.(*corev1.Secret) = s
							return nil
						}
						return errBoom
					},
				},
				nn: types.NamespacedName{
					Namespace: testNamespace,
					Name:      testSecretName,
				},
			},
			want: want{
				out: testSecretData,
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, gotErr := getSecretData(context.Background(), tc.args.kube, tc.args.nn)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("getSecretData(...): -want error, +got error: %s", diff)
			}
			if diff := cmp.Diff(tc.want.out, got); diff != "" {
				t.Errorf("getSecretData(...): -want result, +got result: %s", diff)
			}
		})
	}
}

func Test_getConfigMapData(t *testing.T) {
	type args struct {
		kube client.Client
		nn   types.NamespacedName
	}
	type want struct {
		out map[string]string
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"FailedToGetCM": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							return kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testCMName)
						}
						return errBoom
					},
				},
				nn: types.NamespacedName{
					Namespace: testNamespace,
					Name:      testCMName,
				},
			},
			want: want{
				out: nil,
				err: errors.Wrap(kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testCMName),
					fmt.Sprintf(errFailedToGetConfigMap, testNamespace)),
			},
		},
		"CMDataIsNil": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							c := corev1.ConfigMap{
								Data: nil,
							}
							*obj.(*corev1.ConfigMap) = c
							return nil
						}
						return errBoom
					},
				},
				nn: types.NamespacedName{
					Namespace: testNamespace,
					Name:      testCMName,
				},
			},
			want: want{
				out: nil,
				err: errors.New(errConfigMapDataIsNil),
			},
		},
		"Success": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							s := corev1.ConfigMap{
								Data: testCMData,
							}
							*obj.(*corev1.ConfigMap) = s
							return nil
						}
						return errBoom
					},
				},
				nn: types.NamespacedName{
					Namespace: testNamespace,
					Name:      testCMName,
				},
			},
			want: want{
				out: testCMData,
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, gotErr := getConfigMapData(context.Background(), tc.args.kube, tc.args.nn)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("getConfigMapData(...): -want error, +got error: %s", diff)
			}
			if diff := cmp.Diff(tc.want.out, got); diff != "" {
				t.Errorf("getConfigMapData(...): -want result, +got result: %s", diff)
			}
		})
	}
}

func Test_getDataValueFromSource(t *testing.T) {
	type args struct {
		kube       client.Client
		source     v1beta1.ValueFromSource
		defaultKey string
	}
	type want struct {
		out string
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"SourceNotSetErr": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{},
			},
			want: want{
				out: "",
				err: errors.New(errSourceNotSetForValueFrom),
			},
		},
		"ErrWhileGettingSecret": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testSecretName && key.Namespace == testNamespace {
							return errBoom
						}
						return nil
					},
				},
				source: v1beta1.ValueFromSource{
					SecretKeyRef: &v1beta1.DataKeySelector{
						Name:     testSecretName,
						Key:      "test",
						Optional: false,
					},
				},
			},
			want: want{
				out: "",
				err: errors.Wrap(errors.Wrap(errBoom, fmt.Sprintf(errFailedToGetSecret, testNamespace)), errFailedToGetDataFromSecretRef),
			},
		},
		"ErrWhileGettingCM": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							return errBoom
						}
						return nil
					},
				},
				source: v1beta1.ValueFromSource{
					ConfigMapKeyRef: &v1beta1.DataKeySelector{
						Name:     testCMName,
						Key:      "test",
						Optional: false,
					},
				},
			},
			want: want{
				out: "",
				err: errors.Wrap(errors.Wrap(errBoom, fmt.Sprintf(errFailedToGetConfigMap, testNamespace)), errFailedToGetDataFromConfigMapRef),
			},
		},
		"SecretNotFoundButOptional": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testSecretName && key.Namespace == testNamespace {
							return kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testSecretName)
						}
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{
					SecretKeyRef: &v1beta1.DataKeySelector{
						Name:     testSecretName,
						Key:      "test",
						Optional: true,
					},
				},
			},
			want: want{
				out: "",
				err: nil,
			},
		},
		"SecretNotFoundNotOptional": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testSecretName && key.Namespace == testNamespace {
							return kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testSecretName)
						}
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{
					SecretKeyRef: &v1beta1.DataKeySelector{
						Name:     testSecretName,
						Key:      "test",
						Optional: false,
					},
				},
			},
			want: want{
				out: "",
				err: errors.Wrap(errors.Wrap(kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testSecretName), fmt.Sprintf(errFailedToGetSecret, testNamespace)), errFailedToGetDataFromSecretRef),
			},
		},
		"CMNotFoundButOptional": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							return kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testCMName)
						}
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{
					ConfigMapKeyRef: &v1beta1.DataKeySelector{
						Name:     testCMName,
						Key:      "test",
						Optional: true,
					},
				},
			},
			want: want{
				out: "",
				err: nil,
			},
		},
		"CMNotFoundNotOptional": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							return kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testCMName)
						}
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{
					ConfigMapKeyRef: &v1beta1.DataKeySelector{
						Name:     testCMName,
						Key:      "test",
						Optional: false,
					},
				},
			},
			want: want{
				out: "",
				err: errors.Wrap(errors.Wrap(kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testCMName), fmt.Sprintf(errFailedToGetConfigMap, testNamespace)), errFailedToGetDataFromConfigMapRef),
			},
		},
		"SecretKeyMissingButOptional": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testSecretName && key.Namespace == testNamespace {
							s := corev1.Secret{
								Data: testSecretData,
							}
							*obj.(*corev1.Secret) = s
							return nil
						}
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{
					SecretKeyRef: &v1beta1.DataKeySelector{
						Name:     testSecretName,
						Key:      "nonexistingkey",
						Optional: true,
					},
				},
			},
			want: want{
				out: "",
				err: nil,
			},
		},
		"SecretKeyMissingNotOptional": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testSecretName && key.Namespace == testNamespace {
							s := corev1.Secret{
								Data: testSecretData,
							}
							*obj.(*corev1.Secret) = s
							return nil
						}
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{
					SecretKeyRef: &v1beta1.DataKeySelector{
						Name:     testSecretName,
						Key:      "nonexistingkey",
						Optional: false,
					},
				},
			},
			want: want{
				out: "",
				err: fmt.Errorf(errMissingKeyForValuesFrom, "nonexistingkey"),
			},
		},
		"CMKeyMissingButOptional": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							s := corev1.ConfigMap{
								Data: testCMData,
							}
							*obj.(*corev1.ConfigMap) = s
							return nil
						}
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{
					ConfigMapKeyRef: &v1beta1.DataKeySelector{
						Name:     testCMName,
						Key:      "nonexistingkey",
						Optional: true,
					},
				},
			},
			want: want{
				out: "",
				err: nil,
			},
		},
		"CMKeyMissingNotOptional": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							s := corev1.ConfigMap{
								Data: testCMData,
							}
							*obj.(*corev1.ConfigMap) = s
							return nil
						}
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{
					ConfigMapKeyRef: &v1beta1.DataKeySelector{
						Name:     testCMName,
						Key:      "nonexistingkey",
						Optional: false,
					},
				},
			},
			want: want{
				out: "",
				err: fmt.Errorf(errMissingKeyForValuesFrom, "nonexistingkey"),
			},
		},
		"SuccessSecretWithKey": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testSecretName && key.Namespace == testNamespace {
							s := corev1.Secret{
								Data: testSecretData,
							}
							*obj.(*corev1.Secret) = s
							return nil
						}
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{
					SecretKeyRef: &v1beta1.DataKeySelector{
						Name:     testSecretName,
						Key:      "test",
						Optional: false,
					},
				},
			},
			want: want{
				out: "ok",
				err: nil,
			},
		},
		"SuccessSecretWithDefaultKey": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testSecretName && key.Namespace == testNamespace {
							s := corev1.Secret{
								Data: testSecretData,
							}
							*obj.(*corev1.Secret) = s
							return nil
						}
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{
					SecretKeyRef: &v1beta1.DataKeySelector{
						Name:     testSecretName,
						Optional: false,
					},
				},
				defaultKey: "test",
			},
			want: want{
				out: "ok",
				err: nil,
			},
		},
		"SuccessCMWithKey": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							s := corev1.ConfigMap{
								Data: testCMData,
							}
							*obj.(*corev1.ConfigMap) = s
							return nil
						}
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{
					ConfigMapKeyRef: &v1beta1.DataKeySelector{
						Name:     testCMName,
						Key:      "test",
						Optional: false,
					},
				},
			},
			want: want{
				out: "ok",
				err: nil,
			},
		},
		"SuccessCMWithDefaultKey": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == testCMName && key.Namespace == testNamespace {
							s := corev1.ConfigMap{
								Data: testCMData,
							}
							*obj.(*corev1.ConfigMap) = s
							return nil
						}
						return errBoom
					},
				},
				source: v1beta1.ValueFromSource{
					ConfigMapKeyRef: &v1beta1.DataKeySelector{
						Name:     testCMName,
						Optional: false,
					},
				},
				defaultKey: "test",
			},
			want: want{
				out: "ok",
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, gotErr := getDataValueFromSource(context.Background(), tc.args.kube, tc.args.source, tc.args.defaultKey, testNamespace)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("getDataValueFromSource(...): -want error, +got error: %s", diff)
			}
			if diff := cmp.Diff(tc.want.out, got); diff != "" {
				t.Errorf("getDataValueFromSource(...): -want result, +got result: %s", diff)
			}
		})
	}

}
