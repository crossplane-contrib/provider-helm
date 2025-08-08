package release

import (
	"context"
	"testing"

	kubeclient "github.com/crossplane-contrib/provider-kubernetes/pkg/kube/client"
	kconfig "github.com/crossplane-contrib/provider-kubernetes/pkg/kube/config"
	"github.com/crossplane/crossplane-runtime/v2/apis/common"
	xpv1 "github.com/crossplane/crossplane-runtime/v2/apis/common/v1"
	xpv2 "github.com/crossplane/crossplane-runtime/v2/apis/common/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/types"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"

	clusterapis "github.com/crossplane-contrib/provider-helm/apis/cluster"
	namespacedapis "github.com/crossplane-contrib/provider-helm/apis/namespaced"
	"github.com/crossplane-contrib/provider-helm/apis/namespaced/release/v1beta1"
	helmv1beta1 "github.com/crossplane-contrib/provider-helm/apis/namespaced/v1beta1"
	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

const (
	providerName    = "helm-test"
	testReleaseName = "test-release"
)

type helmReleaseModifier func(release *v1beta1.Release)

func helmRelease(rm ...helmReleaseModifier) *v1beta1.Release {
	r := &v1beta1.Release{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testReleaseName,
			Namespace: testNamespace,
		},
		Spec: v1beta1.ReleaseSpec{
			ManagedResourceSpec: xpv2.ManagedResourceSpec{
				ProviderConfigReference: &common.ProviderConfigReference{
					Name: providerName,
					Kind: "ClusterProviderConfig",
				},
			},
			ForProvider: v1beta1.ReleaseParameters{
				Chart: v1beta1.ChartSpec{
					Name:    testChart,
					Version: testVersion,
				},
			},
		},
		Status: v1beta1.ReleaseStatus{},
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
type MockPullAndLoadChartFn func(mg resource.Managed, creds *helmClient.RepoCreds) (*chart.Chart, error)

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

func (c *MockHelmClient) PullAndLoadChart(mg resource.Managed, creds *helmClient.RepoCreds) (*chart.Chart, error) {
	if c.MockPullAndLoadChart != nil {
		return c.MockPullAndLoadChart(mg, creds)
	}
	return nil, nil
}

type notHelmRelease struct {
	resource.Managed
}

func Test_connector_Connect(t *testing.T) {
	providerConfig := helmv1beta1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: providerName},
		Spec: kconfig.ProviderConfigSpec{
			Credentials: kconfig.ProviderCredentials{
				Source: xpv1.CredentialsSourceNone,
			},
			Identity: &kconfig.Identity{
				Type: kconfig.IdentityTypeGoogleApplicationCredentials,
				ProviderCredentials: kconfig.ProviderCredentials{
					Source: xpv1.CredentialsSourceNone,
				},
			},
		},
	}

	clusterProviderConfig := helmv1beta1.ClusterProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: providerName},
	}

	providerConfigUsage := helmv1beta1.ProviderConfigUsage{
		ObjectMeta: metav1.ObjectMeta{Name: providerName},
	}

	providerConfigGoogleInjectedIdentity := *providerConfig.DeepCopy()
	providerConfigGoogleInjectedIdentity.Spec.Identity.Source = xpv1.CredentialsSourceInjectedIdentity

	providerConfigAzure := helmv1beta1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: providerName},
		Spec: kconfig.ProviderConfigSpec{
			Credentials: kconfig.ProviderCredentials{
				Source: xpv1.CredentialsSourceNone,
			},
			Identity: &kconfig.Identity{
				Type: kconfig.IdentityTypeAzureServicePrincipalCredentials,
				ProviderCredentials: kconfig.ProviderCredentials{
					Source: xpv1.CredentialsSourceNone,
				},
			},
		},
	}

	providerConfigAzureInjectedIdentity := *providerConfigAzure.DeepCopy()
	providerConfigAzureInjectedIdentity.Spec.Identity.Source = xpv1.CredentialsSourceInjectedIdentity

	providerConfigUnknownIdentitySource := *providerConfigAzure.DeepCopy()
	providerConfigUnknownIdentitySource.Spec.Identity.Type = "foo"

	type args struct {
		client            client.Client
		clientForProvider client.Client
		newHelmClientFn   func(log logging.Logger, config *rest.Config, helmArgs ...helmClient.ArgsApplier) (helmClient.Client, error)
		usage             helmClient.ModernTracker
		mg                resource.Managed
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
		"FailedToTrackUsage": {
			args: args{
				client: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						switch o := obj.(type) {
						case *helmv1beta1.ProviderConfig:
							*o = providerConfig
						case *helmv1beta1.ClusterProviderConfig:
							*o = clusterProviderConfig
						case *helmv1beta1.ProviderConfigUsage:
							*o = providerConfigUsage
						default:
							return errBoom
						}
						return nil
					},
					MockScheme: func() *runtime.Scheme {
						s := runtime.NewScheme()
						if err := clusterapis.AddToScheme(s); err != nil {
							t.Fatal(err)
						}
						if err := namespacedapis.AddToScheme(s); err != nil {
							t.Fatal(err)
						}
						return s
					},
				},
				usage: helmClient.ModernTrackerFn(func(ctx context.Context, mg resource.ModernManaged) error { return errBoom }),
				mg:    helmRelease(),
			},
			want: want{
				err: errors.Wrap(errors.Wrap(errBoom, errFailedToTrackUsage), "failed to resolve provider config"),
			},
		},
		"FailedToGetProvider": {
			args: args{
				client: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if key.Name == providerName {
							*obj.(*helmv1beta1.ClusterProviderConfig) = clusterProviderConfig
							return errBoom
						}
						return nil
					},
					MockScheme: func() *runtime.Scheme {
						s := runtime.NewScheme()
						if err := clusterapis.AddToScheme(s); err != nil {
							t.Fatal(err)
						}
						if err := namespacedapis.AddToScheme(s); err != nil {
							t.Fatal(err)
						}
						return s
					},
				},
				usage: helmClient.ModernTrackerFn(func(ctx context.Context, mg resource.ModernManaged) error { return nil }),
				mg:    helmRelease(),
			},
			want: want{
				err: errors.Wrap(errors.Wrap(errBoom, errGetProviderConfig), "failed to resolve provider config"),
			},
		},
		"FailedToCreateNewHelmClient": {
			args: args{
				client: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						switch o := obj.(type) {
						case *helmv1beta1.ProviderConfig:
							*o = providerConfig
						case *helmv1beta1.ClusterProviderConfig:
							*o = clusterProviderConfig
						default:
							return errBoom
						}
						return nil
					},
					MockStatusUpdate: func(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						return nil
					},
					MockScheme: func() *runtime.Scheme {
						s := runtime.NewScheme()
						if err := clusterapis.AddToScheme(s); err != nil {
							t.Fatal(err)
						}
						if err := namespacedapis.AddToScheme(s); err != nil {
							t.Fatal(err)
						}
						return s
					},
				},
				clientForProvider: &test.MockClient{},
				newHelmClientFn: func(log logging.Logger, restConfig *rest.Config, helmArgs ...helmClient.ArgsApplier) (helmClient.Client, error) {
					return nil, errBoom
				},
				usage: helmClient.ModernTrackerFn(func(ctx context.Context, mg resource.ModernManaged) error { return nil }),
				mg:    helmRelease(),
			},
			want: want{
				err: errors.Wrap(errBoom, errNewHelmClient),
			},
		},
		"Success": {
			args: args{
				client: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						switch t := obj.(type) {
						case *helmv1beta1.ProviderConfig:
							*t = providerConfig
						case *helmv1beta1.ClusterProviderConfig:
							*t = clusterProviderConfig
						default:
							return errBoom
						}
						return nil
					},
					MockStatusUpdate: func(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						return nil
					},
					MockScheme: func() *runtime.Scheme {
						s := runtime.NewScheme()
						if err := clusterapis.AddToScheme(s); err != nil {
							t.Fatal(err)
						}
						if err := namespacedapis.AddToScheme(s); err != nil {
							t.Fatal(err)
						}
						return s
					},
				},
				clientForProvider: &test.MockClient{},
				newHelmClientFn: func(log logging.Logger, restConfig *rest.Config, helmArgs ...helmClient.ArgsApplier) (h helmClient.Client, err error) {
					return &MockHelmClient{}, nil
				},
				usage: helmClient.ModernTrackerFn(func(ctx context.Context, mg resource.ModernManaged) error { return nil }),
				mg:    helmRelease(),
			},
			want: want{
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := &connector{
				logger: logging.NewNopLogger(),
				client: tc.args.client,
				usage:  tc.usage,
				clientBuilder: kubeclient.BuilderFn(func(ctx context.Context, pc kconfig.ProviderConfigSpec) (client.Client, *rest.Config, error) {
					return tc.args.clientForProvider, nil, nil
				}),
				newHelmClientFn: tc.args.newHelmClientFn,
			}
			_, gotErr := c.Connect(context.Background(), tc.args.mg)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("Connect(...): -want error, +got error: %s", diff)
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
				mg: helmRelease(),
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
				mg: helmRelease(),
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
				mg: helmRelease(),
			},
			want: want{
				err: errors.New(errLastReleaseIsNil),
			},
		},
		"ReleaseIsBeingDeleted": {
			args: args{
				localKube: nil,
				kube:      nil,
				helm: &MockHelmClient{
					MockGetLastRelease: func(r string) (hr *release.Release, err error) {
						return &release.Release{}, nil
					},
				},
				mg: helmRelease(
					func(release *v1beta1.Release) {
						now := metav1.Now()
						release.SetDeletionTimestamp(&now)
					},
				),
			},
			want: want{
				out: managed.ExternalObservation{ResourceExists: true},
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
				mg: helmRelease(),
			},
			want: want{
				err: errors.Wrap(errors.New(errReleaseInfoNilInObservedRelease), errFailedToCheckIfUpToDate),
			},
		},
		"Synced_ButShouldRollback": {
			args: args{
				localKube: nil,
				kube:      nil,
				helm: &MockHelmClient{
					MockGetLastRelease: func(r string) (hr *release.Release, err error) {
						return &release.Release{
							Name: r,
							Info: &release.Info{
								Status: release.StatusFailed,
							},
							Chart: &chart.Chart{
								Metadata: &chart.Metadata{
									Name:    testChart,
									Version: testVersion,
								},
							},
							Config: map[string]interface{}{},
						}, nil
					},
				},
				mg: helmRelease(func(r *v1beta1.Release) {
					rl := int32(3)
					r.Spec.RollbackRetriesLimit = &rl
					r.Status.Failed = 0
				}),
			},
			want: want{
				out: managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: false, ConnectionDetails: managed.ConnectionDetails{}},
				err: nil,
			},
		},
		"UpToDate": {
			args: args{
				localKube: nil,
				kube:      nil,
				helm: &MockHelmClient{
					MockGetLastRelease: func(r string) (hr *release.Release, err error) {
						return &release.Release{
							Name: r,
							Info: &release.Info{},
							Chart: &chart.Chart{
								Metadata: &chart.Metadata{
									Name:    testChart,
									Version: testVersion,
								},
							},
							Config: map[string]interface{}{},
						}, nil
					},
				},
				mg: helmRelease(),
			},
			want: want{
				out: managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: true, ConnectionDetails: managed.ConnectionDetails{}},
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
			got, gotErr := e.Observe(context.Background(), tc.args.mg)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("e.Observe(...): -want error, +got error: %s", diff)
			}

			if diff := cmp.Diff(tc.want.out, got); diff != "" {
				t.Fatalf("e.Observe(...): -want out, +got out: %s", diff)
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
		updateFn  func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error
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
				kube: &test.MockClient{
					MockCreate: test.NewMockCreateFn(nil),
				},
				mg: helmRelease(),
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
				kube: &test.MockClient{
					MockCreate: test.NewMockCreateFn(nil),
				},
				mg: helmRelease(),
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
				kube: &test.MockClient{
					MockCreate: test.NewMockCreateFn(nil),
				},
				mg: helmRelease(),
			},
			want: want{
				err: nil,
			},
		},
		"SuccessNamespaceExists": {
			args: args{
				helm: &MockHelmClient{
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch) (hr *release.Release, err error) {
						return &release.Release{}, nil
					},
				},
				kube: &test.MockClient{
					MockCreate: test.NewMockCreateFn(kerrors.NewAlreadyExists(corev1.Resource("some"), "some")),
				},
				mg: helmRelease(),
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
					MockPullAndLoadChart: func(mg resource.Managed, creds *helmClient.RepoCreds) (*chart.Chart, error) {
						return &chart.Chart{
							Metadata: &chart.Metadata{
								Version: testVersion,
							},
						}, nil
					},
				},
				kube: &test.MockClient{
					MockCreate: test.NewMockCreateFn(nil),
				},
				mg: helmRelease(func(r *v1beta1.Release) {
					r.Spec.ForProvider.Chart.Version = ""
				}),
				updateFn: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
					cr := obj.(*v1beta1.Release)
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
				mg: helmRelease(func(r *v1beta1.Release) {
					l := int32(3)
					r.Spec.RollbackRetriesLimit = &l
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
				mg: helmRelease(func(r *v1beta1.Release) {
					l := int32(3)
					r.Spec.RollbackRetriesLimit = &l
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
				mg: helmRelease(func(r *v1beta1.Release) {
					l := int32(3)
					r.Spec.RollbackRetriesLimit = &l
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
				mg: helmRelease(func(r *v1beta1.Release) {
					l := int32(3)
					r.Spec.RollbackRetriesLimit = &l
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
				mg: helmRelease(),
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
				mg: helmRelease(),
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
				mg: helmRelease(),
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
				mg: helmRelease(),
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
				mg: helmRelease(),
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
			_, gotErr := e.Delete(context.Background(), tc.args.mg)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("e.Delete(...): -want error, +got error: %s", diff)
			}
		})
	}
}
