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
	"reflect"

	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/release"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
)

const (
	errReleaseInfoNilInObservedRelease = "release info is nil in observed helm release"
	errChartNilInObservedRelease       = "chart field is nil in observed helm release"
	errChartMetaNilInObservedRelease   = "chart metadata field is nil in observed helm release"
)

// generateObservation generates release observation for the input release object
func generateObservation(in *release.Release) v1alpha1.ReleaseObservation {
	o := v1alpha1.ReleaseObservation{}

	relInfo := in.Info
	if relInfo != nil {
		o.State = relInfo.Status
		o.ReleaseDescription = relInfo.Description
		o.Revision = in.Version
	}
	return o
}

// isUpToDate checks whether desired spec up to date with the observed state for a given release
func isUpToDate(ctx context.Context, kube client.Client, in *v1alpha1.ReleaseParameters, observed *release.Release, s v1alpha1.ReleaseStatus) (bool, error) {
	if observed.Info == nil {
		return false, errors.New(errReleaseInfoNilInObservedRelease)
	}

	if isPending(observed.Info.Status) {
		return false, nil
	}

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
	desiredConfig, err := composeValuesFromSpec(ctx, kube, in.ValuesSpec)
	if err != nil {
		return false, errors.Wrap(err, errFailedToComposeValues)
	}

	if !reflect.DeepEqual(desiredConfig, observed.Config) {
		return false, nil
	}

	changed, err := newPatcher().hasUpdates(ctx, kube, in.PatchesFrom, s)
	if err != nil {
		return false, errors.Wrap(err, errFailedToLoadPatches)
	}

	if changed {
		return false, nil
	}

	return true, nil
}

func isPending(s release.Status) bool {
	return s == release.StatusPendingInstall || s == release.StatusPendingUpgrade || s == release.StatusPendingRollback
}
