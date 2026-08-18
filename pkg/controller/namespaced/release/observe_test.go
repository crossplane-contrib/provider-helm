package release

import (
	"context"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/google/go-cmp/cmp"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/release/common"
	release "helm.sh/helm/v4/pkg/release/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-helm/apis/namespaced/release/v1beta1"
	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

const (
	testDescription = "test description"
)

var (
	testReleaseConfigStr = `
keyA: valA
keyB:
  subKeyA: subValA
`
	testReleaseConfig = map[string]interface{}{
		"keyA": "valA",
		"keyB": map[string]interface{}{
			"subKeyA": "subValA",
		},
	}
)

func Test_generateObservation(t *testing.T) {
	type args struct {
		in *release.Release
	}
	type want struct {
		out v1beta1.ReleaseObservation
	}
	cases := map[string]struct {
		args
		want
	}{
		"ReleaseInfoNil": {
			args: args{
				in: &release.Release{
					Name: "",
					Info: nil,
				},
			},
			want: want{
				out: v1beta1.ReleaseObservation{
					State:              "",
					ReleaseDescription: "",
				},
			},
		},
		"Success": {
			args: args{
				in: &release.Release{
					Name: "",
					Info: &release.Info{
						Description: testDescription,
						Status:      common.StatusDeployed,
					},
				},
			},
			want: want{
				out: v1beta1.ReleaseObservation{
					State:              common.StatusDeployed,
					ReleaseDescription: testDescription,
				},
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := generateObservation(tc.args.in)
			if diff := cmp.Diff(tc.want.out, got); diff != "" {
				t.Errorf("generateObservation(...): -want result, +got result: %s", diff)
			}
		})
	}
}

func Test_isUpToDate(t *testing.T) {
	type args struct {
		kube     client.Client
		spec     *v1beta1.ReleaseSpec
		observed *release.Release
		status   v1beta1.ReleaseStatus
	}
	type want struct {
		out bool
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"InfoNilInObserved": {
			args: args{
				observed: &release.Release{
					Info: nil,
				},
			},
			want: want{
				out: false,
				err: errors.New(errReleaseInfoNilInObservedRelease),
			},
		},
		"PendingReturnsNotUpToDate": {
			args: args{
				observed: &release.Release{
					Info: &release.Info{
						Status: common.StatusPendingUpgrade,
					},
					Chart:  nil,
					Config: testReleaseConfig,
				},
			},
			want: want{
				out: false,
			},
		},
		"ChartNilInObserved": {
			args: args{
				observed: &release.Release{
					Info:   &release.Info{},
					Chart:  nil,
					Config: testReleaseConfig,
				},
			},
			want: want{
				out: false,
				err: errors.New(errChartNilInObservedRelease),
			},
		},
		"ChartMetaNilInObserved": {
			args: args{
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw:      nil,
						Metadata: nil,
					},
					Config: testReleaseConfig,
				},
			},
			want: want{
				out: false,
				err: errors.New(errChartMetaNilInObservedRelease),
			},
		},
		"FailedToComposeValues": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw:    []byte("invalid-yaml"),
								Object: nil,
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
				},
			},
			want: want{
				out: false,
				err: errors.Wrap(
					errors.Wrap(errors.New("error unmarshaling JSON: while decoding JSON: "+
						"json: cannot unmarshal string into Go value of type map[string]interface {}"),
						errFailedToUnmarshalDesiredValues),
					errFailedToComposeValues),
			},
		},
		"NotUpToDate_ChartNameDifferent": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    "another-chart",
							Version: testVersion,
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
				},
			},
			want: want{
				out: false,
				err: nil,
			},
		},
		"NotUpToDate_ChartVersionDifferent": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: "another-version",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
				},
			},
			want: want{
				out: false,
				err: nil,
			},
		},
		"NotUpToDate_ConfigIsDifferent": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte("keyA: valX"),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
				},
			},
			want: want{
				out: false,
				err: nil,
			},
		},
		"NotUpToDate_ConfigIsDifferent_ManagementPolicies_DoesApply": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ManagedResourceSpec: xpv2.ManagedResourceSpec{
						ManagementPolicies: []xpv2.ManagementAction{
							xpv2.ManagementActionCreate,
							xpv2.ManagementActionDelete,
							xpv2.ManagementActionObserve,
						},
					},
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte("keyA: valX"),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
				},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"NotUpToDate_ConfigIsDifferent_ManagementPolicies_DoesNotApply": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ManagedResourceSpec: xpv2.ManagedResourceSpec{
						ManagementPolicies: []xpv2.ManagementAction{
							xpv2.ManagementActionCreate,
							xpv2.ManagementActionDelete,
							xpv2.ManagementActionObserve,
							xpv2.ManagementActionUpdate,
						},
					},
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte("keyA: valX"),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
				},
			},
			want: want{
				out: false,
				err: nil,
			},
		},
		"NotUpToDate_DigestChanged": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
							Digest:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
				},
				status: v1beta1.ReleaseStatus{
					AtProvider: v1beta1.ReleaseObservation{
						Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					},
				},
			},
			want: want{
				out: false,
				err: nil,
			},
		},
		"UpToDate_DigestMatchesLastDeployed": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
							Digest:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
				},
				status: v1beta1.ReleaseStatus{
					AtProvider: v1beta1.ReleaseObservation{
						Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					},
				},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"UpToDate_DigestSpecifiedButNotYetDeployed": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
							Digest:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
				},
				status: v1beta1.ReleaseStatus{
					AtProvider: v1beta1.ReleaseObservation{
						Digest: "",
					},
				},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"UpToDate_DigestOnly_VersionEmpty": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:   testChart,
							Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: "1.2.3",
						},
					},
					Config: testReleaseConfig,
				},
				status: v1beta1.ReleaseStatus{
					AtProvider: v1beta1.ReleaseObservation{
						Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					},
				},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"NotUpToDate_DigestLabelDrift": {
			// The digest label written at deploy time records a different
			// digest than the spec asks for; the stale status digest matching
			// the spec must not mask the drift.
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
							Digest:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
					Labels: map[string]string{
						helmClient.LabelDigestHash: helmClient.EncodeDigestLabel("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
					},
				},
				status: v1beta1.ReleaseStatus{
					AtProvider: v1beta1.ReleaseObservation{
						Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					},
				},
			},
			want: want{
				out: false,
				err: nil,
			},
		},
		"UpToDate_DigestLabelMatches": {
			// The label records the deployed digest; an empty status digest
			// (e.g. wiped after Create) must not matter.
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
							Digest:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
					Labels: map[string]string{
						helmClient.LabelDigestHash: helmClient.EncodeDigestLabel("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
					},
				},
				status: v1beta1.ReleaseStatus{},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"NotUpToDate_URLChangedViaLabel": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							URL: "https://charts.example.com/mychart-2.0.0.tgz",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
					Labels: map[string]string{
						helmClient.LabelURLHash: helmClient.EncodeURLLabel("https://charts.example.com/mychart-1.0.0.tgz"),
					},
				},
				status: v1beta1.ReleaseStatus{},
			},
			want: want{
				out: false,
				err: nil,
			},
		},
		"UpToDate_URLUnchangedViaLabel": {
			// A stale late-initialized name must not matter in URL mode.
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							URL:  "https://charts.example.com/mychart-1.0.0.tgz",
							Name: "stale-name",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
					Labels: map[string]string{
						helmClient.LabelURLHash: helmClient.EncodeURLLabel("https://charts.example.com/mychart-1.0.0.tgz"),
					},
				},
				status: v1beta1.ReleaseStatus{},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"UpToDate_LegacyURLReleaseWithoutLabel": {
			// Releases deployed before label support cannot detect URL
			// changes; the change stays invisible, as before.
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							URL: "https://charts.example.com/mychart-2.0.0.tgz",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
				},
				status: v1beta1.ReleaseStatus{},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"UpToDate_NonOCIURLIgnoresSpecVersion": {
			// Version alongside an HTTPS URL is documented-ignored and must
			// not cause a perpetual upgrade loop.
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							URL:     "https://charts.example.com/mychart-1.0.0.tgz",
							Version: "9.9.9",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
					Labels: map[string]string{
						helmClient.LabelURLHash: helmClient.EncodeURLLabel("https://charts.example.com/mychart-1.0.0.tgz"),
					},
				},
				status: v1beta1.ReleaseStatus{},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"UpToDate_OCIURLVersionConflictDeferredToDeploy": {
			// A same-URL OCI version conflict is no longer drift: the URL-mode
			// version comparison was removed, so the conflict is surfaced at
			// deploy time instead of looping here.
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							URL:     "oci://registry.example.com/charts/mychart:1.2.3",
							Version: "2.0.0",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: "1.2.3",
						},
					},
					Config: testReleaseConfig,
					Labels: map[string]string{
						helmClient.LabelURLHash: helmClient.EncodeURLLabel("oci://registry.example.com/charts/mychart:1.2.3"),
					},
				},
				status: v1beta1.ReleaseStatus{},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"UpToDate_OCIFloatingTagLatestNoLoop": {
			// A floating :latest tag whose Chart.yaml metadata version differs
			// must not loop: the URL still matches the deploy-time label and the
			// metadata version is no longer compared against the tag.
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							URL: "oci://registry.example.com/charts/mychart:latest",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: "6.10.2",
						},
					},
					Config: testReleaseConfig,
					Labels: map[string]string{
						helmClient.LabelURLHash: helmClient.EncodeURLLabel("oci://registry.example.com/charts/mychart:latest"),
					},
				},
				status: v1beta1.ReleaseStatus{},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"UpToDate_OCIVPrefixedTagNoLoop": {
			// A v-prefixed tag whose metadata version drops the "v" must not
			// loop for the same reason as the floating-tag case.
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							URL: "oci://registry.example.com/charts/mychart:v6.10.1",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: "6.10.2",
						},
					},
					Config: testReleaseConfig,
					Labels: map[string]string{
						helmClient.LabelURLHash: helmClient.EncodeURLLabel("oci://registry.example.com/charts/mychart:v6.10.1"),
					},
				},
				status: v1beta1.ReleaseStatus{},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"NotUpToDate_OCIURLTagChangedViaLabel": {
			// The deploy-time label was written from :6.10.1 but the spec now
			// asks for :6.10.2; a tag change is a URL change and must drift.
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							URL: "oci://registry.example.com/charts/mychart:6.10.2",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: "6.10.1",
						},
					},
					Config: testReleaseConfig,
					Labels: map[string]string{
						helmClient.LabelURLHash: helmClient.EncodeURLLabel("oci://registry.example.com/charts/mychart:6.10.1"),
					},
				},
				status: v1beta1.ReleaseStatus{},
			},
			want: want{
				out: false,
				err: nil,
			},
		},
		"NotUpToDate_URLClearedSwitchesToRepoMode": {
			// The release was deployed from a URL (label present) but the spec
			// now clears the URL to switch back to repository mode;
			// EncodeURLLabel("") == "" no longer matches the label, so drift.
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
					Labels: map[string]string{
						helmClient.LabelURLHash: helmClient.EncodeURLLabel("oci://registry.example.com/charts/mychart:1.0.0"),
					},
				},
				status: v1beta1.ReleaseStatus{},
			},
			want: want{
				out: false,
				err: nil,
			},
		},
		"NotUpToDate_SameURLDigestConflict": {
			// The OCI URL embeds one digest while the spec pins a different one;
			// EffectiveChartDigest cannot resolve, so with a digest label present
			// this is reported as drift to surface the deploy-time rejection.
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							URL:    "oci://registry.example.com/charts/mychart@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
							Digest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
					Labels: map[string]string{
						helmClient.LabelDigestHash: helmClient.EncodeDigestLabel("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
					},
				},
				status: v1beta1.ReleaseStatus{},
			},
			want: want{
				out: false,
				err: nil,
			},
		},
		"SuccessUpToDate": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
				},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"SuccessPatchesAdded": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Values: runtime.RawExtension{
								Raw: []byte(testReleaseConfigStr),
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: testReleaseConfig,
				},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
		"Success_Int64VsFloat64_Set": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				spec: &v1beta1.ReleaseSpec{
					ForProvider: v1beta1.ReleaseParameters{
						Chart: v1beta1.ChartSpec{
							Name:    testChart,
							Version: testVersion,
						},
						ValuesSpec: v1beta1.ValuesSpec{
							Set: []v1beta1.SetVal{
								{Name: "replicas", Value: "3"},
							},
						},
					},
				},
				observed: &release.Release{
					Info: &release.Info{},
					Chart: &chart.Chart{
						Raw: nil,
						Metadata: &chart.Metadata{
							Name:    testChart,
							Version: testVersion,
						},
					},
					Config: map[string]interface{}{
						"replicas": float64(3),
					},
				},
			},
			want: want{
				out: true,
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, gotErr := isUpToDate(context.Background(), tc.args.kube, tc.args.spec, tc.args.observed, tc.args.status, testNamespace)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("isUpToDate(...): -want error, +got error: %s", diff)
			}
			if diff := cmp.Diff(tc.want.out, got); diff != "" {
				t.Errorf("isUpToDate(...): -want result, +got result: %s", diff)
			}
		})
	}
}

