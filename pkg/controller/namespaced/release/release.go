/*
Copyright 2020 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package release

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ktype "sigs.k8s.io/kustomize/api/types"

	xpv1 "github.com/crossplane/crossplane-runtime/v2/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	"github.com/crossplane/crossplane-runtime/v2/pkg/event"
	"github.com/crossplane/crossplane-runtime/v2/pkg/feature"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/crossplane/crossplane-runtime/v2/pkg/statemetrics"

	kubeclient "github.com/crossplane-contrib/provider-kubernetes/pkg/kube/client"

	"github.com/crossplane-contrib/provider-helm/apis/namespaced/release/v1beta1"
	namespacedv1beta1 "github.com/crossplane-contrib/provider-helm/apis/namespaced/v1beta1"
	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

const (
	defaultWaitTimeout = 5 * time.Minute

	helmReleaseNameAnnotation      = "meta.helm.sh/release-name"
	helmReleaseNamespaceAnnotation = "meta.helm.sh/release-namespace"
	helmNamespaceLabel             = "app.kubernetes.io/managed-by"
	helmProviderName               = "provider-helm"
)

const (
	errNotRelease                 = "managed resource is not a Release custom resource"
	errProviderConfigNotSet       = "provider config is not set"
	errGetProviderConfig          = "cannot get provider config"
	errNewHelmClient              = "cannot create new Helm client"
	errFailedToGetLastRelease     = "failed to get last helm release"
	errLastReleaseIsNil           = "last helm release is nil"
	errFailedToCheckIfUpToDate    = "failed to check if release is up to date"
	errFailedToInstall            = "failed to install release"
	errFailedToUpgrade            = "failed to upgrade release"
	errFailedToUninstall          = "failed to uninstall release"
	errFailedToGetRepoCreds       = "failed to get user name and password from secret reference"
	errFailedToComposeValues      = "failed to compose values"
	errBuildKubeForProviderConfig = "cannot build kube client for provider config"
	errFailedToTrackUsage         = "cannot track provider config usage"
	errFailedToLoadPatches        = "failed to load patches"
	errFailedToUpdatePatchSha     = "failed to update patch sha"
	errFailedToSetName            = "failed to update chart spec with the name from URL"
	errFailedToSetVersion         = "failed to update chart spec with the latest version"
	errFailedToCreateNamespace    = "failed to create namespace for release"
)

// Setup adds a controller that reconciles Release managed resources.
func Setup(mgr ctrl.Manager, o controller.Options, timeout time.Duration) error {
	name := managed.ControllerName(v1beta1.ReleaseGroupKind)

	reconcilerOptions := []managed.ReconcilerOption{
		managed.WithExternalConnector(&connector{
			client:          mgr.GetClient(),
			logger:          o.Logger,
			usage:           resource.NewProviderConfigUsageTracker(mgr.GetClient(), &namespacedv1beta1.ProviderConfigUsage{}),
			clientBuilder:   kubeclient.NewIdentityAwareBuilder(mgr.GetClient()),
			newHelmClientFn: helmClient.NewClient,
		}),
		managed.WithPollInterval(o.PollInterval),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithTimeout(timeout),
		managed.WithMetricRecorder(o.MetricOptions.MRMetrics),
		managed.WithDeterministicExternalName(true),
	}

	if o.Features.Enabled(feature.EnableAlphaChangeLogs) {
		reconcilerOptions = append(reconcilerOptions, managed.WithChangeLogger(o.ChangeLogOptions.ChangeLogger))
	}

	if o.Features.Enabled(feature.EnableBetaManagementPolicies) {
		reconcilerOptions = append(reconcilerOptions, managed.WithManagementPolicies())
	}

	if err := mgr.Add(statemetrics.NewMRStateRecorder(
		mgr.GetClient(), o.Logger, o.MetricOptions.MRStateMetrics, &v1beta1.ReleaseList{}, o.MetricOptions.PollStateMetricInterval)); err != nil {
		return err
	}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1beta1.ReleaseGroupVersionKind),
		reconcilerOptions...,
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1beta1.Release{}).
		WithOptions(o.ForControllerRuntime()).
		Complete(r)
}

// SetupGated adds a controller that reconciles ProviderConfigs by accounting for
// their current usage.
func SetupGated(mgr ctrl.Manager, o controller.Options, timeout time.Duration) error {
	o.Gate.Register(func() {
		if err := Setup(mgr, o, timeout); err != nil {
			mgr.GetLogger().Error(err, "unable to setup reconciler", "gvk", v1beta1.ReleaseGroupVersionKind.String())
		}
	}, v1beta1.ReleaseGroupVersionKind)
	return nil
}

type connector struct {
	logger logging.Logger
	client client.Client
	usage  helmClient.ModernTracker

	clientBuilder   kubeclient.Builder
	newHelmClientFn func(log logging.Logger, config *rest.Config, helmArgs ...helmClient.ArgsApplier) (helmClient.Client, error)
}

func withRelease(cr *v1beta1.Release) helmClient.ArgsApplier {
	return func(config *helmClient.Args) {
		config.Namespace = cr.Namespace
		config.Wait = cr.Spec.ForProvider.Wait
		config.Timeout = waitTimeout(cr)
		config.SkipCRDs = cr.Spec.ForProvider.SkipCRDs
		config.InsecureSkipTLSVerify = cr.Spec.ForProvider.InsecureSkipTLSVerify
	}
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) { //nolint:gocyclo
	cr, ok := mg.(*v1beta1.Release)
	if !ok {
		return nil, errors.New(errNotRelease)
	}
	l := c.logger.WithValues("request", cr.Name)

	l.Debug("Connecting")

	pcSpec, err := helmClient.ResolveProviderConfig(ctx, c.client, nil, c.usage, mg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve provider config")
	}

	k, rc, err := c.clientBuilder.KubeForProviderConfig(ctx, *pcSpec)
	if err != nil {
		return nil, errors.Wrap(err, errBuildKubeForProviderConfig)
	}
	h, err := c.newHelmClientFn(c.logger, rc, withRelease(cr))
	if err != nil {
		return nil, errors.Wrap(err, errNewHelmClient)
	}

	return &helmExternal{
		logger:    l,
		localKube: c.client,
		kube:      k,
		helm:      h,
		patch:     newPatcher(),
	}, nil
}

type helmExternal struct {
	logger    logging.Logger
	localKube client.Client
	kube      client.Client
	helm      helmClient.Client
	patch     Patcher
}

func (e *helmExternal) Disconnect(ctx context.Context) error {
	return nil
}

func (e *helmExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1beta1.Release)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotRelease)
	}

	e.logger.Debug("Observing")

	rel, err := e.helm.GetLastRelease(meta.GetExternalName(cr))
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errFailedToGetLastRelease)
	}

	if rel == nil {
		return managed.ExternalObservation{}, errors.New(errLastReleaseIsNil)
	}

	cr.Status.AtProvider = generateObservation(rel)

	// Determining whether the release is up to date may involve reading values
	// from secrets, configmaps, etc. This will fail if said dependencies have
	// been deleted. We don't need to determine whether we're up to date in
	// order to delete the release, so if we know we're about to be deleted we
	// return early to avoid blocking unnecessarily on missing dependencies.
	if meta.WasDeleted(cr) {
		return managed.ExternalObservation{ResourceExists: true}, nil
	}

	s, err := isUpToDate(ctx, e.localKube, &cr.Spec, rel, cr.Status, cr.Namespace)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errFailedToCheckIfUpToDate)
	}
	cr.Status.Synced = s
	cd := managed.ConnectionDetails{}
	if cr.Status.AtProvider.State == release.StatusDeployed && s {
		cr.Status.Failed = 0

		cd, err = connectionDetails(ctx, e.kube, cr.Spec.ConnectionDetails, rel.Name, rel.Namespace)
		if err != nil {
			return managed.ExternalObservation{}, errors.Wrap(err, "cannot get connection details")
		}
		cr.Status.SetConditions(xpv1.Available())
	} else {
		cr.Status.SetConditions(xpv1.Unavailable())
	}

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  cr.Status.Synced && !(shouldRollBack(cr) && !rollBackLimitReached(cr)),
		ConnectionDetails: cd,
	}, nil
}

type deployAction func(release string, chart *chart.Chart, vals map[string]interface{}, patches []ktype.Patch) (*release.Release, error)

func (e *helmExternal) deploy(ctx context.Context, cr *v1beta1.Release, action deployAction) error {
	cv, err := composeValuesFromSpec(ctx, e.localKube, cr.Spec.ForProvider.ValuesSpec, cr.Namespace)
	if err != nil {
		return errors.Wrap(err, errFailedToComposeValues)
	}

	creds, err := repoCredsFromSecret(ctx, e.localKube, cr.Namespace, cr.Spec.ForProvider.Chart.PullSecretRef)
	if err != nil {
		return errors.Wrap(err, errFailedToGetRepoCreds)
	}

	p, err := e.patch.getFromSpec(ctx, e.localKube, cr.Spec.ForProvider.PatchesFrom, cr.Namespace)
	if err != nil {
		return errors.Wrap(err, errFailedToLoadPatches)
	}

	chart, err := e.helm.PullAndLoadChart(cr, creds)
	if err != nil {
		return err
	}
	if cr.Spec.ForProvider.Chart.Name == "" {
		cr.Spec.ForProvider.Chart.Name = chart.Metadata.Name
		if err := e.localKube.Update(ctx, cr); err != nil {
			return errors.Wrap(err, errFailedToSetName)
		}
	}
	if cr.Spec.ForProvider.Chart.Version == "" {
		cr.Spec.ForProvider.Chart.Version = chart.Metadata.Version
		if err := e.localKube.Update(ctx, cr); err != nil {
			return errors.Wrap(err, errFailedToSetVersion)
		}
	}

	rel, err := action(meta.GetExternalName(cr), chart, cv, p)

	if err != nil {
		return err
	}

	if rel == nil {
		return errors.New(errLastReleaseIsNil)
	}

	sha, err := e.patch.shaOf(p)
	if err != nil {
		return errors.Wrap(err, errFailedToUpdatePatchSha)
	}
	cr.Status.PatchesSha = sha
	cr.Status.AtProvider = generateObservation(rel)

	return nil
}

func (e *helmExternal) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1beta1.Release)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotRelease)
	}

	e.logger.Debug("Creating")

	return managed.ExternalCreation{}, errors.Wrap(e.deploy(ctx, cr, e.helm.Install), errFailedToInstall)
}

func (e *helmExternal) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1beta1.Release)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotRelease)
	}

	if shouldRollBack(cr) {
		e.logger.Debug("Last release failed")
		if !rollBackLimitReached(cr) {
			// Rollback
			e.logger.Debug("Will rollback/uninstall to retry")
			cr.Status.Failed++
			// If it's the first revision of a Release, rollback would fail since there is no previous revision.
			// We need to uninstall to retry.
			if cr.Status.AtProvider.Revision == 1 {
				e.logger.Debug("Uninstalling")
				return managed.ExternalUpdate{}, e.helm.Uninstall(meta.GetExternalName(cr))
			}
			e.logger.Debug("Rolling back to previous release version")
			return managed.ExternalUpdate{}, e.helm.Rollback(meta.GetExternalName(cr))
		}
		e.logger.Debug("Reached max rollback retries, will not retry")
		return managed.ExternalUpdate{}, nil
	}

	e.logger.Debug("Updating")
	return managed.ExternalUpdate{}, errors.Wrap(e.deploy(ctx, cr, e.helm.Upgrade), errFailedToUpgrade)
}

func (e *helmExternal) Delete(_ context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1beta1.Release)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotRelease)
	}

	e.logger.Debug("Deleting")

	return managed.ExternalDelete{}, errors.Wrap(e.helm.Uninstall(meta.GetExternalName(cr)), errFailedToUninstall)
}

func shouldRollBack(cr *v1beta1.Release) bool {
	return rollBackEnabled(cr) &&
		((cr.Status.Synced && cr.Status.AtProvider.State == release.StatusFailed) ||
			(cr.Status.AtProvider.State == release.StatusPendingInstall) ||
			(cr.Status.AtProvider.State == release.StatusPendingUpgrade))
}

func rollBackEnabled(cr *v1beta1.Release) bool {
	return cr.Spec.RollbackRetriesLimit != nil
}
func rollBackLimitReached(cr *v1beta1.Release) bool {
	return cr.Status.Failed >= *cr.Spec.RollbackRetriesLimit
}

func (e *helmExternal) createNamespace(ctx context.Context, name string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				helmNamespaceLabel: helmProviderName,
			},
		},
	}
	return e.kube.Create(ctx, ns)
}

func waitTimeout(cr *v1beta1.Release) time.Duration {
	if cr.Spec.ForProvider.WaitTimeout != nil {
		return cr.Spec.ForProvider.WaitTimeout.Duration
	}
	return defaultWaitTimeout
}
