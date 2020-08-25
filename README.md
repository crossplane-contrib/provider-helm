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

If you would like to install `provider-helm` without modifications create
the following `ClusterPackageInstall` in a Kubernetes cluster where Crossplane is
installed:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: helm
---
apiVersion: packages.crossplane.io/v1alpha1
kind: ClusterPackageInstall
metadata:
  name: provider-helm
  namespace: helm
spec:
  package: "crossplane-contrib/provider-helm:latest"
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
sudo kubectl proxy --port=80
```

### Testing in Local Cluster

Create a provider for local cluster. See [Kubernetes native providers](https://github.com/crossplane/crossplane/blob/master/design/one-pager-k8s-native-providers.md#proposal-kubernetes-provider-kind)
for more information.

1. Deploy [RBAC for local cluster](examples/provider/local-service-account.yaml)

    ```
    kubectl apply -f examples/provider/local-service-account.yaml
    ```
1. Deploy [local-provider.yaml](examples/provider/local-provider.yaml) by replacing `spec.credentialsSecretRef.name` with the token secret name.

    ```
    EXP="s/<helm-provider-token-secret-name>/$(kubectl get sa helm-provider -n crossplane-system -o jsonpath="{.secrets[0].name}")/g"
    cat examples/provider/local-provider.yaml | sed -e "${EXP}" | kubectl apply -f -
    ```
1. Now you can create `Release` resources with provider reference, see [sample release.yaml](examples/sample/release.yaml).

### Cleanup

```
make local.down
```
