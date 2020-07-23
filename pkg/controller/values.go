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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
)

const (
	errFailedToUnmarshalDesiredValues = "failed to unmarshal desired values"
)

func composeValuesFromSpec(_ context.Context, _ client.Client, spec v1alpha1.ValuesSpec) (map[string]interface{}, error) {
	var composed map[string]interface{}
	err := yaml.Unmarshal([]byte(spec.Values), &composed)
	if err != nil {
		return nil, errors.Wrap(err, errFailedToUnmarshalDesiredValues)
	}
	return composed, nil
}
