package helm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
)

func TestResolveOCIChartVersionAndDigest(t *testing.T) {
	type want struct {
		urlPath string
		version string
		digest  string
		err     error
	}

	tests := []struct {
		name     string
		chartURL string
		want     want
	}{
		{
			name:     "VersionOnly",
			chartURL: "oci://registry.example.com/charts/mychart:1.2.3",
			want: want{
				urlPath: "oci://registry.example.com/charts/mychart",
				version: "1.2.3",
				digest:  "",
				err:     nil,
			},
		},
		{
			name:     "DigestOnly",
			chartURL: "oci://registry.example.com/charts/mychart@sha256:abc123def456",
			want: want{
				urlPath: "oci://registry.example.com/charts/mychart",
				version: "",
				digest:  "sha256:abc123def456",
				err:     nil,
			},
		},
		{
			name:     "BothVersionAndDigest",
			chartURL: "oci://registry.example.com/charts/mychart:1.2.3@sha256:abc123def456",
			want: want{
				urlPath: "oci://registry.example.com/charts/mychart",
				version: "1.2.3",
				digest:  "sha256:abc123def456",
				err:     nil,
			},
		},
		{
			name:     "NoVersionNoDigest",
			chartURL: "oci://registry.example.com/charts/mychart",
			want: want{
				urlPath: "oci://registry.example.com/charts/mychart",
				version: "",
				digest:  "",
				err:     nil,
			},
		},
		{
			name:     "ComplexPath",
			chartURL: "oci://registry.example.com:5000/org/repo/charts/mychart:1.0.0@sha256:abc",
			want: want{
				urlPath: "oci://registry.example.com:5000/org/repo/charts/mychart",
				version: "1.0.0",
				digest:  "sha256:abc",
				err:     nil,
			},
		},
		{
			name:     "LongDigest",
			chartURL: "oci://ghcr.io/myorg/charts/wordpress:15.2.5@sha256:d1c2884a2ac2d2f80fb1bf384e45b4cc72669498ccd237843dcc63bfcac810a3",
			want: want{
				urlPath: "oci://ghcr.io/myorg/charts/wordpress",
				version: "15.2.5",
				digest:  "sha256:d1c2884a2ac2d2f80fb1bf384e45b4cc72669498ccd237843dcc63bfcac810a3",
				err:     nil,
			},
		},
		{
			name:     "DigestOnlyLongHash",
			chartURL: "oci://localhost:5000/helm-charts/wordpress@sha256:d1c2884a2ac2d2f80fb1bf384e45b4cc72669498ccd237843dcc63bfcac810a3",
			want: want{
				urlPath: "oci://localhost:5000/helm-charts/wordpress",
				version: "",
				digest:  "sha256:d1c2884a2ac2d2f80fb1bf384e45b4cc72669498ccd237843dcc63bfcac810a3",
				err:     nil,
			},
		},
		{
			name:     "PortInRegistry",
			chartURL: "oci://localhost:5000/charts/mychart:1.0.0",
			want: want{
				urlPath: "oci://localhost:5000/charts/mychart",
				version: "1.0.0",
				digest:  "",
				err:     nil,
			},
		},
		{
			name:     "NonOCIURL",
			chartURL: "https://charts.example.com/mychart",
			want: want{
				urlPath: "",
				version: "",
				digest:  "",
				err:     errors.Errorf(errUnexpectedOCIUrlTmpl, "https://charts.example.com/mychart"),
			},
		},
		{
			name:     "HTTPSRegistry",
			chartURL: "https://registry.example.com/charts/mychart:1.0.0",
			want: want{
				urlPath: "",
				version: "",
				digest:  "",
				err:     errors.Errorf(errUnexpectedOCIUrlTmpl, "https://registry.example.com/charts/mychart:1.0.0"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotVersion, gotDigest, gotErr := resolveOCIChartVersionAndDigest(tt.chartURL)

			// Compare errors
			if diff := cmp.Diff(tt.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Errorf("resolveOCIChartVersionAndDigest() error:\n%s", diff)
			}

			// Only check URL, version, and digest if there's no error
			if gotErr == nil && tt.want.err == nil {
				if gotURL.String() != tt.want.urlPath {
					t.Errorf("URL: want %q, got %q", tt.want.urlPath, gotURL.String())
				}
				if gotVersion != tt.want.version {
					t.Errorf("Version: want %q, got %q", tt.want.version, gotVersion)
				}
				if gotDigest != tt.want.digest {
					t.Errorf("Digest: want %q, got %q", tt.want.digest, gotDigest)
				}
			}
		})
	}
}

func TestResolveOCIChartRef(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		chartName  string
		want       string
	}{
		{
			name:       "BasicRef",
			repository: "oci://registry.example.com/charts",
			chartName:  "mychart",
			want:       "oci://registry.example.com/charts/mychart",
		},
		{
			name:       "RepositoryWithTrailingSlash",
			repository: "oci://registry.example.com/charts/",
			chartName:  "mychart",
			want:       "oci://registry.example.com/charts/mychart",
		},
		{
			name:       "ComplexPath",
			repository: "oci://ghcr.io/myorg/helm-charts",
			chartName:  "wordpress",
			want:       "oci://ghcr.io/myorg/helm-charts/wordpress",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveOCIChartRef(tt.repository, tt.chartName)
			if got != tt.want {
				t.Errorf("resolveOCIChartRef() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveOCIChartVersion_BackwardCompatibility(t *testing.T) {
	// Test that the old function still works correctly
	type want struct {
		urlPath string
		version string
		err     error
	}

	tests := []struct {
		name     string
		chartURL string
		want     want
	}{
		{
			name:     "VersionOnly",
			chartURL: "oci://registry.example.com/charts/mychart:1.2.3",
			want: want{
				urlPath: "oci://registry.example.com/charts/mychart",
				version: "1.2.3",
				err:     nil,
			},
		},
		{
			name:     "NoVersion",
			chartURL: "oci://registry.example.com/charts/mychart",
			want: want{
				urlPath: "oci://registry.example.com/charts/mychart",
				version: "",
				err:     nil,
			},
		},
		{
			name:     "WithDigest_IgnoresDigest",
			chartURL: "oci://registry.example.com/charts/mychart:1.2.3@sha256:abc123",
			want: want{
				urlPath: "oci://registry.example.com/charts/mychart",
				version: "1.2.3",
				err:     nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotVersion, gotErr := resolveOCIChartVersion(tt.chartURL)

			if diff := cmp.Diff(tt.want.err, gotErr, test.EquateErrors()); diff != "" {
				t.Errorf("resolveOCIChartVersion() error:\n%s", diff)
			}

			if gotErr == nil && tt.want.err == nil {
				if gotURL.String() != tt.want.urlPath {
					t.Errorf("URL: want %q, got %q", tt.want.urlPath, gotURL.String())
				}
				if gotVersion != tt.want.version {
					t.Errorf("Version: want %q, got %q", tt.want.version, gotVersion)
				}
			}
		})
	}
}

func TestComputeFileDigest(t *testing.T) {
	tests := []struct {
		name        string
		fileContent string
		wantDigest  string
		wantErr     bool
	}{
		{
			name:        "EmptyFile",
			fileContent: "",
			// SHA256 of empty string
			wantDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			wantErr:    false,
		},
		{
			name:        "SimpleContent",
			fileContent: "Hello, World!",
			// SHA256 of "Hello, World!"
			wantDigest: "sha256:dffd6021bb2bd5b0af676290809ec3a53191dd81c7f70a4b28688a362182986f",
			wantErr:    false,
		},
		{
			name:        "BinaryContent",
			fileContent: "\x00\x01\x02\x03\x04\x05",
			// SHA256 of the binary content
			wantDigest: "sha256:17e88db187afd62c16e5debf3e6527cd006bc012bc90b51a810cd80c2d511f43",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary file
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "test.tgz")

			if err := os.WriteFile(tmpFile, []byte(tt.fileContent), 0600); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			// Compute digest
			gotDigest, err := computeFileDigest(tmpFile)

			if (err != nil) != tt.wantErr {
				t.Errorf("computeFileDigest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if gotDigest != tt.wantDigest {
				t.Errorf("computeFileDigest() = %v, want %v", gotDigest, tt.wantDigest)
			}
		})
	}

	// Test file not found
	t.Run("FileNotFound", func(t *testing.T) {
		_, err := computeFileDigest("/nonexistent/file.tgz")
		if err == nil {
			t.Error("computeFileDigest() expected error for nonexistent file, got nil")
		}
	})
}