func Test_rehydrateFromLabels(t *testing.T) {
	const (
		digestA      = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		digestB      = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		legacyDigest = "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	)
	type args struct {
		cr                 *v1beta1.Release
		rel                *release.Release
		lastDigest         string
		lastOwnershipTaken bool
	}
	type want struct {
		digest         string
		ownershipTaken bool
	}
	cases := map[string]struct {
		args
		want
	}{
		"DigestLabelMatchesSpecRestoresFullDigest": {
			// Branch 1: the label encodes the effective spec digest, so the
			// full-fidelity spec digest is restored (the label is truncated).
			args: args{
				cr: helmRelease(func(r *v1beta1.Release) {
					r.Spec.ForProvider.Chart.Digest = digestA
				}),
				rel: &release.Release{
					Labels: map[string]string{
						helmClient.LabelDigestHash: helmClient.EncodeDigestLabel(digestA),
					},
				},
			},
			want: want{
				digest:         digestA,
				ownershipTaken: false,
			},
		},
		"DigestLabelMismatchKeepsTruncatedLabel": {
			// Branch 2: the deployed digest differs from the spec; only its
			// truncated encoded form is known.
			args: args{
				cr: helmRelease(func(r *v1beta1.Release) {
					r.Spec.ForProvider.Chart.Digest = digestA
				}),
				rel: &release.Release{
					Labels: map[string]string{
						helmClient.LabelDigestHash: helmClient.EncodeDigestLabel(digestB),
					},
				},
				lastOwnershipTaken: true,
			},
			want: want{
				digest:         helmClient.EncodeDigestLabel(digestB),
				ownershipTaken: true,
			},
		},
		"DigestLabelAbsentKeepsLastDigest": {
			// Branch 3: no label, so the previously persisted status digest wins.
			args: args{
				cr:         helmRelease(),
				rel:        &release.Release{},
				lastDigest: legacyDigest,
			},
			want: want{
				digest:         legacyDigest,
				ownershipTaken: false,
			},
		},
		"OwnershipLabelTrueSetsOwnershipTaken": {
			// Branch 4: the sticky ownership label forces OwnershipTaken true.
			args: args{
				cr: helmRelease(),
				rel: &release.Release{
					Labels: map[string]string{
						helmClient.LabelOwnershipTaken: "true",
					},
				},
			},
			want: want{
				digest:         "",
				ownershipTaken: true,
			},
		},
		"OwnershipLabelAbsentKeepsLast": {
			// Branch 5: no ownership label, so the last known value is kept.
			args: args{
				cr:                 helmRelease(),
				rel:                &release.Release{},
				lastOwnershipTaken: true,
			},
			want: want{
				digest:         "",
				ownershipTaken: true,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rehydrateFromLabels(tc.args.cr, tc.args.rel, tc.args.lastDigest, tc.args.lastOwnershipTaken)
			got := want{
				digest:         tc.args.cr.Status.AtProvider.Digest,
				ownershipTaken: tc.args.cr.Status.AtProvider.OwnershipTaken,
			}
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(want{})); diff != "" {
				t.Errorf("rehydrateFromLabels(...): -want, +got: %s", diff)
			}
		})
	}
}

