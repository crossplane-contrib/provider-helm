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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-containerregistry/pkg/registry"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/downloader"
	helmregistry "helm.sh/helm/v4/pkg/registry"
	repo "helm.sh/helm/v4/pkg/repo/v1"
	"k8s.io/client-go/rest"
)

// packageChart creates a chart with the given name and version and saves its
// tarball into dir.
func packageChart(t *testing.T, name, version, dir string) string {
	t.Helper()
	chartPath, err := chartutil.Create(name, t.TempDir())
	if err != nil {
		t.Fatalf("chartutil.Create(...): unexpected error: %v", err)
	}
	c := mustLoadChart(t, chartPath)
	c.Metadata.Version = version
	tgzPath, err := chartutil.Save(c, dir)
	if err != nil {
		t.Fatalf("chartutil.Save(...): unexpected error: %v", err)
	}
	return tgzPath
}

// pushChart packages a chart with the given name and version, pushes it to
// <host>/charts with rc and returns its manifest digest.
func pushChart(t *testing.T, rc *helmregistry.Client, host, name, version string) string {
	t.Helper()
	tgz, err := os.ReadFile(packageChart(t, name, version, t.TempDir()))
	if err != nil {
		t.Fatalf("reading packaged chart: %v", err)
	}
	result, err := rc.Push(tgz, host+"/charts/"+name+":"+version)
	if err != nil {
		t.Fatalf("Push(...) to test registry: unexpected error: %v", err)
	}
	return result.Manifest.Digest
}

// newPullClient returns a client built the way a Release gets one, through
// NewClient, with its content cache at cacheDir and every helm and docker
// location redirected to temporary directories.
func newPullClient(t *testing.T, cacheDir string, apply ...ArgsApplier) *client {
	t.Helper()
	isolateRegistryCredentials(t)
	t.Setenv("HELM_CACHE_HOME", t.TempDir())

	// Init doesn't dial the Kubernetes API, so a non-dialable host is fine.
	restConfig := &rest.Config{Host: "http://127.0.0.1:0"}
	appliers := append([]ArgsApplier{func(a *Args) { a.Namespace = "default" }}, apply...)
	c, err := NewClient(logging.NewNopLogger(), restConfig, appliers...)
	if err != nil {
		t.Fatalf("NewClient(...): unexpected error: %v", err)
	}
	cc, ok := c.(*client)
	if !ok {
		t.Fatalf("NewClient(...) did not return a *client")
	}
	cc.contentCache = cacheDir
	return cc
}

// chartID loads the chart tarball at path and returns <name>-<version>.
func chartID(t *testing.T, path string) string {
	t.Helper()
	c, err := loader.Load(path)
	if err != nil {
		t.Fatalf("loader.Load(%q): unexpected error: %v", path, err)
	}
	return c.Metadata.Name + "-" + c.Metadata.Version
}

func plainHTTP(a *Args) { a.PlainHTTP = true }

// TestPullChart_OCIDigest pulls digest-pinned charts from a real in-process
// registry. A digest selects the chart on its own; a tag or version next to it
// only validates, as it does in helm: it may be missing from the registry, but
// it must not resolve to another digest.
func TestPullChart_OCIDigest(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := srv.Listener.Addr().String()
	repository := "oci://" + host + "/charts"

	pushClient, err := helmregistry.NewClient(helmregistry.ClientOptPlainHTTP())
	if err != nil {
		t.Fatalf("helmregistry.NewClient(...) for push setup: unexpected error: %v", err)
	}
	digestV1 := pushChart(t, pushClient, host, "testchart", "0.1.0")
	digestV2 := pushChart(t, pushClient, host, "testchart", "0.2.0")

	type args struct {
		url        string
		repository string
		name       string
		version    string
		digest     string
	}
	type want struct {
		Chart  string
		Failed bool
	}
	cases := map[string]struct {
		args args
		want want
	}{
		"RepositoryDigestOnly": {
			args: args{repository: repository, name: "testchart", digest: digestV1},
			want: want{Chart: "testchart-0.1.0"},
		},
		"RepositoryDigestWithItsVersion": {
			args: args{repository: repository, name: "testchart", version: "0.1.0", digest: digestV1},
			want: want{Chart: "testchart-0.1.0"},
		},
		"RepositoryDigestWithUntaggedVersion": {
			args: args{repository: repository, name: "testchart", version: "9.9.9", digest: digestV1},
			want: want{Chart: "testchart-0.1.0"},
		},
		"RepositoryDigestWithVersionOfAnotherDigest": {
			args: args{repository: repository, name: "testchart", version: "0.2.0", digest: digestV1},
			want: want{Failed: true},
		},
		"URLDigestOnly": {
			args: args{url: repository + "/testchart@" + digestV1},
			want: want{Chart: "testchart-0.1.0"},
		},
		"URLDigestWithItsTag": {
			args: args{url: repository + "/testchart:0.1.0@" + digestV1},
			want: want{Chart: "testchart-0.1.0"},
		},
		"URLDigestWithTagOfAnotherDigest": {
			args: args{url: repository + "/testchart:0.2.0@" + digestV1},
			want: want{Failed: true},
		},
		"URLTagWithSpecDigestOfAnotherVersion": {
			args: args{url: repository + "/testchart:0.1.0", digest: digestV2},
			want: want{Failed: true},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			hc := newPullClient(t, t.TempDir(), plainHTTP)

			got := want{}
			path, err := hc.pullChart(tc.args.url, tc.args.name, tc.args.version, tc.args.repository, tc.args.digest, &RepoCreds{})
			if err != nil {
				t.Logf("pullChart(...): %v", err)
				got.Failed = true
			} else {
				got.Chart = chartID(t, path)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("pullChart(...): -want, +got:\n%s", diff)
			}
		})
	}
}

