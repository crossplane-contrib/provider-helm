# provider-helm (WIP)

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

Run against a Kubernetes cluster:
```
make run
```

## Testing in Local Cluster

Create a provider for local cluster. See [Kubernetes native providers](https://github.com/crossplane/crossplane/blob/master/design/one-pager-k8s-native-providers.md#proposal-kubernetes-provider-kind)
for more information.

1. Deploy [RBAC for local cluster](examples/provider/local-service-account.yaml)
2. Get the token secret name for the created service account:
   
    ```
    kubectl get sa helm-provider -n crossplane-system
    ```
3. Replace `spec.credentialsSecretRef.name` with the token secret name in [local-provider.yaml](examples/provider/local-provider.yaml).
4. Deploy [local-provider.yaml](examples/provider/local-provider.yaml).
5. Now you can create `Release` resources with provider reference, see [sample release.yaml](examples/sample/release.yaml).