func Test_connectionDetails(t *testing.T) {
	type args struct {
		kube         client.Client
		connDetails  []v1beta1.ConnectionDetail
		relName      string
		relNamespace string
	}
	type want struct {
		out managed.ConnectionDetails
		err error
	}
	cases := map[string]struct {
		args
		want
	}{
		"Fail_NotPartOfRelease": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if o, ok := obj.(*unstructured.Unstructured); o.GetKind() == "Secret" && ok && key.Name == testSecretName && key.Namespace == testNamespace {
							*obj.(*unstructured.Unstructured) = unstructured.Unstructured{
								Object: map[string]interface{}{
									"data": map[string]interface{}{
										"db-password": "MTIzNDU=",
									},
								},
							}
						}
						return nil
					},
				},
				connDetails: []v1beta1.ConnectionDetail{
					{
						ObjectReference: corev1.ObjectReference{
							Kind:       "Secret",
							Namespace:  testNamespace,
							Name:       testSecretName,
							APIVersion: "v1",
							FieldPath:  "data.db-password",
						},
						ToConnectionSecretKey: "password",
					},
				},
				relName:      testReleaseName,
				relNamespace: testNamespace,
			},
			want: want{
				out: managed.ConnectionDetails{},
				err: errors.Errorf(errObjectNotPartOfRelease, corev1.ObjectReference{
					Kind:       "Secret",
					Namespace:  testNamespace,
					Name:       testSecretName,
					APIVersion: "v1",
					FieldPath:  "data.db-password",
				}),
			},
		},
		"Success_PartOfRelease": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if o, ok := obj.(*unstructured.Unstructured); o.GetKind() == "Secret" && ok && key.Name == testSecretName && key.Namespace == testNamespace {
							*obj.(*unstructured.Unstructured) = unstructured.Unstructured{
								Object: map[string]interface{}{
									"metadata": map[string]interface{}{
										"annotations": map[string]interface{}{
											helmReleaseNameAnnotation:      testReleaseName,
											helmReleaseNamespaceAnnotation: testNamespace,
										},
									},
									"data": map[string]interface{}{
										"db-password": "MTIzNDU=",
									},
								},
							}
						}
						return nil
					},
				},
				connDetails: []v1beta1.ConnectionDetail{
					{
						ObjectReference: corev1.ObjectReference{
							Kind:       "Secret",
							Namespace:  testNamespace,
							Name:       testSecretName,
							APIVersion: "v1",
							FieldPath:  "data.db-password",
						},
						ToConnectionSecretKey: "password",
					},
				},
				relName:      testReleaseName,
				relNamespace: testNamespace,
			},
			want: want{
				out: managed.ConnectionDetails{
					"password": []byte("12345"),
				},
			},
		},

		"Success_NotPartOfReleaseAndSkipPartOfReleaseCheck": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if o, ok := obj.(*unstructured.Unstructured); o.GetKind() == "Secret" && ok && key.Name == testSecretName && key.Namespace == testNamespace {
							*obj.(*unstructured.Unstructured) = unstructured.Unstructured{
								Object: map[string]interface{}{
									"data": map[string]interface{}{
										"db-password": "MTIzNDU=",
									},
								},
							}
						}
						return nil
					},
				},

				connDetails: []v1beta1.ConnectionDetail{
					{
						ObjectReference: corev1.ObjectReference{
							Kind:       "Secret",
							Namespace:  testNamespace,
							Name:       testSecretName,
							APIVersion: "v1",
							FieldPath:  "data.db-password",
						},
						ToConnectionSecretKey:  "password",
						SkipPartOfReleaseCheck: true,
					},
				},
				relName:      testReleaseName,
				relNamespace: testNamespace,
			},
			want: want{
				out: managed.ConnectionDetails{
					"password": []byte("12345"),
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, gotErr := connectionDetails(context.Background(), tc.args.kube, tc.args.connDetails, tc.args.relName, tc.args.relNamespace)
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("connectionDetails(...): -want error, +got error: %s", diff)
			}
			if diff := cmp.Diff(tc.want.out, got); diff != "" {
				t.Errorf("connectionDetails(...): -want result, +got result: %s", diff)
			}
		})
	}
}
