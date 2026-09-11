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

package v1alpha1_test

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/randfill"

	"github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1alpha1"
	"github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1beta1"
)

// rawExtensionFuzzer fills only the .Raw bytes of a runtime.RawExtension,
// leaving .Object nil (an interface field randfill can't fill on its own).
// Raw bytes are all that ever actually round-trips through JSON in practice.
func rawExtensionFuzzer(r *runtime.RawExtension, c randfill.Continue) {
	r.Object = nil
	r.Raw = []byte(fmt.Sprintf(`{"k%d":"v%d"}`, c.Intn(1000), c.Intn(1000)))
}

func newFiller(nilChance float64) *randfill.Filler {
	return randfill.New().NilChance(nilChance).NumElements(0, 3).Funcs(rawExtensionFuzzer)
}

// cmpOpts treats nil and empty slices/maps as equal, and ignores the
// conversion-data annotation: its presence/exact value is conversion
// bookkeeping, not part of the round-tripped object's real content.
var cmpOpts = []cmp.Option{
	cmpopts.EquateEmpty(),
	cmpopts.IgnoreMapEntries(func(k, _ string) bool {
		return k == v1alpha1.ConversionDataAnnotationKey
	}),
}

// TestConvertSpokeHubSpoke verifies that a v1alpha1 (spoke) Release survives
// a round trip through v1beta1 (hub) and back unchanged.
func TestConvertSpokeHubSpoke(t *testing.T) {
	for _, nilChance := range []float64{0, 0.3} {
		f := newFiller(nilChance)
		for i := 0; i < 200; i++ {
			src := &v1alpha1.Release{}
			f.Fill(src)
			src.TypeMeta = metav1.TypeMeta{}
			src.ManagedFields = nil // generic k8s metadata quirk, unrelated to our conversion logic

			hub := &v1beta1.Release{}
			if err := src.ConvertTo(hub); err != nil {
				t.Fatalf("nilChance %v, iteration %d: ConvertTo: %v", nilChance, i, err)
			}

			final := &v1alpha1.Release{}
			if err := final.ConvertFrom(hub); err != nil {
				t.Fatalf("nilChance %v, iteration %d: ConvertFrom: %v", nilChance, i, err)
			}

			if diff := cmp.Diff(src, final, cmpOpts...); diff != "" {
				t.Fatalf("nilChance %v, iteration %d: spoke->hub->spoke round-trip diff (-want +got):\n%s", nilChance, i, diff)
			}
		}
	}
}

// TestConvertHubSpokeHub verifies that a v1beta1 (hub) Release survives a
// round trip through v1alpha1 (spoke) and back unchanged, proving that the
// conversion-data annotation correctly preserves fields v1alpha1 has no
// representation for.
func TestConvertHubSpokeHub(t *testing.T) {
	for _, nilChance := range []float64{0, 0.3} {
		f := newFiller(nilChance)
		for i := 0; i < 200; i++ {
			src := &v1beta1.Release{}
			f.Fill(src)
			src.TypeMeta = metav1.TypeMeta{}
			src.ManagedFields = nil // generic k8s metadata quirk, unrelated to our conversion logic

			spoke := &v1alpha1.Release{}
			if err := spoke.ConvertFrom(src); err != nil {
				t.Fatalf("nilChance %v, iteration %d: ConvertFrom: %v", nilChance, i, err)
			}

			final := &v1beta1.Release{}
			if err := spoke.ConvertTo(final); err != nil {
				t.Fatalf("nilChance %v, iteration %d: ConvertTo: %v", nilChance, i, err)
			}

			if diff := cmp.Diff(src, final, cmpOpts...); diff != "" {
				t.Fatalf("nilChance %v, iteration %d: hub->spoke->hub round-trip diff (-want +got):\n%s", nilChance, i, diff)
			}
		}
	}
}
