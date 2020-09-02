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

package controller

import (
	"context"

	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ktype "sigs.k8s.io/kustomize/api/types"

	"github.com/crossplane-contrib/provider-helm/apis/release/v1alpha1"
	helmv1alpha1 "github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
	"github.com/crossplane-contrib/provider-helm/pkg/clients"
	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

const (
	errNotRelease                 = "managed resource is not a Release custom resource"
	errProviderConfigNotSet       = "provider config is not set"
	errProviderNotRetrieved       = "provider could not be retrieved"
	errNewKubernetesClient        = "cannot create new Kubernetes client"
	errProviderSecretNotRetrieved = "secret referred in provider could not be retrieved"
	errFailedToGetLastRelease     = "failed to get last helm release"
	errLastReleaseIsNil           = "last helm release is nil"
	errFailedToCheckIfUpToDate    = "failed to check if release is up to date"
	errFailedToInstall            = "failed to install release"
	errFailedToUpgrade            = "failed to upgrade release"
	errFailedToUninstall          = "failed to uninstall release"
	errFailedToGetRepoCreds       = "failed to get user name and password from secret reference"
	errFailedToComposeValues      = "failed to compose values"
	errFailedToCreateRestConfig   = "cannot create new rest config using provider secret"
	errFailedToLoadPatches        = "failed to load patches"
	errFailedToUpdatePatchSha     = "failed to update patch sha"
	errFailedToSetVersion         = "failed to update chart spec with the latest version"
)

// SetupRelease adds a controller that reconciles Release managed resources.
func SetupRelease(mgr ctrl.Manager, l logging.Logger) error {
	name := managed.ControllerName(v1alpha1.ReleaseGroupKind)
	logger := l.WithValues("controller", name)

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.ReleaseGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			logger:          logger,
			client:          mgr.GetClient(),
			newRestConfigFn: clients.NewRestConfig,
			newKubeClientFn: clients.NewKubeClient,
			newHelmClientFn: helmClient.NewClient,
		}),
		managed.WithInitializers(managed.NewNameAsExternalName(mgr.GetClient())),
		managed.WithLogger(logger),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1alpha1.Release{}).
		Complete(r)
}

type connector struct {
	logger          logging.Logger
	client          client.Client
	newRestConfigFn func(creds map[string][]byte) (*rest.Config, error)
	newKubeClientFn func(config *rest.Config) (client.Client, error)
	newHelmClientFn func(log logging.Logger, config *rest.Config, namespace string) (helmClient.Client, error)
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Release)
	if !ok {
		return nil, errors.New(errNotRelease)
	}
	l := c.logger.WithValues("request", cr.Name)

	l.Debug("Connecting")

	p := &helmv1alpha1.ProviderConfig{}

	if cr.GetProviderConfigReference() == nil {
		return nil, errors.New(errProviderConfigNotSet)
	}

	n := types.NamespacedName{Name: cr.GetProviderConfigReference().Name}
	if err := c.client.Get(ctx, n, p); err != nil {
		return nil, errors.Wrap(err, errProviderNotRetrieved)
	}
	key := types.NamespacedName{Namespace: p.Spec.CredentialsSecretRef.Namespace, Name: p.Spec.CredentialsSecretRef.Name}
	creds, err := getSecretData(ctx, c.client, key)
	if err != nil {
		return nil, errors.Wrap(err, errProviderSecretNotRetrieved)
	}

	// We rely on multiple keys in ProviderConfig secret, so, ignoring "p.Spec.CredentialsSecretRef.Key"
	// TODO(hasan): Consider relying only "kubeconfig" key
	rc, err := c.newRestConfigFn(creds)
	if err != nil {
		return nil, errors.Wrap(err, errFailedToCreateRestConfig)
	}

	k, err := c.newKubeClientFn(rc)
	if err != nil {
		return nil, errors.Wrap(err, errNewKubernetesClient)
	}

	h, err := c.newHelmClientFn(c.logger, rc, cr.Spec.ForProvider.Namespace)

	return &helmExternal{
		logger:    l,
		localKube: c.client,
		kube:      k,
		helm:      h,
		patch:     newPatcher(),
	}, errors.Wrap(err, errNewKubernetesClient)
}

type helmExternal struct {
	logger    logging.Logger
	localKube client.Client
	kube      client.Client
	helm      helmClient.Client
	patch     Patcher
}

func (e *helmExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Release)
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
	s, err := isUpToDate(ctx, e.localKube, &cr.Spec.ForProvider, rel, cr.Status)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errFailedToCheckIfUpToDate)
	}
	cr.Status.Synced = s
	if cr.Status.AtProvider.State == release.StatusDeployed && s {
		cr.Status.Failed = 0
	}

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: cr.Status.Synced && !(shouldRollBack(cr) && !rollBackLimitReached(cr)),
	}, nil
}

type deployAction func(release string, chart *chart.Chart, vals map[string]interface{}, patches []ktype.Patch) (*release.Release, error)

func (e *helmExternal) deploy(ctx context.Context, cr *v1alpha1.Release, action deployAction) error {
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
	cr, ok := mg.(*v1alpha1.Release)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotRelease)
	}

	e.logger.Debug("Creating")
	return managed.ExternalCreation{}, errors.Wrap(e.deploy(ctx, cr, e.helm.Install), errFailedToInstall)
}

func (e *helmExternal) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Release)
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
	cr, ok := mg.(*v1alpha1.Release)
	if !ok {
		return errors.New(errNotRelease)
	}

	e.logger.Debug("Deleting")

	return errors.Wrap(e.helm.Uninstall(meta.GetExternalName(cr)), errFailedToUninstall)
}

func shouldRollBack(cr *v1alpha1.Release) bool {
	return cr.Status.Synced && cr.Status.AtProvider.State == release.StatusFailed && rollBackEnabled(cr)
}

func rollBackEnabled(cr *v1alpha1.Release) bool {
	return cr.Spec.RollbackRetriesLimit != nil
}
func rollBackLimitReached(cr *v1alpha1.Release) bool {
	return cr.Status.Failed >= *cr.Spec.RollbackRetriesLimit
}
