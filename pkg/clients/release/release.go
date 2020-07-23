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
	"reflect"

	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/release"
	"sigs.k8s.io/yaml"

	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
)

const (
	errChartNilInObservedRelease      = "chart field is nil in observed helm release"
	errChartMetaNilInObservedRelease  = "chart metadata field is nil in observed helm release"
	errFailedToUnmarshalDesiredValues = "failed to unmarshal desired values"
)

// GenerateObservation generates release observation for the input release object
func GenerateObservation(in *release.Release) v1alpha1.ReleaseObservation {
	o := v1alpha1.ReleaseObservation{}

	relInfo := in.Info
	if relInfo != nil {
		o.State = relInfo.Status
		o.ReleaseDescription = relInfo.Description
	}
	return o
}

// IsUpToDate checks whether desired spec up to date with the observed state for a given release
func IsUpToDate(in *v1alpha1.ReleaseParameters, observed *release.Release) (bool, error) {
	oc := observed.Chart
	if oc == nil {
		return false, errors.New(errChartNilInObservedRelease)
	}

	ocm := oc.Metadata
	if ocm == nil {
		return false, errors.New(errChartMetaNilInObservedRelease)
	}
	if in.Chart.Name != ocm.Name {
		return false, nil
	}
	if in.Chart.Version != ocm.Version {
		return false, nil
	}
	var desiredConfig map[string]interface{}
	err := yaml.Unmarshal([]byte(in.Values), &desiredConfig)
	if err != nil {
		return false, errors.Wrap(err, errFailedToUnmarshalDesiredValues)
	}
	if !reflect.DeepEqual(desiredConfig, observed.Config) {
		return false, nil
	}

	return true, nil
}
