module github.com/crossplane-contrib/provider-helm

go 1.13

require (
	github.com/crossplane/crossplane v0.11.1
	github.com/crossplane/crossplane-runtime v0.9.1-0.20200827134951-29150890d4c1
	github.com/crossplane/crossplane-tools v0.0.0-20200827141855-f51a6598f2bc
	github.com/google/go-cmp v0.4.0
	github.com/pkg/errors v0.9.1
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	helm.sh/helm/v3 v3.2.4
	k8s.io/api v0.18.8
	k8s.io/apimachinery v0.18.8
	k8s.io/client-go v0.18.8
	rsc.io/letsencrypt v0.0.3 // indirect
	sigs.k8s.io/controller-runtime v0.6.2
	sigs.k8s.io/controller-tools v0.3.0
	sigs.k8s.io/kustomize/api v0.5.1
	sigs.k8s.io/yaml v1.2.0
)
