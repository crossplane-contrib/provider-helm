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
	"io"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	"github.com/google/go-cmp/cmp"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
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
		"NonOCIURLYieldsEmpty": {
			// A digest cannot apply to a non-OCI URL and the deploy rejects
			// the spec, so no label value applies.
			chartURL:   "https://charts.example.com/mychart-1.2.3.tgz",
			specDigest: testDigestA,
			want:       "",
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

func TestValidateUserLabels(t *testing.T) {
	syntax := func(prefix string, msgs []string) string {
		return prefix + ": " + strings.Join(msgs, "; "+prefix+": ")
	}
	longValue := strings.Repeat("a", 64)

	cases := map[string]struct {
		labels map[string]string
		want   error
	}{
		"Nil": {},
		"Valid": {
			labels: map[string]string{"team": "platform", "example.com/tier": "backend", "empty": ""},
		},
		"ReservedName": {
			labels: map[string]string{"owner": "me"},
			want:   errors.New(`"owner" is reserved by Helm`),
		},
		"ProviderPrefix": {
			labels: map[string]string{LabelOwnershipTaken: "true"},
			want:   errors.New(`"release.helm.crossplane.io/ownership-taken" is under the provider's prefix release.helm.crossplane.io/`),
		},
		"NullValue": {
			labels: map[string]string{"team": "null"},
			want:   errors.New(`value of "team" is "null", which Helm treats as a deletion`),
		},
		"InvalidKey": {
			labels: map[string]string{"my team": "platform"},
			want:   errors.New(syntax(`key "my team"`, validation.IsQualifiedName("my team"))),
		},
		"InvalidValue": {
			labels: map[string]string{"team": longValue},
			want:   errors.New(syntax(`value of "team"`, validation.IsValidLabelValue(longValue))),
		},
		"EveryProblemInKeyOrder": {
			labels: map[string]string{"team": "null", "owner": "me"},
			want:   errors.New(`"owner" is reserved by Helm; value of "team" is "null", which Helm treats as a deletion`),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, ValidateUserLabels(tc.labels), test.EquateErrors()); diff != "" {
				t.Errorf("ValidateUserLabels(...): -want, +got:\n%s", diff)
			}
		})
	}
}

func TestUserLabelMarker(t *testing.T) {
	got := UserLabelMarker("example.com/tier")
	if got != UserLabelMarker("example.com/tier") {
		t.Errorf("UserLabelMarker() is not deterministic")
	}
	if got == UserLabelMarker("example.com/tiers") {
		t.Errorf("UserLabelMarker() collides for different keys")
	}
	if !strings.HasPrefix(got, userLabelMarkerPrefix) {
		t.Errorf("UserLabelMarker() = %q, want prefix %q", got, userLabelMarkerPrefix)
	}
	if errs := validation.IsQualifiedName(got); len(errs) != 0 {
		t.Errorf("UserLabelMarker() = %q is not a valid label key: %v", got, errs)
	}
}

func TestMarkUserLabels(t *testing.T) {
	cases := map[string]struct {
		user map[string]string
		want map[string]string
	}{
		"Nil": {
			want: map[string]string{},
		},
		"EachLabelMarked": {
			user: map[string]string{"team": "platform", "example.com/tier": "backend"},
			want: map[string]string{
				"team":                              "platform",
				UserLabelMarker("team"):             "",
				"example.com/tier":                  "backend",
				UserLabelMarker("example.com/tier"): "",
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, MarkUserLabels(tc.user)); diff != "" {
				t.Errorf("MarkUserLabels(...): -want, +got:\n%s", diff)
			}
		})
	}
}

func TestUserLabels(t *testing.T) {
	cases := map[string]struct {
		labels map[string]string
		want   map[string]string
	}{
		"Nil": {
			want: map[string]string{},
		},
		"SystemAndProviderLabelsDropped": {
			labels: map[string]string{
				"name":                              "my-release",
				"owner":                             "helm",
				"status":                            "deployed",
				"version":                           "3",
				"createdAt":                         "1764000000",
				"modifiedAt":                        "1764000000",
				LabelDigestHash:                     "",
				LabelURLHash:                        "",
				LabelOwnershipTaken:                 "true",
				"team":                              "platform",
				UserLabelMarker("team"):             "",
				"example.com/tier":                  "backend",
				UserLabelMarker("example.com/tier"): "",
			},
			want: map[string]string{
				"team":             "platform",
				"example.com/tier": "backend",
			},
		},
		"LabelsWithoutMarkerLeftOut": {
			// Labels an admission policy, kubectl label or helm --labels put on
			// the release Secret, and a marker whose label is gone.
			labels: map[string]string{
				"team":                     "platform",
				UserLabelMarker("team"):    "",
				"policy.example.com/scan":  "passed",
				"cost-center":              "1234",
				UserLabelMarker("removed"): "",
			},
			want: map[string]string{
				"team": "platform",
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, UserLabels(tc.labels)); diff != "" {
				t.Errorf("UserLabels(...): -want, +got:\n%s", diff)
			}
		})
	}
}

