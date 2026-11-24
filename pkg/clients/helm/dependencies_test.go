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
	"strings"
	"testing"

	chart "helm.sh/helm/v4/pkg/chart/v2"
)

func TestCheckChartDependencies(t *testing.T) {
	newChart := func(deps []*chart.Dependency, loaded ...*chart.Chart) *chart.Chart {
		chrt := &chart.Chart{Metadata: &chart.Metadata{
			APIVersion:   "v2",
			Name:         "parent",
			Version:      "1.0.0",
			Dependencies: deps,
		}}
		chrt.SetDependencies(loaded...)
		return chrt
	}

	cases := map[string]struct {
		chrt    *chart.Chart
		wantErr string
	}{
		"NoDeclaredDependencies": {
			chrt: newChart(nil),
		},
		"DeclaredDependencyPresent": {
			chrt: newChart(
				[]*chart.Dependency{{Name: "subchart", Version: "1.0.0"}},
				&chart.Chart{Metadata: &chart.Metadata{Name: "subchart", Version: "1.0.0"}},
			),
		},
		"DeclaredDependencyMissing": {
			chrt:    newChart([]*chart.Dependency{{Name: "subchart", Version: "1.0.0"}}),
			wantErr: "found in Chart.yaml, but missing in charts/ directory: subchart",
		},
		"SomeDeclaredDependenciesMissing": {
			chrt: newChart(
				[]*chart.Dependency{
					{Name: "present", Version: "1.0.0"},
					{Name: "absent", Version: "1.0.0"},
				},
				&chart.Chart{Metadata: &chart.Metadata{Name: "present", Version: "1.0.0"}},
			),
			wantErr: "found in Chart.yaml, but missing in charts/ directory: absent",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := checkChartDependencies(tc.chrt)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("checkChartDependencies() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkChartDependencies() expected an error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("checkChartDependencies() error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
			if !strings.Contains(err.Error(), errChartDepsMissing) {
				t.Errorf("checkChartDependencies() error = %q, want it to contain context %q", err.Error(), errChartDepsMissing)
			}
		})
	}
}
