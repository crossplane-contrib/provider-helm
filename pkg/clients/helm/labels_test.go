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

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
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
				"name":              "my-release",
				"owner":             "helm",
				"status":            "deployed",
				"version":           "3",
				"createdAt":         "1764000000",
				"modifiedAt":        "1764000000",
				LabelDigestHash:     "",
				LabelURLHash:        "",
				LabelOwnershipTaken: "true",
				"team":              "platform",
				"example.com/tier":  "backend",
			},
			want: map[string]string{
				"team":             "platform",
				"example.com/tier": "backend",
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
				desired:  map[string]string{LabelURLHash: "", "team": "platform"},
			},
			want: map[string]string{LabelURLHash: "", "team": "platform"},
		},
		"RemovedUserLabelSentAsNull": {
			args: args{
				previous: map[string]string{"team": "platform", "tier": "backend"},
				desired:  map[string]string{"team": "platform"},
			},
			want: map[string]string{"team": "platform", "tier": labelValueDelete},
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
				previous: map[string]string{"team": "platform"},
			},
			want: map[string]string{"team": labelValueDelete},
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
// revision that the upgrade doesn't override.
func TestUpgradeReplacesUserLabels(t *testing.T) {
	chrt := &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: chart.APIVersionV2, Name: "labels", Version: "0.1.0"},
		Templates: []*common.File{{
			Name: "templates/configmap.yaml",
			Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\n"),
		}},
	}
	installed := map[string]string{LabelURLHash: "", LabelOwnershipTaken: "true", "team": "platform", "tier": "backend"}

	cases := map[string]struct {
		desired map[string]string
		want    map[string]string
	}{
		"UserLabelRemovedAndChanged": {
			desired: map[string]string{LabelURLHash: "", "team": "infra"},
			want:    map[string]string{LabelURLHash: "", LabelOwnershipTaken: "true", "team": "infra"},
		},
		"AllUserLabelsRemoved": {
			desired: map[string]string{LabelURLHash: ""},
			want:    map[string]string{LabelURLHash: "", LabelOwnershipTaken: "true"},
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
