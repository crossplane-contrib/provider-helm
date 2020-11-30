package release

import (
	"context"
	"fmt"
	"testing"

	runtimev1alpha1 "github.com/crossplane/crossplane-runtime/apis/core/v1alpha1"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

const (
	testChart               = "testchart"
	testVersion             = "v1"
	testPullSecretName      = "testcreds"
	testPullSecretNamespace = "testns"
	testUser                = "testuser"
	testPass                = "testpass"
)

var (
	errBoom = errors.New("boom")
)

func Test_userInfoFromSecret(t *testing.T) {
	type args struct {
		kube      client.Client
		secretRef runtimev1alpha1.SecretReference
	}
	type want struct {
		out *helm.RepoCreds
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"NoPullSecret": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
			},
			want: want{
				out: &helm.RepoCreds{},
				err: nil,
			},
		},
		"PullSecretMissingNamespace": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == testPullSecretName && key.Namespace == testPullSecretNamespace {
							pullSecret := corev1.Secret{
								Data: map[string][]byte{
									keyRepoUsername: []byte(testUser),
									keyRepoPassword: []byte(testPass),
								},
							}
							*obj.(*corev1.Secret) = pullSecret
							return nil
						}
						return errBoom
					},
				},
				secretRef: runtimev1alpha1.SecretReference{
					Name: testPullSecretName,
				},
			},
			want: want{
				err: errors.New(errChartPullSecretMissingNamespace),
			},
		},
		"PullSecretMissing": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == testPullSecretName && key.Namespace == testPullSecretNamespace {
							return kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testPullSecretName)
						}
						return errBoom
					},
				},
				secretRef: runtimev1alpha1.SecretReference{
					Name:      testPullSecretName,
					Namespace: testPullSecretNamespace,
				},
			},
			want: want{
				err: errors.Wrap(
					errors.Wrap(kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testPullSecretName),
						fmt.Sprintf(errFailedToGetSecret, testPullSecretNamespace)), errFailedToGetRepoPullSecret),
			},
		},
		"PullSecretMissingUsername": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == testPullSecretName && key.Namespace == testPullSecretNamespace {
							pullSecret := corev1.Secret{
								Data: map[string][]byte{
									keyRepoPassword: []byte(testPass),
								},
							}
							*obj.(*corev1.Secret) = pullSecret
							return nil
						}
						return errBoom
					},
				},
				secretRef: runtimev1alpha1.SecretReference{
					Name:      testPullSecretName,
					Namespace: testPullSecretNamespace,
				},
			},
			want: want{
				out: nil,
				err: errors.New(errChartPullSecretMissingUsername),
			},
		},
		"PullSecretMissingPassword": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == testPullSecretName && key.Namespace == testPullSecretNamespace {
							pullSecret := corev1.Secret{
								Data: map[string][]byte{
									keyRepoUsername: []byte(testUser),
								},
							}
							*obj.(*corev1.Secret) = pullSecret
							return nil
						}
						return errBoom
					},
				},
				secretRef: runtimev1alpha1.SecretReference{
					Name:      testPullSecretName,
					Namespace: testPullSecretNamespace,
				},
			},
			want: want{
				err: errors.New(errChartPullSecretMissingPassword),
			},
		},
		"ProperPullSecret": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == testPullSecretName && key.Namespace == testPullSecretNamespace {
							pullSecret := corev1.Secret{
								Data: map[string][]byte{
									keyRepoUsername: []byte(testUser),
									keyRepoPassword: []byte(testPass),
								},
							}
							*obj.(*corev1.Secret) = pullSecret
							return nil
						}
						return errBoom
					},
				},
				secretRef: runtimev1alpha1.SecretReference{
					Name:      testPullSecretName,
					Namespace: testPullSecretNamespace,
				},
			},
			want: want{
				out: &helm.RepoCreds{
					Username: testUser,
					Password: testPass,
				},
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, gotErr := repoCredsFromSecret(context.Background(), tc.args.kube, tc.args.secretRef)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("repoCredsFromSecret(...): -want error, +got error: %s", diff)
			}
			if diff := cmp.Diff(tc.want.out, got); diff != "" {
				t.Errorf("chartDefFromSpec(...): -want result, +got result: %s", diff)
			}
		})
	}
}
