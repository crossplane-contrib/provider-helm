package controller

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

	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
	"github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

const (
	testRepo                = "testrepo"
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

func Test_chartDefFromSpec(t *testing.T) {
	type args struct {
		kube client.Client
		spec v1alpha1.ChartSpec
	}
	type want struct {
		out helm.ChartDefinition
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
				spec: v1alpha1.ChartSpec{
					Repository: testRepo,
					Name:       testChart,
					Version:    testVersion,
				},
			},
			want: want{
				out: helm.ChartDefinition{
					Repository: testRepo,
					Name:       testChart,
					Version:    testVersion,
				},
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
				spec: v1alpha1.ChartSpec{
					Repository: testRepo,
					Name:       testChart,
					Version:    testVersion,
					PullSecretRef: runtimev1alpha1.SecretReference{
						Name: testPullSecretName,
					},
				},
			},
			want: want{
				out: helm.ChartDefinition{},
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
				spec: v1alpha1.ChartSpec{
					Repository: testRepo,
					Name:       testChart,
					Version:    testVersion,
					PullSecretRef: runtimev1alpha1.SecretReference{
						Name:      testPullSecretName,
						Namespace: testPullSecretNamespace,
					},
				},
			},
			want: want{
				out: helm.ChartDefinition{},
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
				spec: v1alpha1.ChartSpec{
					Repository: testRepo,
					Name:       testChart,
					Version:    testVersion,
					PullSecretRef: runtimev1alpha1.SecretReference{
						Name:      testPullSecretName,
						Namespace: testPullSecretNamespace,
					},
				},
			},
			want: want{
				out: helm.ChartDefinition{},
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
				spec: v1alpha1.ChartSpec{
					Repository: testRepo,
					Name:       testChart,
					Version:    testVersion,
					PullSecretRef: runtimev1alpha1.SecretReference{
						Name:      testPullSecretName,
						Namespace: testPullSecretNamespace,
					},
				},
			},
			want: want{
				out: helm.ChartDefinition{},
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
				spec: v1alpha1.ChartSpec{
					Repository: testRepo,
					Name:       testChart,
					Version:    testVersion,
					PullSecretRef: runtimev1alpha1.SecretReference{
						Name:      testPullSecretName,
						Namespace: testPullSecretNamespace,
					},
				},
			},
			want: want{
				out: helm.ChartDefinition{
					Repository: testRepo,
					Name:       testChart,
					Version:    testVersion,
					RepoUser:   testUser,
					RepoPass:   testPass,
				},
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, gotErr := chartDefFromSpec(context.Background(), tc.args.kube, tc.args.spec)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("chartDefFromSpec(...): -want error, +got error: %s", diff)
			}
			if diff := cmp.Diff(tc.want.out, got); diff != "" {
				t.Errorf("chartDefFromSpec(...): -want result, +got result: %s", diff)
			}
		})
	}
}