// TestPullChart_OCIDigestCacheHoldsPinnedChart: a cache entry keyed by a
// digest is served to every Release pinning that digest, so no spec may fill
// it with another chart. Helm pulls by tag when a reference carries both a tag
// and a digest, which makes a tag smuggled into the chart name or the URL the
// way in.
func TestPullChart_OCIDigestCacheHoldsPinnedChart(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := srv.Listener.Addr().String()
	repository := "oci://" + host + "/charts"

	pushClient, err := helmregistry.NewClient(helmregistry.ClientOptPlainHTTP())
	if err != nil {
		t.Fatalf("helmregistry.NewClient(...) for push setup: unexpected error: %v", err)
	}
	pinned := pushChart(t, pushClient, host, "goodchart", "0.1.0")
	pushChart(t, pushClient, host, "otherchart", "6.6.6")

	type args struct {
		url        string
		repository string
		name       string
		digest     string
	}
	type want struct {
		OtherChartPullFailed bool
		PinnedChart          string
	}
	cases := map[string]struct {
		args args
		want want
	}{
		"TagInRepositoryChartName": {
			args: args{repository: repository, name: "otherchart:6.6.6", digest: pinned},
			want: want{OtherChartPullFailed: true, PinnedChart: "goodchart-0.1.0"},
		},
		"TagInURLBeforeVersion": {
			args: args{url: repository + "/otherchart:6.6.6:x", digest: pinned},
			want: want{OtherChartPullFailed: true, PinnedChart: "goodchart-0.1.0"},
		},
		"TagInURLBeforeVersionWithURLDigest": {
			args: args{url: repository + "/otherchart:6.6.6:x@" + pinned},
			want: want{OtherChartPullFailed: true, PinnedChart: "goodchart-0.1.0"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cacheDir := t.TempDir()

			got := want{}
			_, err := newPullClient(t, cacheDir, plainHTTP).pullChart(tc.args.url, tc.args.name, "", tc.args.repository, tc.args.digest, &RepoCreds{})
			if err != nil {
				t.Logf("pullChart(...) of the other chart: %v", err)
				got.OtherChartPullFailed = true
			}

			path, err := newPullClient(t, cacheDir, plainHTTP).pullChart("", "goodchart", "", repository, pinned, &RepoCreds{})
			if err != nil {
				t.Fatalf("pullChart(...) of the pinned chart: unexpected error: %v", err)
			}
			got.PinnedChart = chartID(t, path)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("pullChart(...): -want, +got:\n%s", diff)
			}
		})
	}
}

