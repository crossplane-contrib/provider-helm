package release

import (
	"context"
	"testing"

	xpv1 "github.com/crossplane/crossplane-runtime/v2/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1beta1"
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
					ResourceSpec: xpv1.ResourceSpec{
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
					ResourceSpec: xpv1.ResourceSpec{
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
			got, gotErr := isUpToDate(context.Background(), tc.args.kube, tc.args.spec, tc.args.observed, v1beta1.ReleaseStatus{})
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
