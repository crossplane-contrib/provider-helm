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

package release

import (
	"context"

	runtimev1alpha1 "github.com/crossplane/crossplane-runtime/apis/core/v1alpha1"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

const (
	keyRepoUsername = "username"
	keyRepoPassword = "password"
)

const (
	errFailedToGetRepoPullSecret       = "failed to get repo pull secret"
	errChartPullSecretMissingNamespace = "namespace must be set in chart pull secret ref"
	errChartPullSecretMissingUsername  = "username missing in chart pull secret"
	errChartPullSecretMissingPassword  = "password missing in chart pull secret"
)

func repoCredsFromSecret(ctx context.Context, kube client.Client, secretRef runtimev1alpha1.SecretReference) (*helmClient.RepoCreds, error) {
	repoUser := ""
	repoPass := ""
	if secretRef.Name != "" {
		if secretRef.Namespace == "" {
			return nil, errors.New(errChartPullSecretMissingNamespace)
		}
		d, err := getSecretData(ctx, kube, types.NamespacedName{Name: secretRef.Name, Namespace: secretRef.Namespace})
		if err != nil {
			return nil, errors.Wrap(err, errFailedToGetRepoPullSecret)
		}
		repoUser = string(d[keyRepoUsername])
		if repoUser == "" {
			return nil, errors.New(errChartPullSecretMissingUsername)
		}
		repoPass = string(d[keyRepoPassword])
		if repoPass == "" {
			return nil, errors.New(errChartPullSecretMissingPassword)
		}
	}

	return &helmClient.RepoCreds{
		Username: repoUser,
		Password: repoPass,
	}, nil
}