// TestPullChart_OCIDigestServedFromCache: a digest-pinned chart is stored
// under its manifest digest, which is not the hash of the tarball, and the
// next pull of the same pin does not contact the registry.
func TestPullChart_OCIDigestServedFromCache(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	reg := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		reg.ServeHTTP(w, r)
	}))
	defer srv.Close()
	host := srv.Listener.Addr().String()
	repository := "oci://" + host + "/charts"

	pushClient, err := helmregistry.NewClient(helmregistry.ClientOptPlainHTTP())
	if err != nil {
		t.Fatalf("helmregistry.NewClient(...) for push setup: unexpected error: %v", err)
	}
	digest := pushChart(t, pushClient, host, "testchart", "0.1.0")

	type args struct {
		url        string
		repository string
		name       string
		digest     string
	}
	type want struct {
		Chart              string
		CachedUnderDigest  bool
		FirstPullRequested bool
		SecondPullRequests int
	}
	cases := map[string]struct {
		args args
		want want
	}{
		"Repository": {
			args: args{repository: repository, name: "testchart", digest: digest},
			want: want{Chart: "testchart-0.1.0", CachedUnderDigest: true, FirstPullRequested: true},
		},
		"URL": {
			args: args{url: repository + "/testchart@" + digest},
			want: want{Chart: "testchart-0.1.0", CachedUnderDigest: true, FirstPullRequested: true},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cacheDir := t.TempDir()
			count := func() int {
				mu.Lock()
				defer mu.Unlock()
				return requests
			}

			before := count()
			if _, err := newPullClient(t, cacheDir, plainHTTP).pullChart(tc.args.url, tc.args.name, "", tc.args.repository, tc.args.digest, &RepoCreds{}); err != nil {
				t.Fatalf("pullChart(...): unexpected error: %v", err)
			}
			afterFirst := count()

			path, err := newPullClient(t, cacheDir, plainHTTP).pullChart(tc.args.url, tc.args.name, "", tc.args.repository, tc.args.digest, &RepoCreds{})
			if err != nil {
				t.Fatalf("pullChart(...) second pull: unexpected error: %v", err)
			}

			key, _ := digestCacheKey(digest)
			_, cacheErr := (&downloader.DiskCache{Root: cacheDir}).Get(key, downloader.CacheChart)
			got := want{
				Chart:              chartID(t, path),
				CachedUnderDigest:  cacheErr == nil,
				FirstPullRequested: afterFirst > before,
				SecondPullRequests: count() - afterFirst,
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("pullChart(...): -want, +got:\n%s", diff)
			}
		})
	}
}

// serveChartRepository serves a classic chart repository holding one tarball
// of chart name per version and returns its URL.
func serveChartRepository(t *testing.T, name string, versions []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, v := range versions {
		packageChart(t, name, v, dir)
	}
	idx, err := repo.IndexDirectory(dir, "")
	if err != nil {
		t.Fatalf("repo.IndexDirectory(...): unexpected error: %v", err)
	}
	idx.SortEntries()
	if err := idx.WriteFile(filepath.Join(dir, "index.yaml"), 0o600); err != nil {
		t.Fatalf("writing index.yaml: %v", err)
	}

	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)

	return srv.URL
}

// TestPullChart_ClassicRepository resolves charts through a real repository
// index: the spec version selects among the index entries as it does in helm,
// including the devel range, the only way to get a pre-release without naming
// it.
func TestPullChart_ClassicRepository(t *testing.T) {
	released := []string{"1.0.0", "1.1.0", "2.0.0-rc.1"}

	type args struct {
		versions []string
		version  string
	}
	type want struct {
		Chart string
	}
	cases := map[string]struct {
		args args
		want want
	}{
		"ExactVersion": {
			args: args{versions: released, version: "1.0.0"},
			want: want{Chart: "mychart-1.0.0"},
		},
		"NoVersionSelectsLatestStable": {
			args: args{versions: released},
			want: want{Chart: "mychart-1.1.0"},
		},
		"RangeSelectsHighestMatch": {
			args: args{versions: released, version: "<1.1.0"},
			want: want{Chart: "mychart-1.0.0"},
		},
		"DevelRangeSelectsPreRelease": {
			args: args{versions: released, version: devel},
			want: want{Chart: "mychart-2.0.0-rc.1"},
		},
		"DevelRangeOnPreReleaseOnlyRepository": {
			args: args{versions: []string{"0.1.0-dev.1", "0.1.0-dev.2"}, version: devel},
			want: want{Chart: "mychart-0.1.0-dev.2"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			repoURL := serveChartRepository(t, "mychart", tc.args.versions)

			path, err := newPullClient(t, t.TempDir()).pullChart("", "mychart", tc.args.version, repoURL, "", &RepoCreds{})
			if err != nil {
				t.Fatalf("pullChart(...): unexpected error: %v", err)
			}

			got := want{Chart: chartID(t, path)}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("pullChart(...): -want, +got:\n%s", diff)
			}
		})
	}
}
