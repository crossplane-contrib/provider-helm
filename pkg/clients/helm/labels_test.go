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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	testDigestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testDigestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestEncodeDigestLabel(t *testing.T) {
	cases := map[string]struct {
		digest string
		want   string
	}{
		"Empty": {
			digest: "",
			want:   "",
		},
		"MalformedNoAlgorithm": {
			digest: "aaaaaaaa",
			want:   "",
		},
		"FullSha256TruncatedTo63": {
			digest: testDigestA,
			want:   "sha256-" + strings.Repeat("a", 56),
		},
		"ShortHashKeptWhole": {
			digest: "sha256:abc123",
			want:   "sha256-abc123",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := EncodeDigestLabel(tc.digest)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("EncodeDigestLabel() -want, +got:\n%s", diff)
			}
			if errs := validation.IsValidLabelValue(got); len(errs) != 0 {
				t.Errorf("EncodeDigestLabel() = %q is not a valid label value: %v", got, errs)
			}
		})
	}
}

func TestEncodeURLLabel(t *testing.T) {
	const url = "oci://registry.example.com/charts/mychart:1.2.3"

	if got := EncodeURLLabel(""); got != "" {
		t.Errorf("EncodeURLLabel(\"\") = %q, want \"\"", got)
	}

	got := EncodeURLLabel(url)
	if got != EncodeURLLabel(url) {
		t.Errorf("EncodeURLLabel() is not deterministic")
	}
	if got == EncodeURLLabel(url+"x") {
		t.Errorf("EncodeURLLabel() collides for different URLs")
	}
	if !strings.HasPrefix(got, "sha256-") {
		t.Errorf("EncodeURLLabel() = %q, want sha256- prefix", got)
	}
	if len(got) != len("sha256-")+labelHashHexLen {
		t.Errorf("EncodeURLLabel() length = %d, want %d", len(got), len("sha256-")+labelHashHexLen)
	}
	if errs := validation.IsValidLabelValue(got); len(errs) != 0 {
		t.Errorf("EncodeURLLabel() = %q is not a valid label value: %v", got, errs)
	}
}

func TestEffectiveChartDigest(t *testing.T) {
	cases := map[string]struct {
		chartURL   string
		specDigest string
		want       string
	}{
		"NoURLUsesSpec": {
			specDigest: testDigestA,
			want:       testDigestA,
		},
		"NonOCIURLUsesSpec": {
			chartURL:   "https://charts.example.com/mychart-1.2.3.tgz",
			specDigest: testDigestA,
			want:       testDigestA,
		},
		"OCIURLDigestWins": {
			chartURL: "oci://registry.example.com/charts/mychart@" + testDigestA,
			want:     testDigestA,
		},
		"OCIURLDigestMatchesSpec": {
			chartURL:   "oci://registry.example.com/charts/mychart@" + testDigestA,
			specDigest: testDigestA,
			want:       testDigestA,
		},
		"ConflictYieldsEmpty": {
			// The deploy rejects this spec before any release is written, so
			// no label value applies.
			chartURL:   "oci://registry.example.com/charts/mychart@" + testDigestA,
			specDigest: testDigestB,
			want:       "",
		},
		"BareOCIURLUsesSpec": {
			chartURL:   "oci://registry.example.com/charts/mychart",
			specDigest: testDigestA,
			want:       testDigestA,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := EffectiveChartDigest(tc.chartURL, tc.specDigest)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("EffectiveChartDigest() -want, +got:\n%s", diff)
			}
		})
	}
}

func TestWithoutDeletedLabels(t *testing.T) {
	cases := map[string]struct {
		labels map[string]string
		want   map[string]string
	}{
		"Nil": {
			labels: nil,
			want:   nil,
		},
		"AllDeleted": {
			labels: map[string]string{LabelDigestHash: LabelValueDelete},
			want:   nil,
		},
		"Mixed": {
			labels: map[string]string{
				LabelDigestHash:     "sha256-" + strings.Repeat("a", 56),
				LabelURLHash:        LabelValueDelete,
				LabelOwnershipTaken: "true",
			},
			want: map[string]string{
				LabelDigestHash:     "sha256-" + strings.Repeat("a", 56),
				LabelOwnershipTaken: "true",
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := withoutDeletedLabels(tc.labels)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("withoutDeletedLabels() -want, +got:\n%s", diff)
			}
		})
	}
}
