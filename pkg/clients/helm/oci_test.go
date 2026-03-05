package helm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
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

func TestResolveCachedChartPathWithDigest(t *testing.T) {
	// Instead of writing into the global /tmp/charts cache we create a
	// temporary directory for this test and override the package variable so
	// that the helper under test uses it. t.TempDir() will clean itself up once
	// the test finishes, so no manual removal is necessary.
	tempDir := t.TempDir()
	localChartCache := filepath.Join(tempDir, "charts")
	if err := os.MkdirAll(localChartCache, 0750); err != nil {
		t.Fatalf("Failed to create local chart cache directory: %v", err)
	}

	// patch the global cache path for the duration of this test
	origCache := chartCache
	chartCache = localChartCache
	defer func() { chartCache = origCache }()

	testChartName := "test-chart-digest"
	testChartVersion := "9.9.9"
	testChartContent := "test chart content for digest validation"

	// compute path using the (now-overridden) cache variable
	testChartFile := resolveChartFilePath(testChartName, testChartVersion)

	if err := os.WriteFile(testChartFile, []byte(testChartContent), 0600); err != nil {
		t.Fatalf("Failed to create test chart file: %v", err)
	}

	// Compute the actual digest of the test file
	correctDigest, err := computeFileDigest(testChartFile)
	if err != nil {
		t.Fatalf("Failed to compute digest: %v", err)
	}

	wrongDigest := "sha256:0000000000000000000000000000000000000000000000000000000000000000"

	// Create a mock logger that captures debug messages
	mockLog := &mockLogger{}

	tests := []struct {
		name           string
		chartName      string
		chartVersion   string
		expectedDigest string
		wantEmpty      bool // Whether we expect an empty path (cache miss)
		wantLogCount   int  // Number of debug log calls expected
	}{
		{
			name:           "DigestMatches",
			chartName:      testChartName,
			chartVersion:   testChartVersion,
			expectedDigest: correctDigest,
			wantEmpty:      false, // Cache hit
			wantLogCount:   1,     // "Cache hit: digest matches"
		},
		{
			name:           "DigestMismatch",
			chartName:      testChartName,
			chartVersion:   testChartVersion,
			expectedDigest: wrongDigest,
			wantEmpty:      true, // Cache miss
			wantLogCount:   1,    // "Cache digest mismatch"
		},
		{
			name:           "FileNotFound",
			chartName:      "nonexistent-chart",
			chartVersion:   "1.0.0",
			expectedDigest: correctDigest,
			wantEmpty:      true, // Cache miss
			wantLogCount:   1,    // "failed to verify cached digest"
		},
		{
			name:           "EmptyChartName",
			chartName:      "",
			chartVersion:   testChartVersion,
			expectedDigest: correctDigest,
			wantEmpty:      true, // Cache miss
			wantLogCount:   0,    // No logs, early return
		},
		{
			name:           "EmptyChartVersion",
			chartName:      testChartName,
			chartVersion:   "",
			expectedDigest: correctDigest,
			wantEmpty:      true, // Cache miss
			wantLogCount:   0,    // No logs, early return
		},
		{
			name:           "EmptyNameAndVersion",
			chartName:      "",
			chartVersion:   "",
			expectedDigest: correctDigest,
			wantEmpty:      true, // Cache miss
			wantLogCount:   0,    // No logs, early return
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset mock logger for each test
			mockLog = &mockLogger{}

			// Call the function
			gotPath := resolveCachedChartPathWithDigest(tt.chartName, tt.chartVersion, tt.expectedDigest, mockLog)

			// Check if the result matches expectations (empty vs non-empty)
			gotEmpty := gotPath == ""
			if gotEmpty != tt.wantEmpty {
				t.Errorf("resolveCachedChartPathWithDigest() returned empty=%v, want empty=%v (path=%q)",
					gotEmpty, tt.wantEmpty, gotPath)
			}

			// For cache hits, verify the path is what we expect
			if !tt.wantEmpty && gotPath != testChartFile {
				t.Errorf("resolveCachedChartPathWithDigest() = %q, want %q", gotPath, testChartFile)
			}

			// Check log call count
			if mockLog.debugCallCount != tt.wantLogCount {
				t.Errorf("Debug log call count = %d, want %d", mockLog.debugCallCount, tt.wantLogCount)
			}
		})
	}
}

func TestCacheDigestMatches(t *testing.T) {
	tmpDir := t.TempDir()
	testContent := "test content"
	testFile := filepath.Join(tmpDir, "test.tgz")

	if err := os.WriteFile(testFile, []byte(testContent), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Compute the correct digest
	correctDigest, err := computeFileDigest(testFile)
	if err != nil {
		t.Fatalf("Failed to compute digest: %v", err)
	}

	tests := []struct {
		name       string
		cachedPath string
		expected   string
		wantMatch  bool
		wantDigest string
		wantErr    bool
	}{
		{
			name:       "Match",
			cachedPath: testFile,
			expected:   correctDigest,
			wantMatch:  true,
			wantDigest: correctDigest,
			wantErr:    false,
		},
		{
			name:       "Mismatch",
			cachedPath: testFile,
			expected:   "sha256:0000000000000000000000000000000000000000000000000000000000000000",
			wantMatch:  false,
			wantDigest: correctDigest,
			wantErr:    false,
		},
		{
			name:       "FileNotFound",
			cachedPath: "/nonexistent/file.tgz",
			expected:   correctDigest,
			wantMatch:  false,
			wantDigest: "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMatch, gotDigest, gotErr := cacheDigestMatches(tt.cachedPath, tt.expected)

			if (gotErr != nil) != tt.wantErr {
				t.Errorf("cacheDigestMatches() error = %v, wantErr %v", gotErr, tt.wantErr)
				return
			}

			if gotMatch != tt.wantMatch {
				t.Errorf("cacheDigestMatches() match = %v, want %v", gotMatch, tt.wantMatch)
			}

			if gotDigest != tt.wantDigest {
				t.Errorf("cacheDigestMatches() digest = %v, want %v", gotDigest, tt.wantDigest)
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
