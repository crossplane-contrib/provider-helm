package helm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"

	clusterv1beta1 "github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1beta1"
)

// mockLogger is a simple logger implementation for testing
type mockLogger struct {
	debugCallCount int
}

func (m *mockLogger) Debug(msg string, keysAndValues ...interface{}) {
	m.debugCallCount++
}

func (m *mockLogger) Info(msg string, keysAndValues ...interface{}) {}

func (m *mockLogger) WithValues(keysAndValues ...interface{}) logging.Logger {
	return m
}

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
	const digest = "sha256:abc123def456"

	tests := []struct {
		name       string
		repository string
		chartName  string
		digest     string
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
		{
			name:       "WithDigest",
			repository: "oci://registry.example.com/charts",
			chartName:  "mychart",
			digest:     digest,
			want:       "oci://registry.example.com/charts/mychart@" + digest,
		},
		{
			name:       "WithDigestAndTrailingSlash",
			repository: "oci://registry.example.com/charts/",
			chartName:  "mychart",
			digest:     digest,
			want:       "oci://registry.example.com/charts/mychart@" + digest,
		},
		{
			name:       "DigestWithSurroundingWhitespaceTrimmed",
			repository: "oci://registry.example.com/charts",
			chartName:  "mychart",
			digest:     "  " + digest + "  ",
			want:       "oci://registry.example.com/charts/mychart@" + digest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveOCIChartRef(tt.repository, tt.chartName, tt.digest)
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

// helmDigestPullFilename reproduces the filename Helm's chart downloader writes
// for an OCI chart pulled by digest (see helm.sh/helm/v3 pkg/downloader
// chart_downloader.go DownloadTo): it takes filepath.Base of the reference path
// (e.g. "mychart@sha256:abc") and replaces the last ':' with '-', yielding
// "mychart@sha256-abc.tgz". resolveCachedChartPathWithDigest must construct the
// same name so a digest-pinned pull is found in the cache on the next reconcile.
func helmDigestPullFilename(name, digest string) string {
	base := filepath.Base(name) + "@" + digest // e.g. mychart@sha256:abc
	idx := strings.LastIndexByte(base, ':')
	return fmt.Sprintf("%s-%s.tgz", base[:idx], base[idx+1:])
}

func TestResolveCachedChartPathWithDigest(t *testing.T) {
	// Override the global cache path so we assert against a known base dir.
	tempDir := t.TempDir()
	localChartCache := filepath.Join(tempDir, "charts")
	if err := os.MkdirAll(localChartCache, 0750); err != nil {
		t.Fatalf("Failed to create local chart cache directory: %v", err)
	}
	origCache := chartCache
	chartCache = localChartCache
	defer func() { chartCache = origCache }()

	const digest = "sha256:d1c2884a2ac2d2f80fb1bf384e45b4cc72669498ccd237843dcc63bfcac810a3"

	tests := []struct {
		name      string
		chartName string
		digest    string
		want      string
	}{
		{
			name:      "ValidDigest",
			chartName: "mychart",
			digest:    digest,
			// Must match the filename Helm writes for a digest pull.
			want: filepath.Join(localChartCache, helmDigestPullFilename("mychart", digest)),
		},
		{
			name:      "ChartNameWithPathComponentsIsBased",
			chartName: "charts/nested/mychart",
			digest:    digest,
			want:      filepath.Join(localChartCache, helmDigestPullFilename("mychart", digest)),
		},
		{
			name:      "EmptyChartName",
			chartName: "",
			digest:    digest,
			want:      "", // cannot construct a cache path
		},
		{
			name:      "DigestWithoutAlgoSeparator",
			chartName: "mychart",
			digest:    "deadbeef", // no "algo:hash" form
			want:      "",
		},
		{
			name:      "EmptyDigest",
			chartName: "mychart",
			digest:    "",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveCachedChartPathWithDigest(tt.chartName, tt.digest)
			if got != tt.want {
				t.Errorf("resolveCachedChartPathWithDigest(%q, %q) = %q, want %q",
					tt.chartName, tt.digest, got, tt.want)
			}
		})
	}
}

func TestEnsureChartCached(t *testing.T) {
	// Create a temporary directory for the chart cache
	tempDir := t.TempDir()
	localChartCache := filepath.Join(tempDir, "charts")
	if err := os.MkdirAll(localChartCache, 0750); err != nil {
		t.Fatalf("Failed to create local chart cache directory: %v", err)
	}

	// Override the global cache path for the duration of this test
	origCache := chartCache
	chartCache = localChartCache
	defer func() { chartCache = origCache }()

	// Create a mock client with a mock logger
	mockLog := &mockLogger{}
	mockClient := &client{
		log: mockLog,
	}

	// Test chart details
	testChartName := "test-chart"
	testChartVersion := "1.0.0"
	testChartContent := "test chart tarball content"
	testChartFileName := testChartName + "-" + testChartVersion + ".tgz"
	testChartPath := filepath.Join(localChartCache, testChartFileName)

	tests := []struct {
		name           string
		chartFilePath  string
		setupCache     func() error
		wantErr        bool
		wantPath       string
		validateResult func(t *testing.T, resultPath string)
	}{
		{
			name:          "ChartExistsInCache_RegularFile",
			chartFilePath: testChartPath,
			setupCache: func() error {
				return os.WriteFile(testChartPath, []byte(testChartContent), 0600)
			},
			wantErr:  false,
			wantPath: testChartPath,
			validateResult: func(t *testing.T, resultPath string) {
				if resultPath != testChartPath {
					t.Errorf("Expected path %q, got %q", testChartPath, resultPath)
				}
				// Verify file exists
				if _, err := os.Stat(resultPath); err != nil {
					t.Errorf("Chart file should exist at %q: %v", resultPath, err)
				}
			},
		},
		{
			name:          "ChartExistsInCache_Directory",
			chartFilePath: testChartPath,
			setupCache: func() error {
				return os.MkdirAll(testChartPath, 0750)
			},
			wantErr: true,
			validateResult: func(t *testing.T, resultPath string) {
				// Should return error when cached item is a directory
			},
		},
		{
			name:          "PathTraversalAttempt_SafelyHandled",
			chartFilePath: "../../../etc/passwd",
			setupCache: func() error {
				// Create a file with the sanitized name
				sanitizedName := filepath.Base("../../../etc/passwd")
				safeFile := filepath.Join(localChartCache, sanitizedName)
				return os.WriteFile(safeFile, []byte("safe content"), 0600)
			},
			wantErr: false,
			validateResult: func(t *testing.T, resultPath string) {
				// Verify the path is sanitized and within cache directory
				relPath, err := filepath.Rel(localChartCache, resultPath)
				if err != nil || filepath.IsAbs(relPath) || len(relPath) > 0 && relPath[0] == '.' {
					t.Errorf("Result path %q is not within cache directory %q", resultPath, localChartCache)
				}
				// Verify it's the base filename only
				expectedPath := filepath.Join(localChartCache, filepath.Base("../../../etc/passwd"))
				if resultPath != expectedPath {
					t.Errorf("Expected sanitized path %q, got %q", expectedPath, resultPath)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up cache directory before each test
			files, err := filepath.Glob(filepath.Join(localChartCache, "*"))
			if err == nil {
				for _, f := range files {
					os.RemoveAll(f)
				}
			}

			// Setup cache for this test case
			if tt.setupCache != nil {
				if err := tt.setupCache(); err != nil {
					t.Fatalf("Failed to setup cache: %v", err)
				}
			}

			// Call ensureChartCached
			gotPath, err := mockClient.ensureChartCached(
				tt.chartFilePath,
				"", // chartUrl
				testChartName,
				testChartVersion,
				"", // chartRepo
				"", // chartDigest
				&RepoCreds{},
			)

			// Check error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("ensureChartCached() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Validate result if no error expected
			if !tt.wantErr && tt.validateResult != nil {
				tt.validateResult(t, gotPath)
			}
		})
	}
}

func TestEnsureChartCached_PathTraversalProtection(t *testing.T) {
	// Create a temporary directory for the chart cache
	tempDir := t.TempDir()
	localChartCache := filepath.Join(tempDir, "charts")
	if err := os.MkdirAll(localChartCache, 0750); err != nil {
		t.Fatalf("Failed to create local chart cache directory: %v", err)
	}

	// Override the global cache path
	origCache := chartCache
	chartCache = localChartCache
	defer func() { chartCache = origCache }()

	mockLog := &mockLogger{}
	mockClient := &client{
		log: mockLog,
	}

	// Test various path traversal attempts
	pathTraversalAttempts := []string{
		"../../../etc/passwd",
		"../../secret.txt",
		"./../../hidden.tgz",
		"subdir/../../../etc/shadow",
		"chart/../../../root/.ssh/id_rsa",
	}

	for _, maliciousPath := range pathTraversalAttempts {
		t.Run("PathTraversal_"+maliciousPath, func(t *testing.T) {
			// Create a file with the sanitized (base) name in the cache
			sanitizedName := filepath.Base(maliciousPath)
			safeFile := filepath.Join(localChartCache, sanitizedName)
			if err := os.WriteFile(safeFile, []byte("safe content"), 0600); err != nil {
				t.Fatalf("Failed to create safe file: %v", err)
			}

			// Call ensureChartCached with the malicious path
			gotPath, err := mockClient.ensureChartCached(
				maliciousPath,
				"",
				"test",
				"1.0.0",
				"",
				"",
				&RepoCreds{},
			)

			if err != nil {
				t.Errorf("ensureChartCached() unexpected error: %v", err)
				return
			}

			// Verify the returned path is safe and within cache directory
			relPath, err := filepath.Rel(localChartCache, gotPath)
			if err != nil || filepath.IsAbs(relPath) || len(relPath) > 0 && relPath[0] == '.' {
				t.Errorf("Returned path %q escapes cache directory %q", gotPath, localChartCache)
			}

			// Verify it only uses the base filename
			expectedPath := filepath.Join(localChartCache, sanitizedName)
			if gotPath != expectedPath {
				t.Errorf("Expected sanitized path %q, got %q", expectedPath, gotPath)
			}
		})
	}
}

func TestResolveEffectiveDigest(t *testing.T) {
	const digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// resolveEffectiveDigest now reconciles the digest already extracted from
	// the chart URL with the spec digest. URL parsing is the caller's job.
	cases := map[string]struct {
		urlDigest  string
		specDigest string
		want       string
		wantErr    error
	}{
		"BothEmpty": {
			urlDigest:  "",
			specDigest: "",
			want:       "",
		},
		"URLOnly": {
			urlDigest:  digestA,
			specDigest: "",
			want:       digestA,
		},
		"SpecOnly": {
			urlDigest:  "",
			specDigest: digestA,
			want:       digestA,
		},
		"BothMatch": {
			urlDigest:  digestA,
			specDigest: digestA,
			want:       digestA,
		},
		"Conflict": {
			urlDigest:  digestA,
			specDigest: digestB,
			wantErr:    errors.Errorf(errDigestMismatchTmpl, digestA, digestB),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := resolveEffectiveDigest(tc.urlDigest, tc.specDigest)
			if diff := cmp.Diff(tc.wantErr, err, test.EquateErrors()); diff != "" {
				t.Fatalf("resolveEffectiveDigest() error: -want, +got:\n%s", diff)
			}
			if err != nil {
				return
			}
			if got != tc.want {
				t.Errorf("resolveEffectiveDigest() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPullAndLoadChart_Validation covers the upfront validation guards in
// PullAndLoadChart that fail fast before any chart pull is attempted: digest on
// a non-OCI source, and (when no URL is given) a missing chart name or
// repository. These paths return early, so a client without a configured pull
// client is sufficient.
func TestPullAndLoadChart_Validation(t *testing.T) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	mockClient := &client{log: &mockLogger{}}

	rel := func(c clusterv1beta1.ChartSpec) *clusterv1beta1.Release {
		return &clusterv1beta1.Release{
			Spec: clusterv1beta1.ReleaseSpec{
				ForProvider: clusterv1beta1.ReleaseParameters{Chart: c},
			},
		}
	}

	cases := map[string]struct {
		chart   clusterv1beta1.ChartSpec
		wantErr string
	}{
		"DigestOnNonOCIRepository": {
			chart: clusterv1beta1.ChartSpec{
				Repository: "https://charts.example.com",
				Name:       "mychart",
				Version:    "1.0.0",
				Digest:     digest,
			},
			wantErr: errDigestNotSupportedForNonOCI,
		},
		"DigestWithNoURLOrRepository": {
			chart: clusterv1beta1.ChartSpec{
				Name:   "mychart",
				Digest: digest,
			},
			wantErr: errDigestNotSupportedForNonOCI,
		},
		"NoURLMissingChartName": {
			// version set so we skip the "pull latest" branch and reach the
			// no-URL resolution branch that validates name/repository.
			chart: clusterv1beta1.ChartSpec{
				Repository: "https://charts.example.com",
				Version:    "1.0.0",
			},
			wantErr: errNoChartName,
		},
		"NoURLMissingRepository": {
			chart: clusterv1beta1.ChartSpec{
				Name:    "mychart",
				Version: "1.0.0",
			},
			wantErr: errNoChartRepository,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := mockClient.PullAndLoadChart(rel(tc.chart), &RepoCreds{})
			if err == nil {
				t.Fatalf("PullAndLoadChart() expected error %q, got nil", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Errorf("PullAndLoadChart() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestDigestCacheRoundTrip proves the store/lookup key agreement for
// digest-pinned charts: the path resolveCachedChartPathWithDigest constructs
// matches the filename Helm writes for a digest pull, so a chart pulled on one
// reconcile is found as a cache hit (without re-pulling) on the next. Helm's
// pull stores the tarball under <name>@sha256-<hash>.tgz; ensureChartCached
// must resolve the same path to a hit. Without this agreement a digest-pinned
// chart would be re-pulled on every reconcile.
func TestDigestCacheRoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	localChartCache := filepath.Join(tempDir, "charts")
	if err := os.MkdirAll(localChartCache, 0750); err != nil {
		t.Fatalf("Failed to create local chart cache directory: %v", err)
	}

	origCache := chartCache
	chartCache = localChartCache
	defer func() { chartCache = origCache }()

	mockClient := &client{log: &mockLogger{}}

	const chartName = "podinfo"
	const digest = "sha256:c56f4d760bc9da702f231f37fcec89c66b0993f0cb91446f86d014b133c6693f"

	// The lookup path for a digest-pinned chart.
	cachePath := resolveCachedChartPathWithDigest(chartName, digest)
	if cachePath == "" {
		t.Fatal("expected a non-empty cache path for a valid digest")
	}

	// It must equal the filename Helm produces for a digest pull, otherwise a
	// pulled chart would never be found here.
	wantPath := filepath.Join(localChartCache, helmDigestPullFilename(chartName, digest))
	if cachePath != wantPath {
		t.Fatalf("cache path %q does not match Helm's digest-pull filename %q", cachePath, wantPath)
	}

	// Simulate the chart already pulled and stored under that name on a prior
	// reconcile (Helm's pull + cache store preserves this filename).
	if err := os.WriteFile(cachePath, []byte("pretend-this-is-a-chart-tarball"), 0600); err != nil {
		t.Fatalf("Failed to write cached chart: %v", err)
	}

	// The next reconcile must resolve to a cache hit and not attempt a pull.
	gotPath, err := mockClient.ensureChartCached(cachePath, "", chartName, "", "", digest, &RepoCreds{})
	if err != nil {
		t.Fatalf("ensureChartCached() error: %v", err)
	}
	if gotPath != cachePath {
		t.Errorf("ensureChartCached() = %q, want cache hit at %q", gotPath, cachePath)
	}
}
