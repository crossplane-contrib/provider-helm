module github.com/crossplane-contrib/provider-helm

go 1.16

require (
	cloud.google.com/go v0.84.0 // indirect
	github.com/crossplane/crossplane-runtime v0.12.0
	github.com/crossplane/crossplane-tools v0.0.0-20201007233256-88b291e145bb
	github.com/go-logr/zapr v0.1.1 // indirect
	github.com/google/go-cmp v0.5.6
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.1.0 // indirect
	golang.org/x/oauth2 v0.0.0-20210628180205-a41e5a781914
	golang.org/x/sys v0.0.0-20210616094352-59db8d763f22 // indirect
	golang.org/x/tools v0.1.4 // indirect
	google.golang.org/genproto v0.0.0-20210624195500-8bfb893ecb84 // indirect
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	helm.sh/helm/v3 v3.2.4
	k8s.io/api v0.18.8
	k8s.io/apimachinery v0.18.8
	k8s.io/client-go v0.18.8
	k8s.io/klog/v2 v2.0.0
	rsc.io/letsencrypt v0.0.3 // indirect
	sigs.k8s.io/controller-runtime v0.6.2
	sigs.k8s.io/controller-tools v0.3.0
	sigs.k8s.io/kustomize/api v0.5.1
	sigs.k8s.io/yaml v1.2.0
)

replace github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.2+incompatible
