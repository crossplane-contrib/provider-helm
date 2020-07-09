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
	"fmt"
	"github.com/crossplane-contrib/provider-helm/pkg/clients"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
	kubev1alpha1 "github.com/crossplane/crossplane/apis/kubernetes/v1alpha1"
)

const (
	errNotRelease                  = "managed resource is not a Release custom resource"
	errProviderNotRetrieved       = "provider could not be retrieved"
	errProviderSecretNil          = "cannot find Secret reference on Provider"
	errNewKubernetesClient         = "cannot create new Kubernetes client"
	errProviderSecretNotRetrieved = "secret referred in provider could not be retrieved"
)

// SetupRelease adds a controller that reconciles Release managed resources.
func SetupRelease(mgr ctrl.Manager, l logging.Logger) error {
	name := managed.ControllerName(v1alpha1.ReleaseGroupKind)

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.ReleaseGroupVersionKind),
		managed.WithExternalConnecter(&connector{client: mgr.GetClient(), newClient: clients.NewClient}),
		managed.WithInitializers(managed.NewNameAsExternalName(mgr.GetClient())),
		managed.WithLogger(l.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1alpha1.Release{}).
		Complete(r)
}

type connector struct {
	client    client.Client
	newClient func(ctx context.Context, secret *v1.Secret, scheme *runtime.Scheme) (client.Client, error)
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Release)
	if !ok {
		return nil, errors.New(errNotRelease)
	}

	p := &kubev1alpha1.Provider{}
	n := meta.NamespacedNameOf(cr.Spec.ProviderReference)
	if err := c.client.Get(ctx, n, p); err != nil {
		return nil, errors.Wrap(err, errProviderNotRetrieved)
	}

	s := &v1.Secret{}
	n = types.NamespacedName{Namespace: p.Spec.Secret.Namespace, Name: p.Spec.Secret.Name}
	if err := c.client.Get(ctx, n, s); err != nil {
		return nil, errors.Wrap(err, errProviderSecretNotRetrieved)
	}

	k, err := c.newClient(ctx, s, nil)

	return &external{kube: k}, errors.Wrap(err, errNewKubernetesClient)
}

type external struct {
	kube    client.Client
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Release)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotRelease)
	}

	fmt.Printf("Observing: %+v", cr)

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: true,
		// ConnectionDetails: getConnectionDetails(cr, instance),
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Release)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotRelease)
	}

	fmt.Printf("Creating: %+v", cr)

	return managed.ExternalCreation{}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Release)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotRelease)
	}

	fmt.Printf("Updating: %+v", cr)

	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.Release)
	if !ok {
		return errors.New(errNotRelease)
	}

	fmt.Printf("Deleting: %+v", cr)

	return nil
}
