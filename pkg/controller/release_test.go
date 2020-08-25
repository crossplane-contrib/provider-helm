package controller

import (
	"context"
	"fmt"
	"testing"

	runtimev1alpha1 "github.com/crossplane/crossplane-runtime/apis/core/v1alpha1"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	kubev1alpha1 "github.com/crossplane/crossplane/apis/kubernetes/v1alpha1"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/types"

	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

const (
	providerName            = "helm-test"
	providerSecretName      = "helm-test-secret"
	providerSecretNamespace = "helm-test-secret-namespace"

	providerSecretKey  = "credentials.json"
	providerSecretData = "somethingsecret"

	testReleaseName = "test-release"
)

type helmReleaseModifier func(release *v1alpha1.Release)

func helmRelase(rm ...helmReleaseModifier) *v1alpha1.Release {
	r := &v1alpha1.Release{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testReleaseName,
			Namespace: testNamespace,
		},
		Spec: v1alpha1.ReleaseSpec{
			ResourceSpec: runtimev1alpha1.ResourceSpec{
				ProviderReference: &corev1.ObjectReference{
					Name: providerName,
				},
			},
			ForProvider: v1alpha1.ReleaseParameters{
				Chart: v1alpha1.ChartSpec{
					Version: testVersion,
				},
			},
		},
		Status: v1alpha1.ReleaseStatus{},
	}

	for _, m := range rm {
		m(r)
	}

	return r
}

type MockGetLastReleaseFn func(release string) (*release.Release, error)
type MockInstallFn func(release string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch) (*release.Release, error)
type MockUpgradeFn func(release string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch) (*release.Release, error)
type MockRollBackFn func(release string) error
type MockUninstallFn func(release string) error
type MockPullAndLoadChartFn func(spec *v1alpha1.ChartSpec, creds *helmClient.RepoCreds) (*chart.Chart, error)

type MockHelmClient struct {
	MockGetLastRelease   MockGetLastReleaseFn
	MockInstall          MockInstallFn
	MockUpgrade          MockUpgradeFn
	MockRollBack         MockRollBackFn
	MockUninstall        MockUninstallFn
	MockPullAndLoadChart MockPullAndLoadChartFn
}

func (c *MockHelmClient) GetLastRelease(release string) (*release.Release, error) {
	return c.MockGetLastRelease(release)
}

func (c *MockHelmClient) Install(release string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch) (*release.Release, error) {
	return c.MockInstall(release, chart, vals, patches)
}

func (c *MockHelmClient) Upgrade(release string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch) (*release.Release, error) {
	return c.MockUpgrade(release, chart, vals, patches)
}

func (c *MockHelmClient) Rollback(release string) error {
	return c.MockRollBack(release)
}

func (c *MockHelmClient) Uninstall(release string) error {
	return c.MockUninstall(release)
}

func (c *MockHelmClient) PullAndLoadChart(spec *v1alpha1.ChartSpec, creds *helmClient.RepoCreds) (*chart.Chart, error) {
	if c.MockPullAndLoadChart != nil {
		return c.MockPullAndLoadChart(spec, creds)
	}
	return nil, nil
}

type notHelmRelease struct {
	resource.Managed
}

