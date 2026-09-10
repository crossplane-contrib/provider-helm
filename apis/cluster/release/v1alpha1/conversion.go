/*
Copyright 2026 The Crossplane Authors.

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

package v1alpha1

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"

	"github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1beta1"
)

// ConversionDataAnnotationKey stashes the v1beta1 fields that have no
// v1alpha1 equivalent, so that a write made through the deprecated v1alpha1
// API doesn't silently erase them. ConvertFrom sets it; ConvertTo consumes
// and strips it.
const ConversionDataAnnotationKey = "helm.crossplane.io/release-conversion-data"

// conversionData holds the v1beta1 Release fields that v1alpha1 has no way
// to represent.
type conversionData struct {
	ChartURL              string                     `json:"chartURL,omitempty"`
	ChartDigest           string                     `json:"chartDigest,omitempty"`
	SkipCreateNamespace   bool                       `json:"skipCreateNamespace,omitempty"`
	WaitTimeout           *metav1.Duration           `json:"waitTimeout,omitempty"`
	SkipCRDs              bool                       `json:"skipCRDs,omitempty"`
	InsecureSkipTLSVerify bool                       `json:"insecureSkipTLSVerify,omitempty"`
	PlainHTTP             bool                       `json:"plainHTTP,omitempty"`
	CABundle              *v1beta1.ValueFromSource   `json:"caBundle,omitempty"`
	TakeOwnership         bool                       `json:"takeOwnership,omitempty"`
	MaxHistory            int                        `json:"maxHistory,omitempty"`
	SSAForceConflicts     bool                       `json:"ssaForceConflicts,omitempty"`
	ConnectionDetails     []v1beta1.ConnectionDetail `json:"connectionDetails,omitempty"`
	ObservedDigest        string                     `json:"observedDigest,omitempty"`
	ObservedVersion       string                     `json:"observedVersion,omitempty"`
	OwnershipTaken        bool                       `json:"ownershipTaken,omitempty"`
}

// ConvertTo converts this Release to the Hub version (v1beta1). Fields that
// exist in both versions are copied via an unstructured round-trip instead
// of a hand-maintained field-by-field mapping. v1beta1-only fields are then
// restored from the ConversionDataAnnotationKey annotation, if ConvertFrom
// previously stashed one there (e.g. this object was read, and is now being
// written back, through the deprecated v1alpha1 API) — without this, that
// round trip would silently reset them to their zero value.
func (src *Release) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*v1beta1.Release)
	gvk := dst.GetObjectKind().GroupVersionKind()

	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(src)
	if err != nil {
		return errors.Wrap(err, "cannot convert Release v1alpha1 to unstructured")
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u, dst); err != nil {
		return errors.Wrap(err, "cannot convert unstructured to Release v1beta1")
	}
	dst.GetObjectKind().SetGroupVersionKind(gvk)

	raw, ok := dst.Annotations[ConversionDataAnnotationKey]
	if !ok {
		return nil
	}
	delete(dst.Annotations, ConversionDataAnnotationKey)

	cd := conversionData{}
	if err := json.Unmarshal([]byte(raw), &cd); err != nil {
		return errors.Wrap(err, "cannot unmarshal Release conversion data annotation")
	}

	dst.Spec.ForProvider.Chart.URL = cd.ChartURL
	dst.Spec.ForProvider.Chart.Digest = cd.ChartDigest
	dst.Spec.ForProvider.SkipCreateNamespace = cd.SkipCreateNamespace
	dst.Spec.ForProvider.WaitTimeout = cd.WaitTimeout
	dst.Spec.ForProvider.SkipCRDs = cd.SkipCRDs
	dst.Spec.ForProvider.InsecureSkipTLSVerify = cd.InsecureSkipTLSVerify
	dst.Spec.ForProvider.PlainHTTP = cd.PlainHTTP
	dst.Spec.ForProvider.CABundle = cd.CABundle
	dst.Spec.ForProvider.TakeOwnership = cd.TakeOwnership
	dst.Spec.ForProvider.MaxHistory = cd.MaxHistory
	dst.Spec.ForProvider.SSAForceConflicts = cd.SSAForceConflicts
	dst.Spec.ConnectionDetails = cd.ConnectionDetails
	dst.Status.AtProvider.Digest = cd.ObservedDigest
	dst.Status.AtProvider.Version = cd.ObservedVersion
	dst.Status.AtProvider.OwnershipTaken = cd.OwnershipTaken

	return nil
}

// ConvertFrom converts from the Hub version (v1beta1) to this version, via
// the same unstructured round-trip as ConvertTo. Fields v1alpha1 has no
// equivalent for (e.g. chart URL/digest, SSAForceConflicts, ConnectionDetails)
// are stashed in the ConversionDataAnnotationKey annotation instead of being
// silently dropped, so that ConvertTo can restore them if this object is
// later written back through v1alpha1.
func (dst *Release) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*v1beta1.Release)
	gvk := dst.GetObjectKind().GroupVersionKind()

	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(src)
	if err != nil {
		return errors.Wrap(err, "cannot convert Release v1beta1 to unstructured")
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u, dst); err != nil {
		return errors.Wrap(err, "cannot convert unstructured to Release v1alpha1")
	}
	dst.GetObjectKind().SetGroupVersionKind(gvk)

	cd := conversionData{
		ChartURL:              src.Spec.ForProvider.Chart.URL,
		ChartDigest:           src.Spec.ForProvider.Chart.Digest,
		SkipCreateNamespace:   src.Spec.ForProvider.SkipCreateNamespace,
		WaitTimeout:           src.Spec.ForProvider.WaitTimeout,
		SkipCRDs:              src.Spec.ForProvider.SkipCRDs,
		InsecureSkipTLSVerify: src.Spec.ForProvider.InsecureSkipTLSVerify,
		PlainHTTP:             src.Spec.ForProvider.PlainHTTP,
		CABundle:              src.Spec.ForProvider.CABundle,
		TakeOwnership:         src.Spec.ForProvider.TakeOwnership,
		MaxHistory:            src.Spec.ForProvider.MaxHistory,
		SSAForceConflicts:     src.Spec.ForProvider.SSAForceConflicts,
		ConnectionDetails:     src.Spec.ConnectionDetails,
		ObservedDigest:        src.Status.AtProvider.Digest,
		ObservedVersion:       src.Status.AtProvider.Version,
		OwnershipTaken:        src.Status.AtProvider.OwnershipTaken,
	}

	raw, err := json.Marshal(cd)
	if err != nil {
		return errors.Wrap(err, "cannot marshal Release conversion data annotation")
	}
	// Every field is omitempty, so "{}" means there's nothing v1alpha1-only
	// to preserve — leave the object without the annotation instead of
	// churning it on every object that never used a v1beta1-only field.
	if string(raw) == "{}" {
		return nil
	}
	if dst.Annotations == nil {
		dst.Annotations = map[string]string{}
	}
	dst.Annotations[ConversionDataAnnotationKey] = string(raw)

	return nil
}
