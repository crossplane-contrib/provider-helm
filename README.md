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

If you would like to install `provider-helm` without modifications, you may do
so using the Crossplane CLI in a Kubernetes cluster where Crossplane is
installed:

```console
crossplane xpkg install provider xpkg.crossplane.io/crossplane-contrib/provider-helm:v0.20.0
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
configuration](./examples/provider-config/provider-incluster.yaml) to bind its
service account to an existing role in the cluster:

```console 
kubectl apply -f ./examples/provider-config/provider-incluster.yaml
```

Then simply create a
[`ProviderConfig`](./examples/provider-config/provider-config-incluster.yaml)
that uses an `InjectedIdentity` source:
  
```console 
kubectl apply -f ./examples/provider-config/provider-config-incluster.yaml
```

`provider-helm` will then be installed and ready to use within the cluster. You
can now create `Release` resources, such as [sample
release.yaml](examples/sample/release.yaml).

```console
kubectl create -f examples/sample/release.yaml
```

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

Since the controller is running outside of the local cluster, you need to make
the API server accessible (on a separate terminal):

```console
sudo kubectl proxy --port=8081
```

Then we must prepare a `ProviderConfig` for the local cluster (assuming you are
using `kind` for local development):

```console
KUBECONFIG=$(kind get kubeconfig | sed -e 's|server:\s*.*$|server: http://localhost:8081|g')
kubectl -n crossplane-system create secret generic cluster-config --from-literal=kubeconfig="${KUBECONFIG}" 
kubectl apply -f examples/provider-config/provider-config-with-secret.yaml
```

Now you can create `Release` resources with this `ProviderConfig`, for example
[sample release.yaml](examples/sample/release.yaml).

```console
kubectl create -f examples/sample/release.yaml
```
