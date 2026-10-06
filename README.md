[![Build Actions Status](https://github.com/crossplane-contrib/provider-helm/workflows/CI/badge.svg)](https://github.com/crossplane-contrib/provider-helm/actions)
[![GitHub release](https://img.shields.io/github/release/crossplane-contrib/provider-helm/all.svg?style=flat-square)](https://github.com/crossplane-contrib/provider-helm/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/crossplane-contrib/provider-helm)](https://goreportcard.com/report/github.com/crossplane-contrib/provider-helm)

# provider-helm

`provider-helm` is a Crossplane Provider that enables deployment and management
of Helm Releases on Kubernetes clusters typically provisioned by Crossplane, and
has the following functionality:

- A `Release` resource type to manage Helm Releases.
- A managed resource controller that reconciles `Release` objects and manages
  Helm releases.

## Install

If you would like to install the latest version of `provider-helm` without modifications, you may do
so using the Crossplane CLI in a Kubernetes cluster where Crossplane is
installed:

```shell
latest_tag=$(crane ls xpkg.crossplane.io/crossplane-contrib/provider-helm | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n1)
crossplane xpkg install provider xpkg.crossplane.io/crossplane-contrib/provider-helm:$latest_tag
```

Then you will need to create a `ProviderConfig` that specifies the credentials
to connect to the Kubernetes API. This is commonly done within a `Composition`
by storing a `kubeconfig` into a secret that the `ProviderConfig` references. An
example of this approach can be found in
[`configuration-aws-eks`](https://github.com/upbound/configuration-aws-eks/blob/release-0.7/apis/composition.yaml#L427-L452).

### Quick start

An alternative, that will get you started quickly, is to reuse existing
credentials from within the control plane.

First install `provider-helm` with [additional
configuration](./examples/cluster/provider-config/provider-incluster.yaml) to bind its
service account to an existing role in the cluster:

```console 
kubectl apply -f ./examples/cluster/provider-config/provider-incluster.yaml
```

Then simply create a
[`ProviderConfig`](./examples/cluster/provider-config/provider-config-incluster.yaml)
that uses an `InjectedIdentity` source:
  
```console 
kubectl apply -f ./examples/cluster/provider-config/provider-config-incluster.yaml
```

`provider-helm` will then be installed and ready to use within the cluster. You
can now create `Release` resources, such as [sample
release.yaml](examples/cluster/sample/release.yaml).

```console
kubectl create -f examples/cluster/sample/release.yaml
```

## Target cluster clients

The provider builds one client per distinct ProviderConfig credential set
(kubeconfig plus identity) and keeps it in an LRU cache shared by the
cluster-scoped and namespaced `Release` controllers. Building a client is
expensive because its REST mapper primes itself with full aggregated discovery
on first use, so the cache has to hold every credential set the provider talks
to. Once the credential sets in use outnumber the bound, every new client
evicts the least recently used one, whose next reconcile rebuilds it (discovery
included) and evicts another: the whole cache churns, not only the credential
set that did not fit.

At startup the provider sizes the cache from the cluster: it lists its
`ProviderConfig` and `ClusterProviderConfig` objects, derives the cache key of
each one the way the client builder does (so ProviderConfigs sharing a
kubeconfig count once, and every in-cluster ProviderConfig counts as one), and
bounds the cache at the number of distinct credential sets plus headroom of
ten percent or four entries, whichever is larger, for credentials that rotate
and ProviderConfigs created later, never below 8. The result is logged at
startup. The provider does not start if its ProviderConfigs and their
credentials cannot be read within 300 seconds. When the provider runs outside a
cluster, as in local development, ProviderConfigs with an in-cluster identity
cannot be resolved and each counts as a credential set of its own, so the
cache is merely oversized.

The bound is fixed for the lifetime of the process, so ProviderConfigs created
after startup share the headroom until the next restart. The cache reports
`provider_helm_client_cache_size` (the bound),
`provider_helm_client_cache_entries` (clients currently cached) and
`provider_helm_client_cache_events_total` with an `event` label of `hit`,
`miss` or `evict`; a sustained eviction rate means the credential sets in use
no longer fit the bound and the cache is churning.

Client-side rate limiting is disabled for these clients. A cached client is
shared by every concurrent reconcile of its credential set, so client-go's
per-client token bucket (5 QPS, burst 10) would serialize them. This applies to
everything built from the same configuration, including the Helm client that
installs, upgrades and observes releases. Load on the target cluster is bounded
by `--max-reconcile-rate` on the provider side and by API Priority and Fairness
on the API server.

## Design 

See [the design
document](https://github.com/crossplane/crossplane/blob/master/design/one-pager-helm-provider.md).

## Developing locally

**Pre-requisite:** A Kubernetes cluster with Crossplane installed

To run the `provider-helm` controller against your existing local cluster,
simply run:

```console
make run
```

Since the controller is running outside the local cluster, you need to make
the API server accessible (on a separate terminal):

```console
sudo kubectl proxy --port=8081
```

Then we must prepare a `ProviderConfig` for the local cluster (assuming you are
using `kind` for local development):

```console
KUBECONFIG=$(kind get kubeconfig | sed -e 's|server:\s*.*$|server: http://localhost:8081|g')
kubectl -n crossplane-system create secret generic cluster-config --from-literal=kubeconfig="${KUBECONFIG}" 
kubectl apply -f examples/cluster/provider-config/provider-config-with-secret.yaml
```

Now you can create `Release` resources with this `ProviderConfig`, for example
[sample release.yaml](examples/cluster/sample/release.yaml).

```console
kubectl create -f examples/cluster/sample/release.yaml
```
