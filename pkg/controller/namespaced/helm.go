/*
Copyright 2020 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cluster

import (
	"time"

	kubeclient "github.com/crossplane-contrib/provider-kubernetes/pkg/kube/client"
	"github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/crossplane-contrib/provider-helm/pkg/controller/namespaced/config"
	"github.com/crossplane-contrib/provider-helm/pkg/controller/namespaced/release"
)

// Setup creates all Helm controllers with the supplied logger and adds them
// to the supplied manager. The client builder is shared with the controllers
// of the other scope so that its client cache is shared too.
func Setup(mgr ctrl.Manager, o controller.Options, timeout time.Duration, clientBuilder kubeclient.Builder) error {
	if err := config.Setup(mgr, o, timeout); err != nil {
		return err
	}
	return release.Setup(mgr, o, timeout, clientBuilder)
}

// SetupGated creates all controllers with the supplied logger and adds them to
// the supplied manager gated.
func SetupGated(mgr ctrl.Manager, o controller.Options, timeout time.Duration, clientBuilder kubeclient.Builder) error {
	if err := config.SetupGated(mgr, o, timeout); err != nil {
		return err
	}
	return release.SetupGated(mgr, o, timeout, clientBuilder)
}
