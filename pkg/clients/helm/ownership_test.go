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
	"context"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	"github.com/google/go-cmp/cmp"
	release "helm.sh/helm/v4/pkg/release/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	testRelName = "some-release"
	testRelNS   = "release-namespace"
)

// mapperClient is a MockClient that serves a RESTMapper, which MockClient
// itself returns nil for. Only the kinds registered here are resolvable;
// anything else maps to a no-match error, as an unserved kind does on a real
// cluster.
type mapperClient struct {
	ctrlclient.Client
	mapper apimeta.RESTMapper
}

func (c *mapperClient) RESTMapper() apimeta.RESTMapper { return c.mapper }

func testMapper() apimeta.RESTMapper {
	gv := schema.GroupVersion{Group: "", Version: "v1"}
	m := apimeta.NewDefaultRESTMapper([]schema.GroupVersion{gv})
	m.Add(gv.WithKind("ConfigMap"), apimeta.RESTScopeNamespace)
	m.Add(gv.WithKind("Namespace"), apimeta.RESTScopeRoot)
	return m
}

// ownedMeta is the metadata Helm stamps on a resource belonging to the test
// release, as a YAML fragment.
const ownedMeta = `
  labels:
    app.kubernetes.io/managed-by: Helm
  annotations:
    meta.helm.sh/release-name: some-release
    meta.helm.sh/release-namespace: release-namespace`

func manifest(docs ...string) string {
	out := ""
	for _, d := range docs {
		out += "---\n# Source: chart/templates/thing.yaml\n" + d + "\n"
	}
	return out
}

func cm(name, ns string) string {
	s := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name
	if ns != "" {
		s += "\n  namespace: " + ns
	}
	return s
}

// liveObject returns a Get that writes the given labels and annotations onto
// the metadata object it is handed, and records the key it was asked for.
func liveObject(labels, annotations map[string]string, keys *[]ctrlclient.ObjectKey) func(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object) error {
	return func(_ context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object) error {
		if keys != nil {
			*keys = append(*keys, key)
		}
		u, ok := obj.(*metav1.PartialObjectMetadata)
		if !ok {
			return errors.New("not partial metadata")
		}
		u.SetName(key.Name)
		u.SetNamespace(key.Namespace)
		u.SetLabels(labels)
		u.SetAnnotations(annotations)
		return nil
	}
}

func ownedLabels() map[string]string {
	return map[string]string{OwnerLabelManagedBy: OwnerManagedByHelm}
}

func ownedAnnotations() map[string]string {
	return map[string]string{
		OwnerAnnotationReleaseName:      testRelName,
		OwnerAnnotationReleaseNamespace: testRelNS,
	}
}