func TestLabelsForUpgrade(t *testing.T) {
	type args struct {
		previous map[string]string
		desired  map[string]string
	}
	cases := map[string]struct {
		args args
		want map[string]string
	}{
		"NoPreviousUserLabels": {
			args: args{
				previous: map[string]string{LabelURLHash: ""},
				desired:  map[string]string{LabelURLHash: "", "team": "platform", UserLabelMarker("team"): ""},
			},
			want: map[string]string{LabelURLHash: "", "team": "platform", UserLabelMarker("team"): ""},
		},
		"RemovedUserLabelSentAsNull": {
			args: args{
				previous: map[string]string{
					"team":                  "platform",
					UserLabelMarker("team"): "",
					"tier":                  "backend",
					UserLabelMarker("tier"): "",
				},
				desired: map[string]string{"team": "platform", UserLabelMarker("team"): ""},
			},
			want: map[string]string{
				"team":                  "platform",
				UserLabelMarker("team"): "",
				"tier":                  labelValueDelete,
				UserLabelMarker("tier"): labelValueDelete,
			},
		},
		"LabelsWithoutMarkerKept": {
			// Helm keeps the previous revision's labels it isn't told to delete.
			args: args{
				previous: map[string]string{"policy.example.com/scan": "passed", "cost-center": "1234"},
				desired:  map[string]string{LabelURLHash: ""},
			},
			want: map[string]string{LabelURLHash: ""},
		},
		"MarkerWithoutLabelSentAsNull": {
			args: args{
				previous: map[string]string{UserLabelMarker("removed"): ""},
				desired:  map[string]string{LabelURLHash: ""},
			},
			want: map[string]string{LabelURLHash: "", UserLabelMarker("removed"): labelValueDelete},
		},
		"ProviderAndSystemLabelsNeverRemoved": {
			// The ownership label is sticky, and Helm sets its system labels
			// on every revision itself.
			args: args{
				previous: map[string]string{
					LabelOwnershipTaken: "true",
					"owner":             "helm",
					"status":            "deployed",
				},
				desired: map[string]string{LabelURLHash: ""},
			},
			want: map[string]string{LabelURLHash: ""},
		},
		"NilDesired": {
			args: args{
				previous: map[string]string{"team": "platform", UserLabelMarker("team"): ""},
			},
			want: map[string]string{"team": labelValueDelete, UserLabelMarker("team"): labelValueDelete},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, labelsForUpgrade(tc.args.previous, tc.args.desired)); diff != "" {
				t.Errorf("labelsForUpgrade(...): -want, +got:\n%s", diff)
			}
		})
	}
}

// TestUpgradeReplacesUserLabels installs and upgrades a release through real
// Helm actions on in-memory release storage, and checks the labels the new
// revision carries: Helm itself would keep every label of the previous
// revision that the upgrade doesn't override, which is what keeps labels set
// outside the provider.
func TestUpgradeReplacesUserLabels(t *testing.T) {
	chrt := &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: chart.APIVersionV2, Name: "labels", Version: "0.1.0"},
		Templates: []*common.File{{
			Name: "templates/configmap.yaml",
			Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\n"),
		}},
	}
	// policy.example.com/scan stands for a label set on the release outside the
	// provider, which upgrades must keep.
	installed := map[string]string{
		LabelURLHash:              "",
		LabelOwnershipTaken:       "true",
		"team":                    "platform",
		UserLabelMarker("team"):   "",
		"tier":                    "backend",
		UserLabelMarker("tier"):   "",
		"policy.example.com/scan": "passed",
	}

	cases := map[string]struct {
		desired map[string]string
		want    map[string]string
	}{
		"UserLabelRemovedAndChanged": {
			desired: map[string]string{LabelURLHash: "", "team": "infra", UserLabelMarker("team"): ""},
			want: map[string]string{
				LabelURLHash:              "",
				LabelOwnershipTaken:       "true",
				"team":                    "infra",
				UserLabelMarker("team"):   "",
				"policy.example.com/scan": "passed",
			},
		},
		"AllUserLabelsRemoved": {
			desired: map[string]string{LabelURLHash: ""},
			want:    map[string]string{LabelURLHash: "", LabelOwnershipTaken: "true", "policy.example.com/scan": "passed"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := &action.Configuration{
				Releases:     storage.Init(driver.NewMemory()),
				KubeClient:   &kubefake.PrintingKubeClient{Out: io.Discard},
				Capabilities: common.DefaultCapabilities,
			}
			install := action.NewInstall(cfg)
			install.Namespace = "default"
			hc := &client{
				log:           logging.NewNopLogger(),
				getClient:     action.NewGet(cfg),
				installClient: install,
				upgradeClient: action.NewUpgrade(cfg),
			}

			if _, err := hc.Install("labels", chrt, nil, nil, DeployOptions{Labels: installed}); err != nil {
				t.Fatalf("Install(...): unexpected error: %v", err)
			}
			rel, err := hc.Upgrade("labels", chrt, nil, nil, DeployOptions{Labels: tc.desired})
			if err != nil {
				t.Fatalf("Upgrade(...): unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, rel.Labels); diff != "" {
				t.Errorf("Upgrade(...) release labels: -want, +got:\n%s", diff)
			}
		})
	}
}
