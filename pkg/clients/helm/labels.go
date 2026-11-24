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

package helm

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"strings"

	"helm.sh/helm/v4/pkg/registry"
	"helm.sh/helm/v4/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Custom release labels written on every install and upgrade. Helm persists
// them as labels on the release Secret, which makes deploy-time facts
// observable again on later reconciles: unlike managed resource status, which
// crossplane-runtime resets after a successful Create, and unlike the spec,
// they live on the external resource itself.
const (
	// LabelPrefix is the key prefix of the labels the provider writes itself.
	// spec.forProvider.labels can't use it.
	LabelPrefix = "release.helm.crossplane.io/"

	// LabelDigestHash carries the encoded chart digest the release was
	// deployed from. Empty when the deploy was not digest-pinned, so that a
	// digest pinned later is detected as drift; absent only when the release
	// predates label support.
	LabelDigestHash = "release.helm.crossplane.io/digest-hash"
	// LabelURLHash carries the encoded chart URL the release was deployed
	// from. Empty when the deploy did not use spec.forProvider.chart.url, so
	// that a URL set later is detected as drift; absent only when the release
	// predates label support.
	LabelURLHash = "release.helm.crossplane.io/url-hash"
	// LabelOwnershipTaken records that takeOwnership was exercised for this
	// release. It is sticky: once written it is never removed, so the fact
	// that the release was adopted stays observable even after
	// spec.forProvider.takeOwnership is unset. It is also what
	// deployOptions' one-shot adoption gate reads on later reconciles,
	// where the runtime may have reverted the persisted status - adoption
	// never takes ownership twice, while the Observe-side drift check
	// (see OwnershipDrifted) is what re-stamps metadata after that.
	LabelOwnershipTaken = "release.helm.crossplane.io/ownership-taken"

	// labelValueDelete is the label value Helm's upgrade treats as "remove
	// this key" when it merges the given labels over the previous revision's.
	labelValueDelete = "null"

	// labelHashHexLen is how many hex characters of a hash fit in a label
	// value next to a "sha256-" style algorithm prefix: Kubernetes caps label
	// values at 63 characters and a full sha256 hex is 64, one over even
	// without a prefix.
	labelHashHexLen = 56
)

// asLabelValue returns v when it is a valid Kubernetes label value, otherwise
// "". The encoders guarantee 63-char, valid-charset output for the digests and
// URLs seen in practice; this is a final guard so that an unexpected input
// (e.g. a digest with an algorithm prefix longer than "sha256") is written as
// the empty value, i.e. recorded as not digest-pinned, rather than producing a
// value the API server rejects at deploy. Only a digest embedded in an OCI URL
// can reach it, as the spec digest is pattern-validated; the URL label still
// detects any change to such a URL.
func asLabelValue(v string) string {
	if len(validation.IsValidLabelValue(v)) != 0 {
		return ""
	}
	return v
}

// EncodeDigestLabel converts a chart digest ("sha256:<64 hex>") into a valid
// Kubernetes label value of the form "sha256-<first 56 hex>". The truncation
// is imposed by the 63-character label value limit; the encoded value is used
// for equality-based drift detection only, while pull-time verification always
// uses the full digest. Returns "" for an empty or malformed digest, or one
// whose algorithm prefix does not fit a label value (see asLabelValue).
func EncodeDigestLabel(digest string) string {
	algo, hash, found := strings.Cut(digest, ":")
	if !found || algo == "" || hash == "" {
		return ""
	}
	if len(hash) > labelHashHexLen {
		hash = hash[:labelHashHexLen]
	}
	return asLabelValue(algo + "-" + hash)
}

// EncodeURLLabel hashes a chart URL into a label value of the same shape as
// EncodeDigestLabel: "sha256-<first 56 hex of sha256(url)>". URLs are hashed
// rather than truncated since they are arbitrary strings, and the readable
// value already lives in the spec. Returns "" for an empty URL.
func EncodeURLLabel(url string) string {
	if url == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(url))
	return asLabelValue("sha256-" + hex.EncodeToString(sum[:])[:labelHashHexLen])
}

// EffectiveChartDigest returns the digest a deploy of the given chart source
// would be pinned to: the digest embedded in an OCI URL wins, otherwise the
// spec digest. Returns "" when the two conflict, the URL cannot be parsed, or
// a spec digest accompanies a non-OCI URL: the deploy itself rejects those
// specs before any release is written, so no label value applies.
func EffectiveChartDigest(chartURL, specDigest string) string {
	if chartURL == "" {
		return specDigest
	}
	if !registry.IsOCI(chartURL) {
		return ""
	}
	_, _, urlDigest, err := resolveOCIChartVersionAndDigest(chartURL)
	if err != nil {
		return ""
	}
	d, err := resolveEffectiveDigest(urlDigest, specDigest)
	if err != nil {
		return ""
	}
	return d
}

// UserLabels returns the labels of a release that come from
// spec.forProvider.labels: all of them except Helm's system labels, which
// release storage adds to every revision, and the provider's own under
// LabelPrefix.
func UserLabels(labels map[string]string) map[string]string {
	user := map[string]string{}
	for k, v := range labels {
		if slices.Contains(driver.GetSystemLabels(), k) || strings.HasPrefix(k, LabelPrefix) {
			continue
		}
		user[k] = v
	}
	return user
}

// labelsForUpgrade returns the labels to give Helm's upgrade so that the new
// revision carries exactly the desired user labels. The upgrade merges the
// labels it gets over the previous revision's and deletes only keys whose
// value is "null", so every user label of the previous revision that isn't
// desired is sent as "null". The provider's own labels are never removed this
// way: the ownership label is sticky by design.
func labelsForUpgrade(previous, desired map[string]string) map[string]string {
	labels := maps.Clone(desired)
	if labels == nil {
		labels = map[string]string{}
	}
	for k := range UserLabels(previous) {
		if _, ok := desired[k]; !ok {
			labels[k] = labelValueDelete
		}
	}
	return labels
}