func Test_connector_Connect(t *testing.T) {
	provider := kubev1alpha1.Provider{
		ObjectMeta: metav1.ObjectMeta{Name: providerName},
		Spec: kubev1alpha1.ProviderSpec{
			Secret: runtimev1alpha1.SecretReference{
				Name:      providerSecretName,
				Namespace: providerSecretNamespace,
			},
		},
	}

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: providerSecretNamespace, Name: providerSecretName},
		Data:       map[string][]byte{providerSecretKey: []byte(providerSecretData)},
	}

	type args struct {
		client          client.Client
		newRestConfigFn func(creds map[string][]byte) (*rest.Config, error)
		newKubeClientFn func(config *rest.Config) (client.Client, error)
		newHelmClientFn func(log logging.Logger, config *rest.Config, namespace string) (helmClient.Client, error)
		mg              resource.Managed
	}
	type want struct {
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"NotReleaseResource": {
			args: args{
				mg: notHelmRelease{},
			},
			want: want{
				err: errors.New(errNotRelease),
			},
		},
		"FailedToGetProvider": {
			args: args{
				client: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == providerName {
							*obj.(*kubev1alpha1.Provider) = provider
							return errBoom
						}
						return nil
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: errors.Wrap(errBoom, errProviderNotRetrieved),
			},
		},
		"FailedToGetProviderSecret": {
			args: args{
				client: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == providerName {
							*obj.(*kubev1alpha1.Provider) = provider
							return nil
						}
						if key.Name == providerSecretName && key.Namespace == providerSecretNamespace {
							return errBoom
						}
						return errBoom
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: errors.Wrap(errors.Wrap(errBoom, fmt.Sprintf(errFailedToGetSecret, providerSecretNamespace)), errProviderSecretNotRetrieved),
			},
		},
		"FailedToCreateRestConfig": {
			args: args{
				client: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == providerName {
							*obj.(*kubev1alpha1.Provider) = provider
							return nil
						}
						if key.Name == providerSecretName && key.Namespace == providerSecretNamespace {
							*obj.(*corev1.Secret) = secret
							return nil
						}
						return errBoom
					},
				},
				newRestConfigFn: func(creds map[string][]byte) (config *rest.Config, err error) {
					return nil, errBoom
				},
				mg: helmRelase(),
			},
			want: want{
				err: errors.Wrap(errBoom, errFailedToCreateRestConfig),
			},
		},
		"FailedToCreateNewKubernetesClient": {
			args: args{
				client: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == providerName {
							*obj.(*kubev1alpha1.Provider) = provider
							return nil
						}
						if key.Name == providerSecretName && key.Namespace == providerSecretNamespace {
							*obj.(*corev1.Secret) = secret
							return nil
						}
						return errBoom
					},
				},
				newRestConfigFn: func(creds map[string][]byte) (config *rest.Config, err error) {
					return &rest.Config{}, nil
				},
				newKubeClientFn: func(config *rest.Config) (c client.Client, err error) {
					return nil, errBoom
				},
				mg: helmRelase(),
			},
			want: want{
				err: errors.Wrap(errBoom, errNewKubernetesClient),
			},
		},
		"Success": {
			args: args{
				client: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj runtime.Object) error {
						if key.Name == providerName {
							*obj.(*kubev1alpha1.Provider) = provider
							return nil
						}
						if key.Name == providerSecretName && key.Namespace == providerSecretNamespace {
							*obj.(*corev1.Secret) = secret
							return nil
						}
						return errBoom
					},
				},
				newRestConfigFn: func(creds map[string][]byte) (config *rest.Config, err error) {
					return &rest.Config{}, nil
				},
				newKubeClientFn: func(config *rest.Config) (c client.Client, err error) {
					return &test.MockClient{}, nil
				},
				newHelmClientFn: func(log logging.Logger, config *rest.Config, namespace string) (h helmClient.Client, err error) {
					return &MockHelmClient{}, nil
				},
				mg: helmRelase(),
			},
			want: want{
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := &connector{
				logger:          logging.NewNopLogger(),
				client:          tc.args.client,
				newRestConfigFn: tc.args.newRestConfigFn,
				newKubeClientFn: tc.args.newKubeClientFn,
				newHelmClientFn: tc.args.newHelmClientFn,
			}
			_, gotErr := c.Connect(context.Background(), tc.args.mg)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("#TODO(...): -want error, +got error: %s", diff)
			}
		})
	}
}

