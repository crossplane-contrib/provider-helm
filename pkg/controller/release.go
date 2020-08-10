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
	kubev1alpha1 "github.com/crossplane/crossplane/apis/kubernetes/v1alpha1"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
	"github.com/crossplane-contrib/provider-helm/pkg/clients"
	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

const (
	errNotRelease                 = "managed resource is not a Release custom resource"
	errProviderNotRetrieved       = "provider could not be retrieved"
	errNewKubernetesClient        = "cannot create new Kubernetes client"
	errProviderSecretNotRetrieved = "secret referred in provider could not be retrieved"
	errFailedToGetLastRelease     = "failed to get last helm release"
	errLastReleaseIsNil           = "last helm release is nil"
	errFailedToCheckIfUpToDate    = "failed to check if release is up to date"
	errFailedToInstall            = "failed to install release"
	errFailedToUpgrade            = "failed to upgrade release"
	errFailedToUninstall          = "failed to uninstall release"
	errFailedToBuildChartDef      = "failed to build chart definition"
	errFailedToComposeValues      = "failed to compose values"
	errFailedToCreateRestConfig   = "cannot create new rest config using provider secret"
	errFailedToLoadPatches        = "failed to load patches"
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

	p := &kubev1alpha1.Provider{}
	n := meta.NamespacedNameOf(cr.Spec.ProviderReference)
	if err := c.client.Get(ctx, n, p); err != nil {
		return nil, errors.Wrap(err, errProviderNotRetrieved)
	}
	key := types.NamespacedName{Namespace: p.Spec.Secret.Namespace, Name: p.Spec.Secret.Name}
	creds, err := getSecretData(ctx, c.client, key)
	if err != nil {
		return nil, errors.Wrap(err, errProviderSecretNotRetrieved)
	}

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
	}, errors.Wrap(err, errNewKubernetesClient)
}

type helmExternal struct {
	logger    logging.Logger
	localKube client.Client
	kube      client.Client
	helm      helmClient.Client
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

	u, err := isUpToDate(ctx, e.localKube, &cr.Spec.ForProvider, rel)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errFailedToCheckIfUpToDate)
	}

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: u,
	}, nil
}

func (e *helmExternal) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Release)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotRelease)
	}

	e.logger.Debug("Creating")

	cv, err := composeValuesFromSpec(ctx, e.localKube, cr.Spec.ForProvider.ValuesSpec)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errFailedToComposeValues)
	}

	cd, err := chartDefFromSpec(ctx, e.localKube, cr.Spec.ForProvider.Chart)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errFailedToBuildChartDef)
	}

	p, err := getPatchesFromSpec(ctx, e.localKube, cr.Spec.ForProvider.PatchesFrom)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errFailedToLoadPatches)
	}

	rel, err := e.helm.Install(meta.GetExternalName(cr), cd, cv, p)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errFailedToInstall)
	}
	if rel == nil {
		return managed.ExternalCreation{}, errors.New(errLastReleaseIsNil)
	}

	cr.Status.AtProvider = generateObservation(rel)
	return managed.ExternalCreation{}, nil
}

func (e *helmExternal) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Release)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotRelease)
	}

	e.logger.Debug("Updating")

	cv, err := composeValuesFromSpec(ctx, e.localKube, cr.Spec.ForProvider.ValuesSpec)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errFailedToComposeValues)
	}

	cd, err := chartDefFromSpec(ctx, e.localKube, cr.Spec.ForProvider.Chart)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errFailedToBuildChartDef)
	}

	p, err := getPatchesFromSpec(ctx, e.localKube, cr.Spec.ForProvider.PatchesFrom)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errFailedToLoadPatches)
	}

	rel, err := e.helm.Upgrade(meta.GetExternalName(cr), cd, cv, p)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errFailedToUpgrade)
	}
	if rel == nil {
		return managed.ExternalUpdate{}, errors.New(errLastReleaseIsNil)
	}

	cr.Status.AtProvider = generateObservation(rel)
	return managed.ExternalUpdate{}, nil
}

func (e *helmExternal) Delete(_ context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.Release)
	if !ok {
		return errors.New(errNotRelease)
	}

	e.logger.Debug("Deleting")

	return errors.Wrap(e.helm.Uninstall(meta.GetExternalName(cr)), errFailedToUninstall)
}
