module github.com/crossplane-contrib/provider-helm

go 1.13

require (
	github.com/crossplane/crossplane-runtime v0.9.1-0.20201001234308-3f1afd106a8c
	github.com/crossplane/crossplane-tools v0.0.0-20201001224552-fb258cc0eb30
	github.com/go-logr/zapr v0.1.1 // indirect
	github.com/golang/groupcache v0.0.0-20190702054246-869f871628b6 // indirect
	github.com/google/go-cmp v0.4.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.1.0 // indirect
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
