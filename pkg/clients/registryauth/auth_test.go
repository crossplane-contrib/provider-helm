/*
Copyright 2025 The Crossplane Authors.

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

package registryauth

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	xpv1 "github.com/crossplane/crossplane-runtime/v2/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"

	clusterv1beta1 "github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1beta1"
	namespacedv1beta1 "github.com/crossplane-contrib/provider-helm/apis/namespaced/release/v1beta1"
	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

const (
	testNamespace  = "test-namespace"
	testSecretName = "test-secret"
	testUsername   = "testuser"
	testPassword   = "testpass"
	testChartRepo  = "oci://registry.example.com/charts"
	testChartName  = "my-chart"
	testChartVer   = "1.0.0"
)

var (
	errBoom = errors.New("boom")
)

func TestResolveNamespaced(t *testing.T) {
	type args struct {
		kube    client.Client
		release *namespacedv1beta1.Release
	}
	type want struct {
		creds *helmClient.RepoCreds
		err   error
	}

	cases := map[string]struct {
		args args
		want want
	}{
		"NoSecretUsesDefaultKeychain": {
			args: args{
				kube: &test.MockClient{},
				release: &namespacedv1beta1.Release{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-release",
						Namespace: testNamespace,
					},
					Spec: namespacedv1beta1.ReleaseSpec{
						ForProvider: namespacedv1beta1.ReleaseParameters{
							Chart: namespacedv1beta1.ChartSpec{
								Repository: testChartRepo,
								Name:       testChartName,
								Version:    testChartVer,
							},
						},
					},
				},
			},
			want: want{
				// DefaultKeychain returns empty creds for unknown registries
				creds: &helmClient.RepoCreds{},
				err:   nil,
			},
		},
		"UsernamePasswordSecret": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if s, ok := obj.(*corev1.Secret); ok && key.Name == testSecretName {
							s.Data = map[string][]byte{
								"username": []byte(testUsername),
								"password": []byte(testPassword),
							}
							return nil
						}
						return errBoom
					},
				},
				release: &namespacedv1beta1.Release{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-release",
						Namespace: testNamespace,
					},
					Spec: namespacedv1beta1.ReleaseSpec{
						ForProvider: namespacedv1beta1.ReleaseParameters{
							Chart: namespacedv1beta1.ChartSpec{
								Repository: testChartRepo,
								Name:       testChartName,
								Version:    testChartVer,
								PullSecretRef: xpv1.LocalSecretReference{
									Name: testSecretName,
								},
							},
						},
					},
				},
			},
			want: want{
				creds: &helmClient.RepoCreds{
					Username: testUsername,
					Password: testPassword,
				},
				err: nil,
			},
		},
		"SecretNotFound": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						return kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testSecretName)
					},
				},
				release: &namespacedv1beta1.Release{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-release",
						Namespace: testNamespace,
					},
					Spec: namespacedv1beta1.ReleaseSpec{
						ForProvider: namespacedv1beta1.ReleaseParameters{
							Chart: namespacedv1beta1.ChartSpec{
								Repository: testChartRepo,
								Name:       testChartName,
								Version:    testChartVer,
								PullSecretRef: xpv1.LocalSecretReference{
									Name: testSecretName,
								},
							},
						},
					},
				},
			},
			want: want{
				creds: nil,
				err:   errors.Wrap(kerrors.NewNotFound(schema.GroupResource{Group: corev1.GroupName}, testSecretName), errFailedToGetSecret),
			},
		},
		"SecretMissingCredentials": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if s, ok := obj.(*corev1.Secret); ok && key.Name == testSecretName {
							s.Data = map[string][]byte{
								"somekey": []byte("somevalue"),
							}
							return nil
						}
						return errBoom
					},
				},
				release: &namespacedv1beta1.Release{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-release",
						Namespace: testNamespace,
					},
					Spec: namespacedv1beta1.ReleaseSpec{
						ForProvider: namespacedv1beta1.ReleaseParameters{
							Chart: namespacedv1beta1.ChartSpec{
								Repository: testChartRepo,
								Name:       testChartName,
								Version:    testChartVer,
								PullSecretRef: xpv1.LocalSecretReference{
									Name: testSecretName,
								},
							},
						},
					},
				},
			},
			want: want{
				creds: nil,
				err:   errors.New("secret must contain 'username' and 'password' keys"),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resolver := NewResolver(tc.args.kube)
			got, err := resolver.ResolveNamespaced(context.Background(), tc.args.release)

			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("ResolveNamespaced() error: -want, +got:\n%s", diff)
			}

			if diff := cmp.Diff(tc.want.creds, got); diff != "" {
				t.Errorf("ResolveNamespaced() creds: -want, +got:\n%s", diff)
			}
		})
	}
}

func TestResolveCluster(t *testing.T) {
	type args struct {
		kube    client.Client
		release *clusterv1beta1.Release
	}
	type want struct {
		creds *helmClient.RepoCreds
		err   error
	}

	cases := map[string]struct {
		args args
		want want
	}{
		"NoSecretUsesDefaultKeychain": {
			args: args{
				kube: &test.MockClient{},
				release: &clusterv1beta1.Release{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-release",
					},
					Spec: clusterv1beta1.ReleaseSpec{
						ResourceSpec: xpv1.ResourceSpec{},
						ForProvider: clusterv1beta1.ReleaseParameters{
							Chart: clusterv1beta1.ChartSpec{
								Repository: testChartRepo,
								Name:       testChartName,
								Version:    testChartVer,
							},
						},
					},
				},
			},
			want: want{
				creds: &helmClient.RepoCreds{},
				err:   nil,
			},
		},
		"UsernamePasswordSecret": {
			args: args{
				kube: &test.MockClient{
					MockGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if s, ok := obj.(*corev1.Secret); ok && key.Name == testSecretName {
							s.Data = map[string][]byte{
								"username": []byte(testUsername),
								"password": []byte(testPassword),
							}
							return nil
						}
						return errBoom
					},
				},
				release: &clusterv1beta1.Release{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-release",
					},
					Spec: clusterv1beta1.ReleaseSpec{
						ResourceSpec: xpv1.ResourceSpec{},
						ForProvider: clusterv1beta1.ReleaseParameters{
							Chart: clusterv1beta1.ChartSpec{
								Repository: testChartRepo,
								Name:       testChartName,
								Version:    testChartVer,
								PullSecretRef: xpv1.SecretReference{
									Name:      testSecretName,
									Namespace: testNamespace,
								},
							},
						},
					},
				},
			},
			want: want{
				creds: &helmClient.RepoCreds{
					Username: testUsername,
					Password: testPassword,
				},
				err: nil,
			},
		},
		"MissingNamespaceInSecretRef": {
			args: args{
				kube: &test.MockClient{},
				release: &clusterv1beta1.Release{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-release",
					},
					Spec: clusterv1beta1.ReleaseSpec{
						ResourceSpec: xpv1.ResourceSpec{},
						ForProvider: clusterv1beta1.ReleaseParameters{
							Chart: clusterv1beta1.ChartSpec{
								Repository: testChartRepo,
								Name:       testChartName,
								Version:    testChartVer,
								PullSecretRef: xpv1.SecretReference{
									Name: testSecretName,
									// Namespace missing
								},
							},
						},
					},
				},
			},
			want: want{
				creds: nil,
				err:   errors.New("namespace required in PullSecretRef for cluster-scoped Release"),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resolver := NewResolver(tc.args.kube)
			got, err := resolver.ResolveCluster(context.Background(), tc.args.release)

			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("ResolveCluster() error: -want, +got:\n%s", diff)
			}

			if diff := cmp.Diff(tc.want.creds, got); diff != "" {
				t.Errorf("ResolveCluster() creds: -want, +got:\n%s", diff)
			}
		})
	}
}
