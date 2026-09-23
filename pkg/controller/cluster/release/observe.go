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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crossplane/crossplane-runtime/v2/pkg/fieldpath"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"helm.sh/helm/v4/pkg/release/common"
	release "helm.sh/helm/v4/pkg/release/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1beta1"
	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

const (
	errReleaseInfoNilInObservedRelease = "release info is nil in observed helm release"
	errChartNilInObservedRelease       = "chart field is nil in observed helm release"
	errChartMetaNilInObservedRelease   = "chart metadata field is nil in observed helm release"
	errObjectNotPartOfRelease          = "object is not part of release: %v"
	devel                              = ">0.0.0-0"
)

// generateObservation generates release observation for the input release object
func generateObservation(in *release.Release) v1beta1.ReleaseObservation {
	o := v1beta1.ReleaseObservation{}

	relInfo := in.Info
	if relInfo != nil {
		o.State = relInfo.Status
		o.ReleaseDescription = relInfo.Description
		o.Revision = in.Version
	}

	// Store actual deployed chart version for observability
	if in.Chart != nil && in.Chart.Metadata != nil {
		o.Version = in.Chart.Metadata.Version
	}

	return o
}

// rehydrateFromLabels restores deploy-time facts into the observation from
// the release labels, which survive on the external resource itself, unlike
// managed resource status, which crossplane-runtime reverts after a successful
// Create. Releases deployed before label support have none: those keep the
// previously persisted status values.
func rehydrateFromLabels(cr *v1beta1.Release, rel *release.Release, lastDigest string, lastOwnershipTaken bool) {
	// status.atProvider.digest only ever holds a full digest, while the label
	// stores a truncated encoding. The label therefore confirms that the spec
	// digest is what was deployed, which is then surfaced in full; when it
	// does not confirm (drift about to be upgraded, or no label at all) the
	// last persisted digest is the full digest of the last successful deploy,
	// or empty when unknown.
	cr.Status.AtProvider.Digest = lastDigest
	if v, ok := rel.Labels[helmClient.LabelDigestHash]; ok {
		specDigest := helmClient.EffectiveChartDigest(cr.Spec.ForProvider.Chart.URL, cr.Spec.ForProvider.Chart.Digest)
		if v == helmClient.EncodeDigestLabel(specDigest) {
			cr.Status.AtProvider.Digest = specDigest
		}
	}

	if v, ok := rel.Labels[helmClient.LabelOwnershipTaken]; ok {
		cr.Status.AtProvider.OwnershipTaken = v == "true"
	} else {
		cr.Status.AtProvider.OwnershipTaken = lastOwnershipTaken
	}
}

// normalizeConfig JSON-serializes and re-deserializes a config map to
// normalize numeric types (int64 → float64), matching how Helm stores
// release configs. This ensures desired vs observed comparison succeeds
// when the only difference is int64 vs float64 (commonly from strvals parsing).
func normalizeConfig(cfg map[string]interface{}) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return cfg
	}
	var normalized map[string]interface{}
	if err := json.Unmarshal(data, &normalized); err != nil {
		return cfg
	}
	return normalized
}