func Test_helmExternal_Observe(t *testing.T) {
	type args struct {
		localKube client.Client
		kube      client.Client
		helm      helmClient.Client
		mg        resource.Managed
	}
	type want struct {
		out managed.ExternalObservation
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"NotReleaseResource": {
			args: args{
				mg: notHelmRelease{},
			},
			want: want{
				err: errors.New(errNotRelease),
			},
		},
		"NoHelmReleaseExists": {
			args: args{
				localKube: nil,
				kube:      nil,
				helm: &MockHelmClient{
					MockGetLastRelease: func(r string) (hr *release.Release, err error) {
						return nil, driver.ErrReleaseNotFound
					},
				},
				mg: helmRelase(),
			},
			want: want{
				out: managed.ExternalObservation{ResourceExists: false},
				err: nil,
			},
		},
		"FailedToGetLastRelease": {
			args: args{
				localKube: nil,
				kube:      nil,
				helm: &MockHelmClient{
					MockGetLastRelease: func(r string) (hr *release.Release, err error) {
						return nil, errBoom
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: errors.Wrap(errBoom, errFailedToGetLastRelease),
			},
		},
		"ErrorLastReleaseIsNil": {
			args: args{
				localKube: nil,
				kube:      nil,
				helm: &MockHelmClient{
					MockGetLastRelease: func(r string) (hr *release.Release, err error) {
						return nil, nil
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: errors.New(errLastReleaseIsNil),
			},
		},
		"FailedToCheckIsUpToDate": {
			args: args{
				localKube: nil,
				kube:      nil,
				helm: &MockHelmClient{
					MockGetLastRelease: func(r string) (hr *release.Release, err error) {
						return &release.Release{}, nil
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: errors.Wrap(errors.New(errChartNilInObservedRelease), errFailedToCheckIfUpToDate),
			},
		},
		"UpdateDate": {
			args: args{
				localKube: nil,
				kube:      nil,
				helm: &MockHelmClient{
					MockGetLastRelease: func(r string) (hr *release.Release, err error) {
						return &release.Release{
							Name: r,
							Chart: &chart.Chart{
								Metadata: &chart.Metadata{
									Name:    testChart,
									Version: testVersion,
								},
							},
						}, nil
					},
				},
				mg: helmRelase(),
			},
			want: want{
				out: managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: true},
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &helmExternal{
				logger:    logging.NewNopLogger(),
				localKube: tc.args.localKube,
				kube:      tc.args.kube,
				helm:      tc.args.helm,
			}
			_, gotErr := e.Observe(context.Background(), tc.args.mg)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("e.Observe(...): -want error, +got error: %s", diff)
			}
		})
	}
}

func Test_helmExternal_Create(t *testing.T) {
	type args struct {
		localKube client.Client
		kube      client.Client
		helm      helmClient.Client
		mg        resource.Managed
		updateFn  func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error
	}
	type want struct {
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"NotReleaseResource": {
			args: args{
				mg: notHelmRelease{},
			},
			want: want{
				err: errors.New(errNotRelease),
			},
		},
		"InstalledFailed": {
			args: args{
				helm: &MockHelmClient{
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch) (hr *release.Release, err error) {
						return nil, errBoom
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: errors.Wrap(errBoom, errFailedToInstall),
			},
		},
		"InstalledButLastReleaseIsNil": {
			args: args{
				helm: &MockHelmClient{
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch) (hr *release.Release, err error) {
						return nil, nil
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: errors.Wrap(errors.New(errLastReleaseIsNil), errFailedToInstall),
			},
		},
		"Success": {
			args: args{
				helm: &MockHelmClient{
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch) (hr *release.Release, err error) {
						return &release.Release{}, nil
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: nil,
			},
		},
		"LatestVersion": {
			args: args{
				helm: &MockHelmClient{
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch) (hr *release.Release, err error) {
						return &release.Release{}, nil
					},
					MockPullAndLoadChart: func(spec *v1alpha1.ChartSpec, creds *helmClient.RepoCreds) (*chart.Chart, error) {
						return &chart.Chart{
							Metadata: &chart.Metadata{
								Version: testVersion,
							},
						}, nil
					},
				},
				mg: helmRelase(func(r *v1alpha1.Release) {
					r.Spec.ForProvider.Chart.Version = ""
				}),
				updateFn: func(ctx context.Context, obj runtime.Object, opts ...client.UpdateOption) error {
					cr := obj.(*v1alpha1.Release)
					if diff := cmp.Diff(cr.Spec.ForProvider.Chart.Version, testVersion); diff != "" {
						t.Fatalf("updateFn(...): -want version, +got version: %s", diff)
					}
					return nil
				},
			},
			want: want{
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &helmExternal{
				logger:    logging.NewNopLogger(),
				localKube: tc.args.localKube,
				kube:      tc.args.kube,
				helm:      tc.args.helm,
				patch:     newPatcher(),
			}
			if tc.args.updateFn != nil {
				e.localKube = &test.MockClient{
					MockUpdate: tc.args.updateFn,
				}
			}
			_, gotErr := e.Create(context.Background(), tc.args.mg)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("e.Create(...): -want error, +got error: %s", diff)
			}
		})
	}
}

func Test_helmExternal_Update(t *testing.T) {
	type args struct {
		localKube client.Client
		kube      client.Client
		helm      helmClient.Client
		mg        resource.Managed
	}
	type want struct {
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"NotReleaseResource": {
			args: args{
				mg: notHelmRelease{},
			},
			want: want{
				err: errors.New(errNotRelease),
			},
		},
		"RetryUninstallFails": {
			args: args{
				helm: &MockHelmClient{
					MockUninstall: func(release string) error {
						return errBoom
					},
				},
				mg: helmRelase(func(r *v1alpha1.Release) {
					l := int32(3)
					r.Spec.RollbackLimit = &l
					r.Status.Synced = true
					r.Status.AtProvider.Revision = 1
					r.Status.AtProvider.State = release.StatusFailed
				}),
			},
			want: want{
				err: errBoom,
			},
		},
		"RetryRollbackFails": {
			args: args{
				helm: &MockHelmClient{
					MockRollBack: func(release string) error {
						return errBoom
					},
				},
				mg: helmRelase(func(r *v1alpha1.Release) {
					l := int32(3)
					r.Spec.RollbackLimit = &l
					r.Status.Synced = true
					r.Status.AtProvider.Revision = 3
					r.Status.AtProvider.State = release.StatusFailed
				}),
			},
			want: want{
				err: errBoom,
			},
		},
		"RetryRollbackSuccess": {
			args: args{
				helm: &MockHelmClient{
					MockRollBack: func(release string) error {
						return nil
					},
				},
				mg: helmRelase(func(r *v1alpha1.Release) {
					l := int32(3)
					r.Spec.RollbackLimit = &l
					r.Status.Synced = true
					r.Status.AtProvider.Revision = 3
					r.Status.AtProvider.State = release.StatusFailed
				}),
			},
			want: want{
				err: nil,
			},
		},
		"MaxRetry": {
			args: args{
				helm: &MockHelmClient{},
				mg: helmRelase(func(r *v1alpha1.Release) {
					l := int32(3)
					r.Spec.RollbackLimit = &l
					r.Status.Failed = 3
					r.Status.Synced = true
					r.Status.AtProvider.Revision = 3
					r.Status.AtProvider.State = release.StatusFailed
				}),
			},
			want: want{
				err: nil,
			},
		},
		"UpgradeFailed": {
			args: args{
				helm: &MockHelmClient{
					MockUpgrade: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch) (hr *release.Release, err error) {
						return nil, errBoom
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: errors.Wrap(errBoom, errFailedToUpgrade),
			},
		},
		"UpgradedButLastReleaseIsNil": {
			args: args{
				helm: &MockHelmClient{
					MockUpgrade: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch) (hr *release.Release, err error) {
						return nil, nil
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: errors.Wrap(errors.New(errLastReleaseIsNil), errFailedToUpgrade),
			},
		},
		"Success": {
			args: args{
				helm: &MockHelmClient{
					MockUpgrade: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch) (hr *release.Release, err error) {
						return &release.Release{}, nil
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &helmExternal{
				logger:    logging.NewNopLogger(),
				localKube: tc.args.localKube,
				kube:      tc.args.kube,
				helm:      tc.args.helm,
				patch:     newPatcher(),
			}
			_, gotErr := e.Update(context.Background(), tc.args.mg)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("e.Update(...): -want error, +got error: %s", diff)
			}
		})
	}
}

func Test_helmExternal_Delete(t *testing.T) {
	type args struct {
		localKube client.Client
		kube      client.Client
		helm      helmClient.Client
		mg        resource.Managed
	}
	type want struct {
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"NotReleaseResource": {
			args: args{
				mg: notHelmRelease{},
			},
			want: want{
				err: errors.New(errNotRelease),
			},
		},
		"FailedToUninstall": {
			args: args{
				helm: &MockHelmClient{
					MockUninstall: func(release string) error {
						return errBoom
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: errors.Wrap(errBoom, errFailedToUninstall),
			},
		},
		"Success": {
			args: args{
				helm: &MockHelmClient{
					MockUninstall: func(release string) error {
						return nil
					},
				},
				mg: helmRelase(),
			},
			want: want{
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &helmExternal{
				logger:    logging.NewNopLogger(),
				localKube: tc.args.localKube,
				kube:      tc.args.kube,
				helm:      tc.args.helm,
			}
			gotErr := e.Delete(context.Background(), tc.args.mg)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("e.Delete(...): -want error, +got error: %s", diff)
			}
		})
	}
}
