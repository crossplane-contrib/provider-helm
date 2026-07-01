package release

import (
	"context"
	"testing"

	xpv1 "github.com/crossplane/crossplane-runtime/v2/apis/common/v1"
	xpv2 "github.com/crossplane/crossplane-runtime/v2/apis/common/v2"
	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	"github.com/google/go-cmp/cmp"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-helm/apis/namespaced/release/v1beta1"
)

const (
	testDescription = "test description"
)

var (
	testReleaseConfigStr = `
keyA: valA
keyB:
  subKeyA: subValA
  0e088d42-9039-4d86-bc8b-1a7d71768276: foobar
  2ce9c043-a5a1-48d7-8a99-55ad9aa4a441: foobar
  47463905-d483-4630-98d5-1844e08a16dc: foobar
  4c2ebfd6-6f70-494a-805e-e74d01a0c326: foobar
  4e6b8a03-2ab4-4f6e-b568-43af8ecf9487: foobar
  58bfb404-3b64-465e-8b43-395b36ffe9fd: foobar
  5d6e183f-be3c-4395-bebd-cce276955edd: foobar
  7bfc17a9-20ca-44a5-b18a-89112d7e13d2: foobar
  30cb9570-046d-4a81-9799-234327ae02d0: foobar
  57b66a6d-1d67-4047-8b97-bd5561c369a0: foobar
  57d22cbf-c3d3-4eac-8880-fba531cc9012: foobar
  638c6340-6796-4fea-9c6a-35274c2c10ec: foobar
  877d702d-2e56-49c4-827f-00fb8eba3898: foobar
  9463f2cc-df3b-4642-8a67-dba133fbf7d0: foobar
`
	testReleaseConfig = map[string]interface{}{
		"keyA": "valA",
		"keyB": map[string]interface{}{
			"subKeyA":                              "subValA",
			"0e088d42-9039-4d86-bc8b-1a7d71768276": "foobar",
			"2ce9c043-a5a1-48d7-8a99-55ad9aa4a441": "foobar",
			"47463905-d483-4630-98d5-1844e08a16dc": "foobar",
			"4c2ebfd6-6f70-494a-805e-e74d01a0c326": "foobar",
			"4e6b8a03-2ab4-4f6e-b568-43af8ecf9487": "foobar",
			"58bfb404-3b64-465e-8b43-395b36ffe9fd": "foobar",
			"5d6e183f-be3c-4395-bebd-cce276955edd": "foobar",
			"7bfc17a9-20ca-44a5-b18a-89112d7e13d2": "foobar",
			"30cb9570-046d-4a81-9799-234327ae02d0": "foobar",
			"57b66a6d-1d67-4047-8b97-bd5561c369a0": "foobar",
			"57d22cbf-c3d3-4eac-8880-fba531cc9012": "foobar",
			"638c6340-6796-4fea-9c6a-35274c2c10ec": "foobar",
			"877d702d-2e56-49c4-827f-00fb8eba3898": "foobar",
			"9463f2cc-df3b-4642-8a67-dba133fbf7d0": "foobar",
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
						Status:      release.StatusDeployed,
					},
				},
			},
			want: want{
				out: v1beta1.ReleaseObservation{
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
						ManagementPolicies: []xpv1.ManagementAction{
							xpv1.ManagementActionCreate,
							xpv1.ManagementActionDelete,
							xpv1.ManagementActionObserve,
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
						ManagementPolicies: []xpv1.ManagementAction{
							xpv1.ManagementActionCreate,
							xpv1.ManagementActionDelete,
							xpv1.ManagementActionObserve,
							xpv1.ManagementActionUpdate,
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
