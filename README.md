[![Build Status](https://jenkinsci.upbound.io/job/crossplane/job/provider-helm/job/provider-helm/job/master/badge/icon)](https://jenkinsci.upbound.io/job/crossplane/job/provider-helm/job/provider-helm/job/master/)
[![GitHub release](https://img.shields.io/github/release/crossplane-contrib/provider-helm/all.svg?style=flat-square)](https://github.com/crossplane-contrib/provider-helm/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/crossplane-contrib/provider-helm)](https://goreportcard.com/report/github.com/crossplane-contrib/provider-helm)

# provider-helm

`provider-helm` is a Crossplane Provider that enables deployment and management
of Helm Releases on Kubernetes clusters typically provisioned by Crossplane:

- A `Provider` resource type that only points to a credentials `Secret`.
- A `Release` resource type that is to manage Helm Releases.
- A managed resource controller that reconciles `Release` objects and manages Helm releases.

## Install

If you would like to install `provider-helm` without modifications, you may do
so using the Crossplane CLI in a Kubernetes cluster where Crossplane is
installed:

```console
kubectl crossplane install provider crossplane/provider-helm:master
```

You may also manually install `provider-helm` by creating a `Provider` directly:

```yaml
apiVersion: pkg.crossplane.io/v1alpha1
kind: Provider
metadata:
  name: provider-helm
spec:
  package: "crossplane/provider-helm:master"
```

## Design 

See [the design document](https://github.com/crossplane/crossplane/blob/master/design/one-pager-helm-provider.md).

## Developing locally

Start a local development environment with Kind where `crossplane` is installed:

```
make local-dev
```

Run controller against the cluster:

```
make run
```

Since controller is running outside of the Kind cluster, you need to make api server accessible (on a separate terminal):

```
sudo kubectl proxy --port=8081
```

### Testing in Local Cluster

1. Prepare KUBECONFIG for local cluster:
    1. If helm provider running in cluster (e.g. provider installed with crossplane):
    
        ```
        KUBECONFIG=$(kind get kubeconfig --name local-dev | sed -e 's|server:\s*.*$|server: https://kubernetes.default.svc|g')
        ```
    1. If provider helm running outside of the cluster (e.g. running locally with `make run`)
    
        ```
        KUBECONFIG=$(kind get kubeconfig --name local-dev | sed -e 's|server:\s*.*$|server: http://localhost:8081|g')
        ```

1. Create KUBECONFIG secret for local cluster and deploy [local-provider.yaml](examples/provider-config/local-provider-config.yaml).

    ```
    kubectl -n crossplane-system create secret generic local-cluster --from-literal=kubeconfig="${KUBECONFIG}" 
    kubectl apply -f examples/provider-config/local-provider-config.yaml 
    ```

1. Now you can create `Release` resources with provider reference, see [sample release.yaml](examples/sample/release.yaml).

    ```
    kubectl create -f examples/sample/release.yaml
    ```

### Cleanup

```
make local.down
```
