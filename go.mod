module github.com/crossplane-contrib/provider-helm

go 1.16

require (
	github.com/crossplane/crossplane-runtime v0.13.0
	github.com/crossplane/crossplane-tools v0.0.0-20210320162312-1baca298c527
	github.com/google/go-cmp v0.5.6
	github.com/pkg/errors v0.9.1
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	helm.sh/helm/v3 v3.6.2
	k8s.io/api v0.21.2
	k8s.io/apimachinery v0.21.2
	k8s.io/client-go v0.21.2
	sigs.k8s.io/controller-runtime v0.9.2
	sigs.k8s.io/controller-tools v0.6.1
	sigs.k8s.io/kustomize/api v0.8.11
	sigs.k8s.io/kustomize/kyaml v0.11.0
	sigs.k8s.io/yaml v1.2.0
)

replace (
	// todo(turkenh): remove once crossplane-runtime updated
	github.com/crossplane/crossplane-runtime => github.com/turkenh/crossplane-runtime v0.0.0-20210711130541-b667f9ec0a4f
	// See https://github.com/helm/helm/blob/release-3.6.2/go.mod#L50
	github.com/docker/distribution => github.com/docker/distribution v0.0.0-20191216044856-a8371794149d
)
