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

package controller

import (
	"context"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	keyRepoUsername = "username"
	keyRepoPassword = "password"
)

const (
	errFailedToBuildChartDef           = "failed to build chart definition"
	errFailedToGetRepoPullSecret       = "failed to get repo pull secret"
	errChartPullSecretMissingNamespace = "namespace must be set in chart pull secret ref"
	errChartPullSecretMissingUsername  = "username missing in chart pull secret"
	errChartPullSecretMissingPassword  = "password missing in chart pull secret"
)

func chartDefFromSpec(ctx context.Context, kube client.Client, spec v1alpha1.ChartSpec) (helmClient.ChartDefinition, error) {
	repoUser := ""
	repoPass := ""
	if spec.PullSecretRef.Name != "" {
		if spec.PullSecretRef.Namespace == "" {
			return helmClient.ChartDefinition{}, errors.New(errChartPullSecretMissingNamespace)
		}
		d, err := getSecretData(ctx, kube, types.NamespacedName{Name: spec.PullSecretRef.Name, Namespace: spec.PullSecretRef.Namespace})
		if err != nil {
			return helmClient.ChartDefinition{}, errors.Wrap(err, errFailedToGetRepoPullSecret)
		}
		repoUser = string(d[keyRepoUsername])
		if repoUser == "" {
			return helmClient.ChartDefinition{}, errors.Wrap(err, errChartPullSecretMissingUsername)
		}
		repoPass = string(d[keyRepoPassword])
		if repoPass == "" {
			return helmClient.ChartDefinition{}, errors.Wrap(err, errChartPullSecretMissingPassword)
		}
	}

	return helmClient.ChartDefinition{
		Repository: spec.Repository,
		Name:       spec.Name,
		Version:    spec.Version,
		RepoUser:   repoUser,
		RepoPass:   repoPass,
	}, nil
}
