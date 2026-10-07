/*
Copyright 2026 The Crossplane Authors.

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

package main

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kubeclient "github.com/crossplane-contrib/provider-kubernetes/pkg/kube/client"
	kconfig "github.com/crossplane-contrib/provider-kubernetes/pkg/kube/config"
	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	clusterv1beta1 "github.com/crossplane-contrib/provider-helm/apis/cluster/v1beta1"
	namespacedv1beta1 "github.com/crossplane-contrib/provider-helm/apis/namespaced/v1beta1"
	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

// clientCacheSizeTimeout bounds the startup listing and credential reads that
// size the target cluster client cache; the provider does not start without a
// size, since a partial count would not be a bound.
const clientCacheSizeTimeout = 300 * time.Second

// clientCacheSize returns the bound of the target cluster client cache,
// derived from the ProviderConfigs kube lists and the credentials they
// reference. The bound is fixed for the lifetime of the process, so
// ProviderConfigs created later share whatever headroom is left.
func clientCacheSize(ctx context.Context, log logging.Logger, kube client.Client) (int, error) {
	pcs, err := providerConfigSpecs(ctx, kube)
	if err != nil {
		return 0, errors.Wrap(err, "cannot size target cluster client cache from ProviderConfigs")
	}

	sizing, err := kubeclient.ClientCacheSizeFor(ctx, kube, pcs)
	if err != nil {
		return 0, errors.Wrap(err, "cannot size the client cache")
	}

	log.Info("Sized target cluster client cache from ProviderConfigs",
		"providerConfigs", len(pcs),
		"credentialSets", sizing.CredentialSets,
		"unresolved", sizing.Unresolved,
		"clientCacheSize", sizing.Size)
	return sizing.Size, nil
}

// providerConfigSpecs returns the spec of every ProviderConfig the provider
// serves, as the Release controllers resolve it: the legacy cluster-scoped
// kind and the namespaced and cluster-scoped kinds of the namespaced API
// group. A kind whose CRD is not installed contributes none.
func providerConfigSpecs(ctx context.Context, kube client.Reader) ([]kconfig.ProviderConfigSpec, error) {
	var pcs []kconfig.ProviderConfigSpec

	legacy := &clusterv1beta1.ProviderConfigList{}
	if err := kube.List(ctx, legacy); err != nil && !meta.IsNoMatchError(err) {
		return nil, errors.Wrapf(err, "cannot list %s", clusterv1beta1.ProviderConfigGroupKind)
	}
	for i := range legacy.Items {
		pcs = append(pcs, legacy.Items[i].Spec)
	}

	namespaced := &namespacedv1beta1.ProviderConfigList{}
	if err := kube.List(ctx, namespaced); err != nil && !meta.IsNoMatchError(err) {
		return nil, errors.Wrapf(err, "cannot list %s", namespacedv1beta1.ProviderConfigGroupKind)
	}
	for i := range namespaced.Items {
		pc := &namespaced.Items[i]
		helmClient.EnrichLocalSecretRefs(pc, pc.GetNamespace())
		pcs = append(pcs, pc.Spec)
	}

	cluster := &namespacedv1beta1.ClusterProviderConfigList{}
	if err := kube.List(ctx, cluster); err != nil && !meta.IsNoMatchError(err) {
		return nil, errors.Wrapf(err, "cannot list %s", namespacedv1beta1.ClusterProviderConfigGroupKind)
	}
	for i := range cluster.Items {
		pcs = append(pcs, cluster.Items[i].Spec)
	}

	return pcs, nil
}
