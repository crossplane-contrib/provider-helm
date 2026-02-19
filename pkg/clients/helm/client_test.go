package helm

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestResolveOCIChartVersion(t *testing.T) {
	tests := map[string]struct {
		chartURL string
		wantPath string
		wantVer  string
		wantErr  bool
	}{
		"Tag": {
			chartURL: "oci://registry.example.com/stable/mychart:1.2.3",
			wantPath: "/stable/mychart",
			wantVer:  "1.2.3",
		},
		"Digest": {
			chartURL: "oci://registry.example.com/stable/mychart@sha256:5a2c3d4e5f6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d",
			wantPath: "/stable/mychart@sha256:5a2c3d4e5f6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d",
			wantVer:  "",
		},
		"TagAndDigest": {
			chartURL: "oci://registry.example.com/stable/mychart:1.2.3@sha256:5a2c3d4e5f6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d",
			wantPath: "/stable/mychart@sha256:5a2c3d4e5f6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d",
			wantVer:  "1.2.3",
		},
		"NonOCI": {
			chartURL: "https://example.com/stable/mychart:1.2.3",
			wantErr:  true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			gotURL, gotVer, err := resolveOCIChartVersion(tc.chartURL)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveOCIChartVersion(%q): expected error", tc.chartURL)
				}
				return
			}

			if err != nil {
				t.Fatalf("resolveOCIChartVersion(%q): %v", tc.chartURL, err)
			}

			if diff := cmp.Diff(tc.wantPath, gotURL.Path); diff != "" {
				t.Fatalf("resolveOCIChartVersion(%q) path mismatch (-want +got):\n%s", tc.chartURL, diff)
			}
			if diff := cmp.Diff(tc.wantVer, gotVer); diff != "" {
				t.Fatalf("resolveOCIChartVersion(%q) version mismatch (-want +got):\n%s", tc.chartURL, diff)
			}
		})
	}
}
