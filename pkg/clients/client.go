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

package clients

import (
	"fmt"
	"net/url"

	runtimev1alpha1 "github.com/crossplane/crossplane-runtime/apis/core/v1alpha1"
	"github.com/pkg/errors"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewRestConfig returns a rest config given a secret with connection information.
func NewRestConfig(creds map[string][]byte) (*rest.Config, error) {
	// If "kubeconfig" key found, use it
	kc, f := creds[runtimev1alpha1.ResourceCredentialsSecretKubeconfigKey]
	if f {
		ac, err := clientcmd.Load(kc)
		if err != nil {
			return nil, errors.Wrap(err, "failed to load kubeconfig")
		}
		return restConfigFromAPIConfig(ac)
	}

	u, err := url.Parse(string(creds[runtimev1alpha1.ResourceCredentialsSecretEndpointKey]))
	if err != nil {
		return nil, errors.Wrapf(err, "cannot parse Kubernetes endpoint as URL")
	}

	return &rest.Config{
		Host:     u.String(),
		Username: string(creds[runtimev1alpha1.ResourceCredentialsSecretUserKey]),
		Password: string(creds[runtimev1alpha1.ResourceCredentialsSecretPasswordKey]),
		TLSClientConfig: rest.TLSClientConfig{
			// This field's godoc claims clients will use 'the hostname used to
			// contact the server' when it is left unset. In practice clients
			// appear to use the URL, including scheme and port.
			ServerName: u.Hostname(),
			CAData:     creds[runtimev1alpha1.ResourceCredentialsSecretCAKey],
			CertData:   creds[runtimev1alpha1.ResourceCredentialsSecretClientCertKey],
			KeyData:    creds[runtimev1alpha1.ResourceCredentialsSecretClientKeyKey],
		},
		BearerToken: string(creds[runtimev1alpha1.ResourceCredentialsSecretTokenKey]),
	}, nil
}

// NewKubeClient returns a kubernetes client given a secret with connection
// information.
func NewKubeClient(config *rest.Config) (client.Client, error) {
	kc, err := client.New(config, client.Options{})
	if err != nil {
		return nil, errors.Wrap(err, "cannot create Kubernetes client")
	}

	return kc, nil
}

func restConfigFromAPIConfig(c *api.Config) (*rest.Config, error) {
	ctx := c.CurrentContext
	if ctx == "" {
		return nil, errors.New("currentContext not set in kubeconfig")
	}
	cluster := c.Clusters[ctx]
	if cluster == nil {
		return nil, errors.New(fmt.Sprintf("cluster for currentContext (%s) not found", ctx))
	}
	user := c.AuthInfos[ctx]
	if user == nil {
		return nil, errors.New(fmt.Sprintf("auth info for currentContext (%s) not found", ctx))
	}
	return &rest.Config{
		Host:            cluster.Server,
		Username:        user.Username,
		Password:        user.Password,
		BearerToken:     user.Token,
		BearerTokenFile: user.TokenFile,
		Impersonate: rest.ImpersonationConfig{
			UserName: user.Impersonate,
			Groups:   user.ImpersonateGroups,
			Extra:    user.ImpersonateUserExtra,
		},
		AuthProvider: user.AuthProvider,
		ExecProvider: user.Exec,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure:   cluster.InsecureSkipTLSVerify,
			ServerName: cluster.TLSServerName,
			CertData:   user.ClientCertificateData,
			KeyData:    user.ClientKeyData,
			CAData:     cluster.CertificateAuthorityData,
		},
	}, nil
}
