package controller

import (
	"context"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-helm/apis/release/v1alpha1"
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
		out v1alpha1.ReleaseObservation
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
				out: v1alpha1.ReleaseObservation{
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
						Status:      release.StatusDeployed,
					},
				},
			},
			want: want{
				out: v1alpha1.ReleaseObservation{
					State:              release.StatusDeployed,
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
		in       *v1alpha1.ReleaseParameters
		observed *release.Release
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
						Status: release.StatusPendingUpgrade,
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
				in: &v1alpha1.ReleaseParameters{
					Chart: v1alpha1.ChartSpec{
						Name:    testChart,
						Version: testVersion,
					},
					ValuesSpec: v1alpha1.ValuesSpec{
						Values: "invalid-yaml",
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
				in: &v1alpha1.ReleaseParameters{
					Chart: v1alpha1.ChartSpec{
						Name:    "another-chart",
						Version: testVersion,
					},
					ValuesSpec: v1alpha1.ValuesSpec{
						Values: testReleaseConfigStr,
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
				in: &v1alpha1.ReleaseParameters{
					Chart: v1alpha1.ChartSpec{
						Name:    testChart,
						Version: "another-version",
					},
					ValuesSpec: v1alpha1.ValuesSpec{
						Values: testReleaseConfigStr,
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
				in: &v1alpha1.ReleaseParameters{
					Chart: v1alpha1.ChartSpec{
						Name:    testChart,
						Version: testVersion,
					},
					ValuesSpec: v1alpha1.ValuesSpec{
						Values: "keyA: valX",
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
		"SuccessUpToDate": {
			args: args{
				kube: &test.MockClient{
					MockGet: nil,
				},
				in: &v1alpha1.ReleaseParameters{
					Chart: v1alpha1.ChartSpec{
						Name:    testChart,
						Version: testVersion,
					},
					ValuesSpec: v1alpha1.ValuesSpec{
						Values: testReleaseConfigStr,
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
				in: &v1alpha1.ReleaseParameters{
					Chart: v1alpha1.ChartSpec{
						Name:    testChart,
						Version: testVersion,
					},
					ValuesSpec: v1alpha1.ValuesSpec{
						Values: testReleaseConfigStr,
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
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, gotErr := isUpToDate(context.Background(), tc.args.kube, tc.args.in, tc.args.observed, v1alpha1.ReleaseStatus{})
			if diff := cmp.Diff(tc.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Fatalf("isUpToDate(...): -want error, +got error: %s", diff)
			}
			if diff := cmp.Diff(tc.want.out, got); diff != "" {
				t.Errorf("isUpToDate(...): -want result, +got result: %s", diff)
			}
		})
	}
}
