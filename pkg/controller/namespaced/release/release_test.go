package release

import (
	"context"
	"testing"
	"time"

	kubeclient "github.com/crossplane-contrib/provider-kubernetes/pkg/kube/client"
	kconfig "github.com/crossplane-contrib/provider-kubernetes/pkg/kube/config"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/google/go-cmp/cmp"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	helmcommon "helm.sh/helm/v4/pkg/release/common"
	release "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/kustomize/api/types"

	"github.com/crossplane/crossplane-runtime/v2/pkg/controller"
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
				ProviderConfigReference: &xpv2.ProviderConfigReference{
					Name: providerName,
					Kind: "ClusterProviderConfig",
				},
			},
			ForProvider: v1beta1.ReleaseParameters{
				SkipCreateNamespace: true,
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
type MockInstallFn func(release string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (*release.Release, error)
type MockUpgradeFn func(release string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (*release.Release, error)
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

func (c *MockHelmClient) Install(release string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (*release.Release, error) {
	return c.MockInstall(release, chart, vals, patches, opts)
}

func (c *MockHelmClient) Upgrade(release string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (*release.Release, error) {
	return c.MockUpgrade(release, chart, vals, patches, opts)
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
				Source: xpv2.CredentialsSourceNone,
			},
			Identity: &kconfig.Identity{
				Type: kconfig.IdentityTypeGoogleApplicationCredentials,
				ProviderCredentials: kconfig.ProviderCredentials{
					Source: xpv2.CredentialsSourceNone,
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
	providerConfigGoogleInjectedIdentity.Spec.Identity.Source = xpv2.CredentialsSourceInjectedIdentity

	providerConfigAzure := helmv1beta1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: providerName},
		Spec: kconfig.ProviderConfigSpec{
			Credentials: kconfig.ProviderCredentials{
				Source: xpv2.CredentialsSourceNone,
			},
			Identity: &kconfig.Identity{
				Type: kconfig.IdentityTypeAzureServicePrincipalCredentials,
				ProviderCredentials: kconfig.ProviderCredentials{
					Source: xpv2.CredentialsSourceNone,
				},
			},
		},
	}

	providerConfigAzureInjectedIdentity := *providerConfigAzure.DeepCopy()
	providerConfigAzureInjectedIdentity.Spec.Identity.Source = xpv2.CredentialsSourceInjectedIdentity

	providerConfigUnknownIdentitySource := *providerConfigAzure.DeepCopy()
	providerConfigUnknownIdentitySource.Spec.Identity.Type = "foo"

	type args struct {
		client            client.Client
		clientForProvider client.Client
		newHelmClientFn   func(log logging.Logger, config *rest.Config, helmArgs ...helmClient.ArgsApplier) (helmClient.Client, error)
		usage             resource.ModernTracker
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
				usage: resource.ModernTrackerFn(func(ctx context.Context, mg resource.ModernManaged) error { return errBoom }),
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
				usage: resource.ModernTrackerFn(func(ctx context.Context, mg resource.ModernManaged) error { return nil }),
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
				usage: resource.ModernTrackerFn(func(ctx context.Context, mg resource.ModernManaged) error { return nil }),
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
				usage: resource.ModernTrackerFn(func(ctx context.Context, mg resource.ModernManaged) error { return nil }),
				mg:    helmRelease(),
			},
			want: want{
				err: nil,
			},
		},
		// A deleted Release with a CABundle pointing at a Secret that no
		// longer exists must still connect successfully: Connect runs before
		// every operation including uninstall, which never needs CABundle,
		// so resolving it here would otherwise turn a routine
		// ConfigMap/Secret cleanup ordering into a permanently stuck
		// finalizer.
		"CABundleResolutionSkippedWhenDeleted": {
			args: args{
				client: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						switch t := obj.(type) {
						case *helmv1beta1.ProviderConfig:
							*t = providerConfig
							return nil
						case *helmv1beta1.ClusterProviderConfig:
							*t = clusterProviderConfig
							return nil
						case *corev1.Secret:
							// The CABundle's source Secret is gone - if Connect
							// tried to resolve it, this is what it would hit.
							return errBoom
						default:
							return errBoom
						}
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
				usage: resource.ModernTrackerFn(func(ctx context.Context, mg resource.ModernManaged) error { return nil }),
				mg: helmRelease(
					func(release *v1beta1.Release) {
						release.Spec.ForProvider.CABundle = &v1beta1.ValueFromSource{
							SecretKeyRef: &v1beta1.DataKeySelector{
								Name: "missing-ca-secret",
							},
						}
						now := metav1.Now()
						release.SetDeletionTimestamp(&now)
					},
				),
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
		// digest is status.atProvider.digest after the observation.
		digest string
		err    error
	}
	const pinnedDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
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
								Status: helmcommon.StatusFailed,
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
		"LegacyDigestPinIsDrift": {
			// A release deployed before label support with no digest recorded
			// in status: pinning one is drift, and the pin is not copied into
			// status as if it had been deployed.
			args: args{
				localKube: nil,
				kube:      nil,
				helm: &MockHelmClient{
					MockGetLastRelease: func(r string) (hr *release.Release, err error) {
						return &release.Release{
							Name: r,
							Info: &release.Info{
								Status: helmcommon.StatusDeployed,
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
					r.Spec.ForProvider.Chart.Digest = pinnedDigest
				}),
			},
			want: want{
				out: managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: false, ConnectionDetails: managed.ConnectionDetails{}},
				err: nil,
			},
		},
		"LegacyDigestPinNotRecordedWithoutUpdatePolicy": {
			// Without the Update policy nothing deploys the pin, so status
			// keeps reporting that no digest is known to be deployed.
			args: args{
				localKube: nil,
				kube:      nil,
				helm: &MockHelmClient{
					MockGetLastRelease: func(r string) (hr *release.Release, err error) {
						return &release.Release{
							Name: r,
							Info: &release.Info{
								Status: helmcommon.StatusDeployed,
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
					r.Spec.ManagementPolicies = []xpv2.ManagementAction{xpv2.ManagementActionObserve}
					r.Spec.ForProvider.Chart.Digest = pinnedDigest
				}),
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

			if cr, ok := tc.args.mg.(*v1beta1.Release); ok {
				if diff := cmp.Diff(tc.want.digest, cr.Status.AtProvider.Digest); diff != "" {
					t.Errorf("e.Observe(...): -want status digest, +got status digest: %s", diff)
				}
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
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (hr *release.Release, err error) {
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
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (hr *release.Release, err error) {
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
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (hr *release.Release, err error) {
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
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (hr *release.Release, err error) {
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
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (hr *release.Release, err error) {
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
		"ReleaseNamespaceSpecified": {
			args: args{
				helm: &MockHelmClient{
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (*release.Release, error) {
						return &release.Release{}, nil
					},
				},
				kube: &test.MockClient{
					MockCreate: test.NewMockCreateFn(nil),
				},
				mg: helmRelease(func(r *v1beta1.Release) {
					r.Spec.ForProvider.Namespace = "remote-namespace"
				}),
			},
			want: want{
				err: nil,
			},
		},
		"CreateNamespaceFailed": {
			args: args{
				kube: &test.MockClient{
					MockCreate: test.NewMockCreateFn(errBoom),
				},
				mg: helmRelease(func(r *v1beta1.Release) {
					r.Spec.ForProvider.Namespace = "myNamespace"
					r.Spec.ForProvider.SkipCreateNamespace = false
				}),
			},
			want: want{
				err: errors.Wrap(errBoom, errFailedToCreateNamespace),
			},
		},
		"CreateNamespaceAlreadyExists": {
			args: args{
				kube: &test.MockClient{
					MockCreate: test.NewMockCreateFn(kerrors.NewAlreadyExists(corev1.Resource("namespaces"), "myNamespace")),
				},
				helm: &MockHelmClient{
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (*release.Release, error) {
						return &release.Release{}, nil
					},
				},
				mg: helmRelease(func(r *v1beta1.Release) {
					r.Spec.ForProvider.Namespace = "myNamespace"
					r.Spec.ForProvider.SkipCreateNamespace = false
				}),
			},
			want: want{
				err: nil,
			},
		},
		"CreateNamespaceSuccess": {
			args: args{
				kube: &test.MockClient{
					MockCreate: test.NewMockCreateFn(nil),
				},
				helm: &MockHelmClient{
					MockInstall: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (*release.Release, error) {
						return &release.Release{}, nil
					},
				},
				mg: helmRelease(func(r *v1beta1.Release) {
					r.Spec.ForProvider.Namespace = "myNamespace"
					r.Spec.ForProvider.SkipCreateNamespace = false
				}),
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
		err            error
		ownershipTaken bool
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
					r.Status.AtProvider.State = helmcommon.StatusFailed
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
					r.Status.AtProvider.State = helmcommon.StatusFailed
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
					r.Status.AtProvider.State = helmcommon.StatusFailed
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
					r.Status.AtProvider.State = helmcommon.StatusFailed
				}),
			},
			want: want{
				err: nil,
			},
		},
		"UpgradeReAdoptsWhileTakeOwnershipSet": {
			// Ownership was already taken, recorded on the release label and
			// rehydrated by Observe. takeOwnership is still set, so the upgrade
			// exercises adoption again: that is what takes back a resource
			// another actor re-stamped since the last deploy.
			args: args{
				helm: &MockHelmClient{
					MockUpgrade: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (hr *release.Release, err error) {
						want := helmClient.DeployOptions{
							TakeOwnership: true,
							Labels: map[string]string{
								helmClient.LabelDigestHash:     "",
								helmClient.LabelURLHash:        "",
								helmClient.LabelOwnershipTaken: "true",
							},
						}
						if diff := cmp.Diff(want, opts); diff != "" {
							t.Errorf("Upgrade(...) options: -want, +got: %s", diff)
						}
						return &release.Release{}, nil
					},
				},
				mg: helmRelease(func(r *v1beta1.Release) {
					r.Spec.ForProvider.TakeOwnership = true
					r.Status.AtProvider.OwnershipTaken = true
				}),
			},
			want: want{
				ownershipTaken: true,
			},
		},
		"LateInitKeepsOwnershipLabel": {
			// Late-initialization's Update decodes the persisted status back
			// into the object, dropping the ownership Observe rehydrated from
			// the release label, so the deploy options must be decided before
			// it or the sticky label would be dropped from this deploy.
			args: args{
				localKube: &test.MockClient{
					MockUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						obj.(*v1beta1.Release).Status = v1beta1.ReleaseStatus{}
						return nil
					},
				},
				helm: &MockHelmClient{
					MockPullAndLoadChart: func(mg resource.Managed, creds *helmClient.RepoCreds) (*chart.Chart, error) {
						return &chart.Chart{Metadata: &chart.Metadata{Name: testChart, Version: testVersion}}, nil
					},
					MockUpgrade: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (hr *release.Release, err error) {
						want := helmClient.DeployOptions{
							TakeOwnership: true,
							Labels: map[string]string{
								helmClient.LabelDigestHash:     "",
								helmClient.LabelURLHash:        "",
								helmClient.LabelOwnershipTaken: "true",
							},
						}
						if diff := cmp.Diff(want, opts); diff != "" {
							t.Errorf("Upgrade(...) options: -want, +got: %s", diff)
						}
						return &release.Release{}, nil
					},
				},
				mg: helmRelease(func(r *v1beta1.Release) {
					r.Spec.ForProvider.Chart.Version = ""
					r.Spec.ForProvider.TakeOwnership = true
					r.Status.AtProvider.OwnershipTaken = true
				}),
			},
			want: want{
				ownershipTaken: true,
			},
		},
		"OwnershipKeptWhenTakeOwnershipUnset": {
			// Adoption happened on an earlier deploy: unsetting takeOwnership
			// neither forgets it nor skips the label, which back-fills it on
			// releases adopted before label support.
			args: args{
				helm: &MockHelmClient{
					MockUpgrade: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (hr *release.Release, err error) {
						want := helmClient.DeployOptions{
							Labels: map[string]string{
								helmClient.LabelDigestHash:     "",
								helmClient.LabelURLHash:        "",
								helmClient.LabelOwnershipTaken: "true",
							},
						}
						if diff := cmp.Diff(want, opts); diff != "" {
							t.Errorf("Upgrade(...) options: -want, +got: %s", diff)
						}
						return &release.Release{}, nil
					},
				},
				mg: helmRelease(func(r *v1beta1.Release) {
					r.Status.AtProvider.OwnershipTaken = true
				}),
			},
			want: want{
				ownershipTaken: true,
			},
		},
		"UpgradeFailed": {
			args: args{
				helm: &MockHelmClient{
					MockUpgrade: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (hr *release.Release, err error) {
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
					MockUpgrade: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (hr *release.Release, err error) {
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
					MockUpgrade: func(r string, chart *chart.Chart, vals map[string]interface{}, patches []types.Patch, opts helmClient.DeployOptions) (hr *release.Release, err error) {
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
			if cr, ok := tc.args.mg.(*v1beta1.Release); ok {
				if diff := cmp.Diff(tc.want.ownershipTaken, cr.Status.AtProvider.OwnershipTaken); diff != "" {
					t.Errorf("e.Update(...): -want status.atProvider.ownershipTaken, +got: %s", diff)
				}
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

func Test_deployOptions(t *testing.T) {
	const (
		chartURL = "oci://registry.example.com/charts/mychart:1.2.3"
		digest   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	type args struct {
		cr *v1beta1.Release
	}
	type want struct {
		opts helmClient.DeployOptions
	}
	cases := map[string]struct {
		args
		want
	}{
		"RepositoryModeWithoutOwnership": {
			// The digest and URL labels are written empty rather than omitted,
			// so that this release stays distinguishable from one deployed
			// before label support and a later pin or URL is detected as drift.
			args: args{
				cr: helmRelease(),
			},
			want: want{
				opts: helmClient.DeployOptions{
					Labels: map[string]string{
						helmClient.LabelDigestHash: "",
						helmClient.LabelURLHash:    "",
					},
				},
			},
		},
		"URLAndDigestWithOwnership": {
			args: args{
				cr: helmRelease(func(r *v1beta1.Release) {
					r.Spec.ForProvider.Chart.URL = chartURL
					r.Spec.ForProvider.Chart.Digest = digest
					r.Spec.ForProvider.TakeOwnership = true
				}),
			},
			want: want{
				opts: helmClient.DeployOptions{
					TakeOwnership: true,
					Labels: map[string]string{
						helmClient.LabelDigestHash:     helmClient.EncodeDigestLabel(digest),
						helmClient.LabelURLHash:        helmClient.EncodeURLLabel(chartURL),
						helmClient.LabelOwnershipTaken: "true",
					},
				},
			},
		},
		"OwnershipRequestedAgainWhenAlreadyTaken": {
			// Ownership was already taken on a prior deploy. takeOwnership is a
			// standing claim, so adoption is still exercised: a resource
			// re-stamped by another actor since that deploy has to be taken
			// back. Observe is what decides a deploy is needed at all.
			args: args{
				cr: helmRelease(func(r *v1beta1.Release) {
					r.Spec.ForProvider.TakeOwnership = true
					r.Status.AtProvider.OwnershipTaken = true
				}),
			},
			want: want{
				opts: helmClient.DeployOptions{
					TakeOwnership: true,
					Labels: map[string]string{
						helmClient.LabelDigestHash:     "",
						helmClient.LabelURLHash:        "",
						helmClient.LabelOwnershipTaken: "true",
					},
				},
			},
		},
		"OwnershipLabelBackfilledFromStatus": {
			// A release adopted before label support has only the persisted
			// status as a record: the label is written even with takeOwnership
			// unset, so the record survives the status.
			args: args{
				cr: helmRelease(func(r *v1beta1.Release) {
					r.Status.AtProvider.OwnershipTaken = true
				}),
			},
			want: want{
				opts: helmClient.DeployOptions{
					Labels: map[string]string{
						helmClient.LabelDigestHash:     "",
						helmClient.LabelURLHash:        "",
						helmClient.LabelOwnershipTaken: "true",
					},
				},
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want.opts, deployOptions(tc.args.cr)); diff != "" {
				t.Errorf("deployOptions(...): -want, +got: %s", diff)
			}
		})
	}
}

func TestSetupRequiresClientBuilder(t *testing.T) {
	cases := map[string]struct {
		setup func(ctrl.Manager, controller.Options, time.Duration, kubeclient.Builder) error
	}{
		"Setup":      {setup: Setup},
		"SetupGated": {setup: SetupGated},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.setup(nil, controller.Options{}, time.Minute, nil)
			if diff := cmp.Diff(errors.New(errNilClientBuilder), err, test.EquateErrors()); diff != "" {
				t.Errorf("%s(...): -want error, +got error:\n%s", name, diff)
			}
		})
	}
}

// mapperClient is a MockClient that serves a RESTMapper, which MockClient
// itself returns nil for. OwnershipDrifted needs one to tell a namespaced
// kind from a cluster-scoped one.
type mapperClient struct {
	client.Client
}

func (c *mapperClient) RESTMapper() apimeta.RESTMapper {
	gv := schema.GroupVersion{Group: "", Version: "v1"}
	m := apimeta.NewDefaultRESTMapper([]schema.GroupVersion{gv})
	m.Add(gv.WithKind("ConfigMap"), apimeta.RESTScopeNamespace)
	m.Add(gv.WithKind("Namespace"), apimeta.RESTScopeRoot)
	return m
}

func Test_helmExternal_ObserveOwnershipDrift(t *testing.T) {
	const manifest = "---\n# Source: chart/templates/cm.yaml\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"

	// deployedRelease is an otherwise up-to-date release that renders one
	// ConfigMap, so that nothing but ownership can make it drift.
	deployedRelease := func(_ string) (*release.Release, error) {
		return &release.Release{
			Name:      testReleaseName,
			Namespace: testNamespace,
			Info:      &release.Info{},
			Chart: &chart.Chart{
				Metadata: &chart.Metadata{Name: testChart, Version: testVersion},
			},
			Config:   map[string]interface{}{},
			Manifest: manifest,
		}, nil
	}

	// liveConfigMap serves the rendered resource with the given ownership
	// metadata, and records whether it was asked for at all.
	liveConfigMap := func(labels, annotations map[string]string, got *bool) test.MockGetFn {
		return func(_ context.Context, key client.ObjectKey, obj client.Object) error {
			*got = true
			u, ok := obj.(*metav1.PartialObjectMetadata)
			if !ok {
				return errors.New("not partial metadata")
			}
			u.SetName(key.Name)
			u.SetNamespace(key.Namespace)
			u.SetLabels(labels)
			u.SetAnnotations(annotations)
			return nil
		}
	}

	owned := map[string]string{helmClient.OwnerLabelManagedBy: helmClient.OwnerManagedByHelm}
	ownedBy := func(relName string) map[string]string {
		return map[string]string{
			helmClient.OwnerAnnotationReleaseName:      relName,
			helmClient.OwnerAnnotationReleaseNamespace: testNamespace,
		}
	}

	type want struct {
		upToDate bool
		// checked is whether the live resource was looked up at all.
		checked bool
		err     error
	}

	cases := map[string]struct {
		takeOwnership bool
		// foreignStamps has the live resource carry ownership metadata that
		// names another actor.
		foreignStamps bool
		// specDrift makes isUpToDate report the spec out of date, the case in
		// which the ownership check is deliberately skipped.
		specDrift bool
		want
	}{
		"DriftForcesADeployWhileTakeOwnershipSet": {
			// The ConfigMap lost Helm's managed-by label to another actor.
			// Nothing about the chart, values or digest changed, so this is the
			// only thing that can bring the provider back to re-stamp it.
			takeOwnership: true,
			foreignStamps: true,
			want:          want{upToDate: false, checked: true},
		},
		"NoDriftIsNotADeploy": {
			// Already owned by this release: left alone, so a steady state
			// with takeOwnership set does not upgrade on every reconcile.
			takeOwnership: true,
			want:          want{upToDate: true, checked: true},
		},
		"NotCheckedWithoutTakeOwnership": {
			// Opt-in: a Release that never asked to adopt pays no lookups,
			// and foreign ownership metadata is not its concern.
			takeOwnership: false,
			want:          want{upToDate: true, checked: false},
		},
		"CheckSkippedWhenSpecAlreadyOutOfDate": {
			// Spec drift already owes a deploy, and that deploy adopts
			// whatever the chart renders regardless of what the check would
			// have found, so the lookups cannot change this observation.
			takeOwnership: true,
			foreignStamps: true,
			specDrift:     true,
			want:          want{upToDate: false, checked: false},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			checked := false
			annotations := ownedBy(testReleaseName)
			if tc.foreignStamps {
				annotations = ownedBy("another-release")
			}

			e := &helmExternal{
				logger: logging.NewNopLogger(),
				kube:   &mapperClient{Client: &test.MockClient{MockGet: liveConfigMap(owned, annotations, &checked)}},
				helm: &MockHelmClient{MockGetLastRelease: func(name string) (*release.Release, error) {
					rel, err := deployedRelease(name)
					if err == nil && tc.specDrift {
						rel.Labels = map[string]string{helmClient.LabelURLHash: "drifted"}
					}
					return rel, err
				}},
			}

			cr := helmRelease(func(r *v1beta1.Release) {
				r.Spec.ForProvider.TakeOwnership = tc.takeOwnership
			})

			got, err := e.Observe(context.Background(), cr)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Fatalf("e.Observe(...): -want error, +got error: %s", diff)
			}
			// Spec drift and ownership drift both report not up to date; the
			// checked flag is what tells them apart in this table.
			if diff := cmp.Diff(tc.want.upToDate, got.ResourceUpToDate); diff != "" {
				t.Errorf("e.Observe(...) ResourceUpToDate: -want, +got: %s", diff)
			}
			if diff := cmp.Diff(tc.want.checked, checked); diff != "" {
				t.Errorf("e.Observe(...) looked up the live resource: -want, +got: %s", diff)
			}
			// Ownership drift describes the resources, not the release: it
			// must never show up as the spec being out of sync.
			if cr.Status.Synced != !tc.specDrift {
				t.Errorf("e.Observe(...) status.synced: want %t, got %t", !tc.specDrift, cr.Status.Synced)
			}
		})
	}
}

func Test_helmExternal_ObserveOwnershipCheckFails(t *testing.T) {
	errBoom := errors.New("boom")

	e := &helmExternal{
		logger: logging.NewNopLogger(),
		kube: &mapperClient{Client: &test.MockClient{
			MockGet: func(_ context.Context, _ client.ObjectKey, _ client.Object) error { return errBoom },
		}},
		helm: &MockHelmClient{
			MockGetLastRelease: func(_ string) (*release.Release, error) {
				return &release.Release{
					Name:      testReleaseName,
					Namespace: testNamespace,
					Info:      &release.Info{},
					Chart:     &chart.Chart{Metadata: &chart.Metadata{Name: testChart, Version: testVersion}},
					Config:    map[string]interface{}{},
					Manifest:  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n",
				}, nil
			},
		},
	}

	cr := helmRelease(func(r *v1beta1.Release) { r.Spec.ForProvider.TakeOwnership = true })

	_, err := e.Observe(context.Background(), cr)
	want := errors.Wrap(errors.Wrap(errBoom, `cannot get ConfigMap "cm"`), errFailedToCheckOwnership)
	if diff := cmp.Diff(want, err, test.EquateErrors()); diff != "" {
		t.Errorf("e.Observe(...): -want error, +got error: %s", diff)
	}
}
