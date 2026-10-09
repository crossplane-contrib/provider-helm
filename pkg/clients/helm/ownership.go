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
	"io"
	"strings"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	release "helm.sh/helm/v4/pkg/release/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// The ownership metadata Helm stamps on every resource it manages, and
// validates before adopting a resource that already exists. These mirror the
// unexported constants in helm.sh/helm/v4/pkg/action: a resource whose live
// values differ from them is one Helm would refuse to adopt without
// --take-ownership.
const (
	OwnerLabelManagedBy             = "app.kubernetes.io/managed-by"
	OwnerManagedByHelm              = "Helm"
	OwnerAnnotationReleaseName      = "meta.helm.sh/release-name"
	OwnerAnnotationReleaseNamespace = "meta.helm.sh/release-namespace"
)

const errParseManifest = "cannot parse rendered manifest of last release"

// OwnershipDrifted reports whether any resource rendered by the release is
// live in the target cluster carrying ownership metadata that does not name
// this release. Helm itself only validates ownership for resources it is about
// to create, so a resource that was adopted once and later re-stamped by
// another actor is invisible to the release: nothing in the chart, values or
// digest changes, and no upgrade is ever triggered to take it back. Calling
// this on every observation is what turns spec.forProvider.takeOwnership from
// a one-shot adoption into a standing claim. A release that is already out of
// date owes no such check - its deploy is going to adopt whatever the chart
// renders whether this is called or not - so callers that know a deploy is
// owed should skip it.
//
// The check costs one lookup per rendered resource per observation, so it
// fetches PartialObjectMetadata: every value it compares lives in metadata,
// and only that needs to cross the wire.
//
// Resources that are absent, or whose kind is not (yet) served by the cluster,
// are not drift: there is nothing to take ownership of, and the next deploy
// creates them. Resources rendered with generateName are skipped for the same
// reason - they have no stable identity to look up.
func OwnershipDrifted(ctx context.Context, kube ctrlclient.Client, rel *release.Release) (bool, error) {
	objs, err := parseManifest(rel.Manifest)
	if err != nil {
		return false, err
	}

	for i := range objs {
		o := objs[i]
		ns, err := objectNamespace(kube.RESTMapper(), o, rel.Namespace)
		if err != nil {
			if apimeta.IsNoMatchError(err) {
				continue
			}
			return false, errors.Wrapf(err, "cannot map %s %q to a resource", o.GetKind(), o.GetName())
		}

		live := &metav1.PartialObjectMetadata{}
		live.SetGroupVersionKind(o.GroupVersionKind())
		if err := kube.Get(ctx, types.NamespacedName{Name: o.GetName(), Namespace: ns}, live); err != nil {
			if kerrors.IsNotFound(err) || apimeta.IsNoMatchError(err) {
				continue
			}
			return false, errors.Wrapf(err, "cannot get %s %q", o.GetKind(), o.GetName())
		}

		if !ownedByRelease(live, rel.Name, rel.Namespace) {
			return true, nil
		}
	}

	return false, nil
}

// ownedByRelease applies the same check Helm's checkOwnership does: the
// managed-by label must name Helm and both release annotations must name this
// release. A missing key counts as not owned, which is the case that matters
// most here - that is how a resource created outside Helm, or stripped by
// another controller, presents.
func ownedByRelease(u *metav1.PartialObjectMetadata, relName, relNamespace string) bool {
	if u.GetLabels()[OwnerLabelManagedBy] != OwnerManagedByHelm {
		return false
	}
	a := u.GetAnnotations()
	return a[OwnerAnnotationReleaseName] == relName && a[OwnerAnnotationReleaseNamespace] == relNamespace
}

// objectNamespace returns the namespace the object is live in: its own, else
// the release namespace, and empty for a cluster-scoped kind, which must be
// fetched without one. The scope is only knowable from the cluster, so this
// consults the client's RESTMapper, whose discovery results are cached with
// the client itself.
func objectNamespace(rm apimeta.RESTMapper, o unstructured.Unstructured, relNamespace string) (string, error) {
	gvk := o.GroupVersionKind()
	m, err := rm.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return "", err
	}
	if m.Scope.Name() != apimeta.RESTScopeNameNamespace {
		return "", nil
	}
	if ns := o.GetNamespace(); ns != "" {
		return ns, nil
	}
	return relNamespace, nil
}

// parseManifest decodes the multi-document YAML Helm persisted for the
// release. Documents that carry no object - the comment-only separators Helm
// emits between templates - and objects without a name are dropped.
func parseManifest(manifest string) ([]unstructured.Unstructured, error) {
	var out []unstructured.Unstructured

	d := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	for {
		u := unstructured.Unstructured{}
		if err := d.Decode(&u); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, errors.Wrap(err, errParseManifest)
		}
		if len(u.Object) == 0 || u.GetKind() == "" || u.GetName() == "" {
			continue
		}
		out = append(out, u)
	}

	return out, nil
}
