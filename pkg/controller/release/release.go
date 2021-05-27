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
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ktype "sigs.k8s.io/kustomize/api/types"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"github.com/crossplane-contrib/provider-helm/apis/release/v1beta1"
	helmv1beta1 "github.com/crossplane-contrib/provider-helm/apis/v1beta1"
	"github.com/crossplane-contrib/provider-helm/pkg/clients"
	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

const (
	maxConcurrency = 10

	resyncPeriod     = 10 * time.Minute
	reconcileTimeout = 10 * time.Minute

	helmReleaseNameAnnotation      = "meta.helm.sh/release-name"
	helmReleaseNamespaceAnnotation = "meta.helm.sh/release-namespace"
	helmNamespaceLabel             = "app.kubernetes.io/managed-by"
	helmProviderName               = "provider-helm"
)

const (
	errNotRelease                        = "managed resource is not a Release custom resource"
	errProviderConfigNotSet              = "provider config is not set"
	errProviderNotRetrieved              = "provider could not be retrieved"
	errCredSecretNotSet                  = "provider credentials secret is not set"
	errNewKubernetesClient               = "cannot create new Kubernetes client"
	errProviderSecretNotRetrieved        = "secret referred in provider could not be retrieved"
	errProviderSecretValueForKeyNotFound = "value for key \"%s\" not found in provider credentials secret"
	errFailedToGetLastRelease            = "failed to get last helm release"
	errLastReleaseIsNil                  = "last helm release is nil"
	errFailedToCheckIfUpToDate           = "failed to check if release is up to date"
	errFailedToInstall                   = "failed to install release"
	errFailedToUpgrade                   = "failed to upgrade release"
	errFailedToUninstall                 = "failed to uninstall release"
	errFailedToGetRepoCreds              = "failed to get user name and password from secret reference"
	errFailedToComposeValues             = "failed to compose values"
	errFailedToCreateRestConfig          = "cannot create new rest config using provider secret"
	errFailedToTrackUsage                = "cannot track provider config usage"
	errFailedToLoadPatches               = "failed to load patches"
	errFailedToUpdatePatchSha            = "failed to update patch sha"
	errFailedToSetName                   = "failed to update chart spec with the name from URL"
	errFailedToSetVersion                = "failed to update chart spec with the latest version"
	errFailedToCreateNamespace           = "failed to create namespace for release"

	errFmtUnsupportedCredSource = "unsupported credentials source %q"
)

// Setup adds a controller that reconciles Release managed resources.
func Setup(mgr ctrl.Manager, l logging.Logger) error {
	name := managed.ControllerName(v1beta1.ReleaseGroupKind)
	logger := l.WithValues("controller", name)

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1beta1.ReleaseGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			logger:          logger,
			client:          mgr.GetClient(),
			usage:           resource.NewProviderConfigUsageTracker(mgr.GetClient(), &helmv1beta1.ProviderConfigUsage{}),
			newRestConfigFn: clients.NewRestConfig,
			newKubeClientFn: clients.NewKubeClient,
			newHelmClientFn: helmClient.NewClient,
		}),
		managed.WithLogger(logger),
		managed.WithTimeout(reconcileTimeout),
		managed.WithPollInterval(resyncPeriod),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1beta1.Release{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrency}).
		Complete(r)
}

type connector struct {
	logger          logging.Logger
	client          client.Client
	usage           resource.Tracker
	newRestConfigFn func(kubeconfig []byte) (*rest.Config, error)
	newKubeClientFn func(config *rest.Config) (client.Client, error)
	newHelmClientFn func(log logging.Logger, config *rest.Config, namespace string, wait bool) (helmClient.Client, error)
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1beta1.Release)
	if !ok {
		return nil, errors.New(errNotRelease)
	}
	l := c.logger.WithValues("request", cr.Name)

	l.Debug("Connecting")

	p := &helmv1beta1.ProviderConfig{}

	if cr.GetProviderConfigReference() == nil {
		return nil, errors.New(errProviderConfigNotSet)
	}

	if err := c.usage.Track(ctx, cr); err != nil {
		return nil, errors.Wrap(err, errFailedToTrackUsage)
	}

	n := types.NamespacedName{Name: cr.GetProviderConfigReference().Name}
	if err := c.client.Get(ctx, n, p); err != nil {
		return nil, errors.Wrap(err, errProviderNotRetrieved)
	}

	var rc *rest.Config
	var err error

	s := p.Spec.Credentials.Source
	switch s { //nolint:exhaustive
	case xpv1.CredentialsSourceInjectedIdentity:
		rc, err = rest.InClusterConfig()
		if err != nil {
			return nil, errors.Wrap(err, errFailedToCreateRestConfig)
		}
	case xpv1.CredentialsSourceSecret:
		ref := p.Spec.Credentials.SecretRef
		if ref == nil {
			return nil, errors.New(errCredSecretNotSet)
		}

		key := types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}
		d, err := getSecretData(ctx, c.client, key)
		if err != nil {
			return nil, errors.Wrap(err, errProviderSecretNotRetrieved)
		}
		kc, f := d[ref.Key]
		if !f {
			return nil, errors.Errorf(errProviderSecretValueForKeyNotFound, ref.Key)
		}
		rc, err = c.newRestConfigFn(kc)
		if err != nil {
			return nil, errors.Wrap(err, errFailedToCreateRestConfig)
		}
	default:
		return nil, errors.Errorf(errFmtUnsupportedCredSource, s)
	}

	k, err := c.newKubeClientFn(rc)
	if err != nil {
		return nil, errors.Wrap(err, errNewKubernetesClient)
	}

	h, err := c.newHelmClientFn(c.logger, rc, cr.Spec.ForProvider.Namespace, cr.Spec.ForProvider.Wait)
	if err != nil {
		return nil, errors.Wrap(err, errNewKubernetesClient)
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

func (e *helmExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1beta1.Release)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotRelease)
	}

	e.logger.Debug("Observing")

	rel, err := e.helm.GetLastRelease(meta.GetExternalName(cr))
	if err == driver.ErrReleaseNotFound {
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

	s, err := isUpToDate(ctx, e.localKube, &cr.Spec.ForProvider, rel, cr.Status)
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
	cv, err := composeValuesFromSpec(ctx, e.localKube, cr.Spec.ForProvider.ValuesSpec)
	if err != nil {
		return errors.Wrap(err, errFailedToComposeValues)
	}

	creds, err := repoCredsFromSecret(ctx, e.localKube, cr.Spec.ForProvider.Chart.PullSecretRef)
	if err != nil {
		return errors.Wrap(err, errFailedToGetRepoCreds)
	}

	p, err := e.patch.getFromSpec(ctx, e.localKube, cr.Spec.ForProvider.PatchesFrom)
	if err != nil {
		return errors.Wrap(err, errFailedToLoadPatches)
	}

	chart, err := e.helm.PullAndLoadChart(&cr.Spec.ForProvider.Chart, creds)
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

	if !cr.Spec.ForProvider.SkipCreateNamespace {
		if err := e.createNamespace(ctx, cr.Spec.ForProvider.Namespace); err != nil {
			if !kerrors.IsAlreadyExists(err) {
				return managed.ExternalCreation{}, errors.Wrap(err, errFailedToCreateNamespace)
			}
		}
	}

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

func (e *helmExternal) Delete(_ context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1beta1.Release)
	if !ok {
		return errors.New(errNotRelease)
	}

	e.logger.Debug("Deleting")

	return errors.Wrap(e.helm.Uninstall(meta.GetExternalName(cr)), errFailedToUninstall)
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
