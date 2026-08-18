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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/downloader"
	"helm.sh/helm/v4/pkg/getter"
)

func TestDigestCacheKey(t *testing.T) {
	sum := sha256.Sum256([]byte("chart bytes"))
	hexSum := hex.EncodeToString(sum[:])

	cases := map[string]struct {
		digest   string
		wantKey  [sha256.Size]byte
		wantHave bool
	}{
		"Empty": {
			digest:   "",
			wantHave: false,
		},
		"BareHex": {
			digest:   hexSum,
			wantKey:  sum,
			wantHave: true,
		},
		"PrefixedHex": {
			digest:   "sha256:" + hexSum,
			wantKey:  sum,
			wantHave: true,
		},
		"MalformedHex": {
			digest:   "not-hex!",
			wantHave: false,
		},
		"WrongLength": {
			digest:   "abcdef",
			wantHave: false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			key, have := digestCacheKey(tc.digest)
			if have != tc.wantHave {
				t.Fatalf("digestCacheKey() have = %v, want %v", have, tc.wantHave)
			}
			if have && key != tc.wantKey {
				t.Errorf("digestCacheKey() key = %x, want %x", key, tc.wantKey)
			}
		})
	}
}

func testDownloader(t *testing.T) *downloader.ChartDownloader {
	t.Helper()
	return &downloader.ChartDownloader{
		Getters: getter.All(&cli.EnvSettings{ContentCache: t.TempDir()}),
		Cache:   &downloader.DiskCache{Root: t.TempDir()},
	}
}

func TestFetchURLToCache_HitSkipsDownload(t *testing.T) {
	content := []byte("cached chart bytes")
	sum := sha256.Sum256(content)

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	hc := &client{log: &mockLogger{}}
	dl := testDownloader(t)
	if _, err := dl.Cache.Put(sum, bytes.NewReader(content), downloader.CacheChart); err != nil {
		t.Fatalf("seeding cache: %v", err)
	}

	got, err := hc.fetchURLToCache(dl, srv.URL+"/mychart-1.0.0.tgz", "sha256:"+hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("fetchURLToCache() error: %v", err)
	}
	if requests != 0 {
		t.Errorf("fetchURLToCache() downloaded despite cache hit: %d requests", requests)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("reading cached chart: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("cached chart content mismatch")
	}
}

func TestFetchURLToCache_DownloadVerifiesAndCaches(t *testing.T) {
	content := []byte("fresh chart bytes")
	sum := sha256.Sum256(content)

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	hc := &client{log: &mockLogger{}}
	dl := testDownloader(t)

	got, err := hc.fetchURLToCache(dl, srv.URL+"/mychart-1.0.0.tgz", hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("fetchURLToCache() error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("fetchURLToCache() requests = %d, want 1", requests)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("reading cached chart: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("cached chart content mismatch")
	}

	// A second fetch with the advertised digest is a pure cache hit.
	if _, err := hc.fetchURLToCache(dl, srv.URL+"/mychart-1.0.0.tgz", hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("fetchURLToCache() second call error: %v", err)
	}
	if requests != 1 {
		t.Errorf("fetchURLToCache() second call requests = %d, want 1 (cache hit)", requests)
	}
}

func TestFetchURLToCache_RejectsDigestMismatch(t *testing.T) {
	advertised := sha256.Sum256([]byte("what the index claims"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("something else entirely"))
	}))
	defer srv.Close()

	hc := &client{log: &mockLogger{}}
	dl := testDownloader(t)

	url := srv.URL + "/mychart-1.0.0.tgz"
	_, err := hc.fetchURLToCache(dl, url, hex.EncodeToString(advertised[:]))
	if err == nil {
		t.Fatalf("fetchURLToCache() expected digest mismatch error, got nil")
	}
	want := fmt.Sprintf(errChartDigestMismatchTmpl, url)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("fetchURLToCache() error = %q, want %q", err.Error(), want)
	}
}