// isUpToDate checks whether desired spec up to date with the observed state for a given release
func isUpToDate(ctx context.Context, kube client.Client, spec *v1beta1.ReleaseSpec, observed *release.Release, s v1beta1.ReleaseStatus) (bool, error) { // nolint:gocyclo
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

	in := spec.ForProvider

	// In URL mode the chart name is documented-ignored and possibly stale, so
	// comparing it against the deployed chart would loop forever after a URL
	// change to a differently-named chart.
	if in.Chart.URL == "" && in.Chart.Name != ocm.Name {
		return false, nil
	}

	mp := sets.New[xpv2.ManagementAction](spec.ManagementPolicies...)

	if len(mp) != 0 && !mp.HasAny(xpv2.ManagementActionUpdate, xpv2.ManagementActionAll) {
		// Treated as up-to-date as we don't update or create the resource
		return true, nil
	}

	// In URL mode the deployed chart's metadata version is deliberately not
	// compared: an OCI URL tag is an arbitrary string (e.g. :latest, :stable, a
	// v-prefixed tag) that need not equal the chart's Chart.yaml version, so
	// comparing them would report perpetual drift. A tag change is a URL change
	// and is caught by the url-hash label below.
	if in.Chart.URL == "" && versionDrifted(in.Chart.Version, ocm.Version) {
		return false, nil
	}

	// URL drift is detected via the label written at deploy time. Every release
	// this provider deployed carries it, empty when no URL was used, so any
	// difference from the encoded spec URL is drift: the URL changed, a URL was
	// added to a repository-mode release, or the URL was cleared to switch back
	// to repository mode (both encode to "").
	if deployedURL, ok := observed.Labels[helmClient.LabelURLHash]; ok {
		if deployedURL != helmClient.EncodeURLLabel(in.Chart.URL) {
			return false, nil
		}
	} else if in.Chart.URL != "" && versionDrifted(in.Chart.Version, ocm.Version) {
		// Releases deployed before label support carry no URL signal: a URL
		// change on them stays invisible until another field triggers an
		// upgrade, as before. The spec version is still compared for them so
		// that a version bump keeps forcing the upgrade it always did, which
		// is how a repository-mode release gets migrated to a URL. The upgrade
		// writes the label, which takes over from then on, so an OCI tag that
		// differs from the chart version cannot loop here.
		return false, nil
	}

	// Digest drift is detected via the label written at deploy time, which
	// makes the deployed digest observable. Every release this provider
	// deployed carries it, empty when the deploy was not pinned, so any
	// difference from the encoded spec digest is drift: pinned to another
	// digest, pinned where none was, or unpinned. Releases deployed before
	// label support fall back to the digest persisted in status.
	specDigestEnc := helmClient.EncodeDigestLabel(helmClient.EffectiveChartDigest(in.Chart.URL, in.Chart.Digest))
	if deployedDigest, ok := observed.Labels[helmClient.LabelDigestHash]; ok {
		if deployedDigest != specDigestEnc {
			return false, nil
		}
		if specDigestEnc == "" && in.Chart.Digest != "" {
			// The spec pins a digest that does not resolve (it conflicts with
			// the OCI URL's embedded digest, or the URL is malformed). The
			// deploy rejects such specs; report drift so that error surfaces
			// instead of masking the conflict as up-to-date. A URL-embedded
			// digest that does not fit a label is deliberately not covered:
			// the deploy accepts it and records it as unpinned, so reporting
			// drift here would upgrade forever.
			return false, nil
		}
	} else if in.Chart.Digest != "" && s.AtProvider.Digest != "" && in.Chart.Digest != s.AtProvider.Digest {
		return false, nil
	}

	desiredConfig, err := composeValuesFromSpec(ctx, kube, in.ValuesSpec)
	if err != nil {
		return false, errors.Wrap(err, errFailedToComposeValues)
	}

	observedConfig := observed.Config
	if observedConfig == nil {
		// If no config provider, desiredConfig returns as empty map. However, observed would be nil in this case.
		// We know both empty and nil are same.
		observedConfig = make(map[string]interface{})
	}

	desiredNorm := normalizeConfig(desiredConfig)
	observedNorm := normalizeConfig(observedConfig)
	if !equality.Semantic.DeepEqual(desiredNorm, observedNorm) {
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

// versionDrifted reports whether the spec pins a chart version other than the
// deployed one. An empty spec version (digest-only deployments, or not yet
// late-initialized) and the devel range pin nothing.
func versionDrifted(specVersion, deployedVersion string) bool {
	return specVersion != "" && specVersion != deployedVersion && specVersion != devel
}

func isPending(s common.Status) bool {
	return s == common.StatusPendingInstall || s == common.StatusPendingUpgrade || s == common.StatusPendingRollback
}

func connectionDetails(ctx context.Context, kube client.Client, connDetails []v1beta1.ConnectionDetail, relName, relNamespace string) (managed.ConnectionDetails, error) {
	mcd := managed.ConnectionDetails{}

	for _, cd := range connDetails {
		ro := unstructuredFromObjectRef(cd.ObjectReference)
		if err := kube.Get(ctx, types.NamespacedName{Name: ro.GetName(), Namespace: ro.GetNamespace()}, &ro); err != nil {
			return mcd, errors.Wrap(err, "cannot get object")
		}

		if !cd.SkipPartOfReleaseCheck && !partOfRelease(ro, relName, relNamespace) {
			return mcd, errors.Errorf(errObjectNotPartOfRelease, cd.ObjectReference)
		}

		paved := fieldpath.Pave(ro.Object)
		v, err := paved.GetValue(cd.FieldPath)
		if err != nil {
			return mcd, errors.Wrapf(err, "failed to get value at fieldPath: %s", cd.FieldPath)
		}
		s := fmt.Sprintf("%v", v)
		fv := []byte(s)
		// prevent secret data being encoded twice
		if cd.Kind == "Secret" && cd.APIVersion == "v1" && strings.HasPrefix(cd.FieldPath, "data") {
			fv, err = base64.StdEncoding.DecodeString(s)
			if err != nil {
				return mcd, errors.Wrap(err, "failed to decode secret data")
			}
		}

		mcd[cd.ToConnectionSecretKey] = fv
	}

	return mcd, nil
}

func unstructuredFromObjectRef(r corev1.ObjectReference) unstructured.Unstructured {
	u := unstructured.Unstructured{}
	u.SetAPIVersion(r.APIVersion)
	u.SetKind(r.Kind)
	u.SetName(r.Name)
	u.SetNamespace(r.Namespace)

	return u
}

func partOfRelease(u unstructured.Unstructured, relName, relNamespace string) bool {
	a := u.GetAnnotations()
	return a[helmReleaseNameAnnotation] == relName && a[helmReleaseNamespaceAnnotation] == relNamespace
}
