module github.com/crossplane-contrib/provider-helm

go 1.13

require (
	github.com/crossplane/crossplane v0.11.1
	github.com/crossplane/crossplane-runtime v0.9.0
	github.com/crossplane/crossplane-tools v0.0.0-20200412230150-efd0edd4565b
	github.com/pkg/errors v0.9.1
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	helm.sh/helm/v3 v3.2.4
	k8s.io/api v0.18.5
	k8s.io/apimachinery v0.18.5
	k8s.io/cli-runtime v0.18.5
	k8s.io/client-go v0.18.5
	sigs.k8s.io/controller-runtime v0.6.0
	sigs.k8s.io/controller-tools v0.2.4
)