func TestOwnershipDrifted(t *testing.T) {
	errBoom := errors.New("boom")

	type args struct {
		get      func(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object) error
		manifest string
	}
	type want struct {
		drifted bool
		err     error
	}

	cases := map[string]struct {
		args
		want
	}{
		"AllResourcesOwned": {
			args: args{
				manifest: manifest(cm("a", "")+ownedMeta, cm("b", "")+ownedMeta),
				get:      liveObject(ownedLabels(), ownedAnnotations(), nil),
			},
			want: want{drifted: false},
		},
		"ManagedByLabelStripped": {
			// The commonest shape of the problem: another actor re-applied the
			// resource without Helm's label. Helm would refuse to adopt it.
			args: args{
				manifest: manifest(cm("a", "") + ownedMeta),
				get:      liveObject(nil, ownedAnnotations(), nil),
			},
			want: want{drifted: true},
		},
		"ManagedByLabelPointsElsewhere": {
			args: args{
				manifest: manifest(cm("a", "") + ownedMeta),
				get: liveObject(map[string]string{OwnerLabelManagedBy: "kustomize"},
					ownedAnnotations(), nil),
			},
			want: want{drifted: true},
		},
		"ReleaseNameAnnotationNamesAnotherRelease": {
			args: args{
				manifest: manifest(cm("a", "") + ownedMeta),
				get: liveObject(ownedLabels(), map[string]string{
					OwnerAnnotationReleaseName:      "other-release",
					OwnerAnnotationReleaseNamespace: testRelNS,
				}, nil),
			},
			want: want{drifted: true},
		},
		"ReleaseNamespaceAnnotationNamesAnotherNamespace": {
			args: args{
				manifest: manifest(cm("a", "") + ownedMeta),
				get: liveObject(ownedLabels(), map[string]string{
					OwnerAnnotationReleaseName:      testRelName,
					OwnerAnnotationReleaseNamespace: "elsewhere",
				}, nil),
			},
			want: want{drifted: true},
		},
		"AnnotationsAbsentEntirely": {
			args: args{
				manifest: manifest(cm("a", "") + ownedMeta),
				get:      liveObject(ownedLabels(), nil, nil),
			},
			want: want{drifted: true},
		},
		"ResourceNotLiveYet": {
			// Nothing to take ownership of: the next deploy creates it.
			args: args{
				manifest: manifest(cm("a", "") + ownedMeta),
				get: func(_ context.Context, _ ctrlclient.ObjectKey, _ ctrlclient.Object) error {
					return kerrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "a")
				},
			},
			want: want{drifted: false},
		},
		"KindNotServedByCluster": {
			// A CRD the chart also installs, not yet established. Not drift.
			args: args{
				manifest: manifest("apiVersion: example.org/v1\nkind: Widget\nmetadata:\n  name: w"),
				get:      liveObject(nil, nil, nil),
			},
			want: want{drifted: false},
		},
		"ResourceWithoutNameSkipped": {
			// Rendered with generateName: no stable identity to look up.
			args: args{
				manifest: manifest("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  generateName: a-"),
				get: func(_ context.Context, _ ctrlclient.ObjectKey, _ ctrlclient.Object) error {
					return errors.New("should not be called")
				},
			},
			want: want{drifted: false},
		},
		"EmptyManifest": {
			args: args{manifest: "", get: liveObject(nil, nil, nil)},
			want: want{drifted: false},
		},
		"CommentOnlyDocumentsSkipped": {
			args: args{
				manifest: "---\n# Source: chart/templates/empty.yaml\n---\n",
				get: func(_ context.Context, _ ctrlclient.ObjectKey, _ ctrlclient.Object) error {
					return errors.New("should not be called")
				},
			},
			want: want{drifted: false},
		},
		"MalformedManifest": {
			args: args{
				manifest: "\tthis is not: yaml: at all\n",
				get:      liveObject(nil, nil, nil),
			},
			want: want{err: errors.Wrap(errors.New("error converting YAML to JSON: yaml: found character that cannot start any token"), errParseManifest)},
		},
		"GetFails": {
			args: args{
				manifest: manifest(cm("a", "") + ownedMeta),
				get: func(_ context.Context, _ ctrlclient.ObjectKey, _ ctrlclient.Object) error {
					return errBoom
				},
			},
			want: want{err: errors.Wrap(errBoom, `cannot get ConfigMap "a"`)},
		},
		"FirstMismatchShortCircuits": {
			// The second resource is never looked up, so its Get failing
			// cannot mask the drift already found on the first.
			args: args{
				manifest: manifest(cm("a", "")+ownedMeta, cm("b", "")+ownedMeta),
				get: func() func(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object) error {
					n := 0
					return func(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object) error {
						n++
						if n > 1 {
							return errBoom
						}
						return liveObject(nil, nil, nil)(ctx, key, obj)
					}
				}(),
			},
			want: want{drifted: true},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			kube := &mapperClient{
				Client: &test.MockClient{MockGet: tc.args.get},
				mapper: testMapper(),
			}
			rel := &release.Release{Name: testRelName, Namespace: testRelNS, Manifest: tc.args.manifest}

			got, err := OwnershipDrifted(context.Background(), kube, rel)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Fatalf("OwnershipDrifted(...): -want error, +got error: %s", diff)
			}
			if diff := cmp.Diff(tc.want.drifted, got); diff != "" {
				t.Errorf("OwnershipDrifted(...): -want, +got: %s", diff)
			}
		})
	}
}

func TestOwnershipDriftedLooksUpTheRightObject(t *testing.T) {
	cases := map[string]struct {
		manifest string
		want     []ctrlclient.ObjectKey
	}{
		"NamespaceDefaultsToTheReleaseNamespace": {
			manifest: manifest(cm("a", "") + ownedMeta),
			want:     []ctrlclient.ObjectKey{{Name: "a", Namespace: testRelNS}},
		},
		"ExplicitNamespaceWins": {
			// A chart may render into a namespace other than the release's.
			manifest: manifest(cm("a", "elsewhere") + ownedMeta),
			want:     []ctrlclient.ObjectKey{{Name: "a", Namespace: "elsewhere"}},
		},
		"ClusterScopedFetchedWithoutANamespace": {
			manifest: manifest("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: ns-a" + ownedMeta),
			want:     []ctrlclient.ObjectKey{{Name: "ns-a"}},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var keys []ctrlclient.ObjectKey
			kube := &mapperClient{
				Client: &test.MockClient{MockGet: liveObject(ownedLabels(), ownedAnnotations(), &keys)},
				mapper: testMapper(),
			}
			rel := &release.Release{Name: testRelName, Namespace: testRelNS, Manifest: tc.manifest}

			if _, err := OwnershipDrifted(context.Background(), kube, rel); err != nil {
				t.Fatalf("OwnershipDrifted(...): unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, keys); diff != "" {
				t.Errorf("OwnershipDrifted(...) looked up: -want, +got: %s", diff)
			}
		})
	}
}
