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

## Developing

Run against a Kubernetes cluster:
```
make run
```

Install `latest` into Kubernetes cluster where Crossplane is installed:
```
make install
```

Install local build into [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/)
cluster where Crossplane is installed:
```
make install-local
```

Build, push, and install:
```
make all
```

Build image:
```
make image
```

Push image:
```
make push
```

Build binary:
```
make build
```

Build package:
```
make build-package
```