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
	"fmt"

	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/strvals"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
)

const (
	keyDefaultValuesFrom = "values.yaml"
	keyDefaultSet        = "value"
)

const (
	errFailedToUnmarshalDesiredValues = "failed to unmarshal desired values"
	errFailedParsingSetData           = "failed parsing --set data"
	errFailedToGetValueFromSource     = "failed to get value from source"
	errMissingValueForSet             = "missing value for --set"
)

func composeValuesFromSpec(ctx context.Context, kube client.Client, spec v1alpha1.ValuesSpec) (map[string]interface{}, error) {
	base := map[string]interface{}{}

	for _, vf := range spec.ValuesFrom {
		s, err := getDataValueFromSource(ctx, kube, vf, keyDefaultValuesFrom)
		if err != nil {
			return nil, errors.Wrap(err, errFailedToGetValueFromSource)
		}

		var currVals map[string]interface{}
		if err = yaml.Unmarshal([]byte(s), &currVals); err != nil {
			return nil, errors.Wrap(err, errFailedToUnmarshalDesiredValues)
		}
		base = mergeMaps(base, currVals)
	}

	var inlineVals map[string]interface{}
	err := yaml.Unmarshal([]byte(spec.Values), &inlineVals)
	if err != nil {
		return nil, errors.Wrap(err, errFailedToUnmarshalDesiredValues)
	}

	base = mergeMaps(base, inlineVals)

	for _, s := range spec.Set {
		v := ""
		if s.Value != "" {
			v = s.Value
		}
		if s.ValueFrom != nil {
			v, err = getDataValueFromSource(ctx, kube, *s.ValueFrom, keyDefaultSet)
			if err != nil {
				return nil, errors.Wrap(err, errFailedToGetValueFromSource)
			}
		}

		if v == "" {
			return nil, errors.New(errMissingValueForSet)
		}

		if err := strvals.ParseInto(fmt.Sprintf("%s=%s", s.Name, v), base); err != nil {
			return nil, errors.Wrap(err, errFailedParsingSetData)
		}
	}

	return base, nil
}

// Copied from helm cli
// https://github.com/helm/helm/blob/9bc7934f350233fa72a11d2d29065aa78ab62792/pkg/cli/values/options.go#L88
func mergeMaps(a, b map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(a))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if v, ok := v.(map[string]interface{}); ok {
			if bv, ok := out[k]; ok {
				if bv, ok := bv.(map[string]interface{}); ok {
					out[k] = mergeMaps(bv, v)
					continue
				}
			}
		}
		out[k] = v
	}
	return out
}