func TestFetchURLToCache_NoDigestDownloadsAndCachesByContent(t *testing.T) {
	content := []byte("digestless chart bytes")
	sum := sha256.Sum256(content)

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	hc := &client{log: &mockLogger{}}
	dl := testDownloader(t)

	if _, err := hc.fetchURLToCache(dl, srv.URL+"/mychart-1.0.0.tgz", ""); err != nil {
		t.Fatalf("fetchURLToCache() error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("fetchURLToCache() requests = %d, want 1", requests)
	}
	// Without an advertised digest there is nothing to look up before the
	// download, but the content still lands in the cache under its hash.
	if _, err := dl.Cache.Get(sum, downloader.CacheChart); err != nil {
		t.Errorf("downloaded chart not cached by content hash: %v", err)
	}
}

func TestCachedChart(t *testing.T) {
	content := []byte("probed chart bytes")
	sum := sha256.Sum256(content)

	hc := &client{log: &mockLogger{}}
	dl := testDownloader(t)
	if _, err := dl.Cache.Put(sum, bytes.NewReader(content), downloader.CacheChart); err != nil {
		t.Fatalf("seeding cache: %v", err)
	}

	cases := map[string]struct {
		digest  string
		wantHit bool
	}{
		"Hit": {
			digest:  "sha256:" + hex.EncodeToString(sum[:]),
			wantHit: true,
		},
		"MissUnknownDigest": {
			digest:  "sha256:" + strings.Repeat("0", 64),
			wantHit: false,
		},
		"EmptyDigest": {
			digest:  "",
			wantHit: false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := hc.cachedChart(dl, tc.digest)
			if ok != tc.wantHit {
				t.Fatalf("cachedChart() hit = %v, want %v", ok, tc.wantHit)
			}
			if !ok {
				return
			}
			data, err := os.ReadFile(got)
			if err != nil {
				t.Fatalf("reading cached chart: %v", err)
			}
			if !bytes.Equal(data, content) {
				t.Errorf("cached chart content mismatch")
			}
		})
	}
}

type stubGetter struct {
	content  []byte
	requests *int
}

func (g *stubGetter) Get(_ string, _ ...getter.Option) (*bytes.Buffer, error) {
	*g.requests++
	return bytes.NewBuffer(g.content), nil
}

func TestFetchOCIRefToCache_KeysByManifestDigest(t *testing.T) {
	content := []byte("oci chart tarball bytes")
	// The pin is the OCI manifest digest, which is NOT the hash of the
	// tarball; entries must still be stored and found under it.
	manifestDigest := "sha256:" + strings.Repeat("ab", 32)
	key, _ := digestCacheKey(manifestDigest)

	requests := 0
	hc := &client{log: &mockLogger{}}
	dl := testDownloader(t)
	dl.Getters = getter.Providers{{
		Schemes: []string{"oci"},
		New: func(_ ...getter.Option) (getter.Getter, error) {
			return &stubGetter{content: content, requests: &requests}, nil
		},
	}}

	ref := "oci://registry.example.com/charts/mychart@" + manifestDigest

	got, err := hc.fetchOCIRefToCache(dl, ref, manifestDigest)
	if err != nil {
		t.Fatalf("fetchOCIRefToCache() error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("fetchOCIRefToCache() requests = %d, want 1", requests)
	}
	if _, err := dl.Cache.Get(key, downloader.CacheChart); err != nil {
		t.Fatalf("chart not cached under the manifest digest key: %v", err)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("reading cached chart: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("cached chart content mismatch")
	}

	// The second pull of the same pin is served from the cache.
	if _, err := hc.fetchOCIRefToCache(dl, ref, manifestDigest); err != nil {
		t.Fatalf("fetchOCIRefToCache() second call error: %v", err)
	}
	if requests != 1 {
		t.Errorf("fetchOCIRefToCache() second call requests = %d, want 1 (cache hit)", requests)
	}
}
