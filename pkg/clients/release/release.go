package release

import (
	"github.com/crossplane-contrib/provider-helm/apis/v1alpha1"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/release"
)

const (
	errChartNilInObservedRelease     = "chart field is nil in observed helm release"
	errChartMetaNilInObservedRelease = "chart metadata field is nil in observed helm release"
)

func GenerateObservation(in *release.Release) v1alpha1.ReleaseObservation {
	o := v1alpha1.ReleaseObservation{}

	relInfo := in.Info
	if relInfo != nil {
		o.Status = relInfo.Status
		o.ReleaseDescription = relInfo.Description
	}
	return o
}

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

	return true, nil
}
