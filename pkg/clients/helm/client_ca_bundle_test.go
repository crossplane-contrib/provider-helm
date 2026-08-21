/*
Copyright 2020 The Crossplane Authors.

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
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/google/go-containerregistry/pkg/registry"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/getter"
	helmregistry "helm.sh/helm/v4/pkg/registry"
	"k8s.io/client-go/rest"
)

func mustLoadChart(t *testing.T, dir string) *chart.Chart {
	t.Helper()
	c, err := loader.LoadDir(dir)
	if err != nil {
		t.Fatalf("loader.LoadDir(%q): unexpected error: %v", dir, err)
	}
	return c
}

// testCA is a self-signed CA plus a leaf certificate it issued for
// 127.0.0.1, generated fresh per test. Real key material, real TLS
// handshakes against it - not mocked.
type testCA struct {
	caPEM    []byte
	leafCert tls.Certificate
}

func newTestCA(t *testing.T) testCA {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test root CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	leafKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER})

	leafCert, err := tls.X509KeyPair(leafPEM, leafKeyPEM)
	if err != nil {
		t.Fatalf("build leaf tls.Certificate: %v", err)
	}

	return testCA{caPEM: caPEM, leafCert: leafCert}
}

func TestHTTPClientTrustingCABundle(t *testing.T) {
	ca := newTestCA(t)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{ca.leafCert}}
	srv.StartTLS()
	defer srv.Close()

	t.Run("DefaultClientRejectsSelfSignedCA", func(t *testing.T) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatalf("http.NewRequest(...): unexpected error: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			defer resp.Body.Close()
		}
		if err == nil {
			t.Fatal("expected default client to reject the self-signed CA, got no error")
		}
	})

	t.Run("ClientTrustingCABundleSucceeds", func(t *testing.T) {
		hc, err := httpClientTrustingCABundle(logging.NewNopLogger(), ca.caPEM)
		if err != nil {
			t.Fatalf("httpClientTrustingCABundle(...): unexpected error: %v", err)
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatalf("http.NewRequest(...): unexpected error: %v", err)
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatalf("Do(...) with trusted CA: unexpected error: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Do(...): got status %d, want 200", resp.StatusCode)
		}
	})

	t.Run("InvalidPEMReturnsError", func(t *testing.T) {
		_, err := httpClientTrustingCABundle(logging.NewNopLogger(), []byte("not a pem certificate"))
		if err == nil {
			t.Fatal("expected an error for invalid PEM input, got nil")
		}
	})
}

// TestWriteCABundleToFile covers content-addressing: this runs inside
// NewClient, which runs on every reconcile via Connect, so calling it
// repeatedly with the SAME content (the common case - a Release's caBundle
// rarely changes between reconciles) must reuse one file rather than
// accumulating a new one that nothing ever deletes.
func TestWriteCABundleToFile(t *testing.T) {
	caBundleCacheDir = t.TempDir()
	ca := newTestCA(t)
	otherCA := newTestCA(t)

	path1, err := writeCABundleToFile(ca.caPEM)
	if err != nil {
		t.Fatalf("writeCABundleToFile(...): unexpected error: %v", err)
	}

	got, err := os.ReadFile(path1)
	if err != nil {
		t.Fatalf("reading back written CA bundle: %v", err)
	}
	if !bytes.Equal(got, ca.caPEM) {
		t.Fatalf("written CA bundle content mismatch: got %q, want %q", got, ca.caPEM)
	}

	t.Run("SameContentReusesTheSameFile", func(t *testing.T) {
		path2, err := writeCABundleToFile(ca.caPEM)
		if err != nil {
			t.Fatalf("writeCABundleToFile(...) second call: unexpected error: %v", err)
		}
		if path1 != path2 {
			t.Fatalf("expected repeated calls with the same content to reuse one file, got distinct paths %q and %q", path1, path2)
		}
	})

	t.Run("DifferentContentGetsADifferentFile", func(t *testing.T) {
		path3, err := writeCABundleToFile(otherCA.caPEM)
		if err != nil {
			t.Fatalf("writeCABundleToFile(...) for different content: unexpected error: %v", err)
		}
		if path3 == path1 {
			t.Fatalf("expected different content to produce a different path, got the same path %q for both", path1)
		}
		got3, err := os.ReadFile(path3)
		if err != nil {
			t.Fatalf("reading back second CA bundle: %v", err)
		}
		if !bytes.Equal(got3, otherCA.caPEM) {
			t.Fatalf("second CA bundle content mismatch: got %q, want %q", got3, otherCA.caPEM)
		}
	})
}

// TestWriteCABundleToFile_SweepsStaleEntries proves the opportunistic cleanup
// actually removes files past caBundleTTL - the mechanism that bounds total
// accumulation now that files are reused instead of created fresh every
// reconcile.
func TestWriteCABundleToFile_SweepsStaleEntries(t *testing.T) {
	caBundleCacheDir = t.TempDir()
	originalTTL := caBundleTTL
	caBundleTTL = 50 * time.Millisecond
	defer func() { caBundleTTL = originalTTL }()

	stale := newTestCA(t)
	fresh := newTestCA(t)

	stalePath, err := writeCABundleToFile(stale.caPEM)
	if err != nil {
		t.Fatalf("writeCABundleToFile(...) for stale content: unexpected error: %v", err)
	}

	time.Sleep(2 * caBundleTTL)

	// Writing a second, different bundle triggers the sweep as a side effect.
	if _, err := writeCABundleToFile(fresh.caPEM); err != nil {
		t.Fatalf("writeCABundleToFile(...) for fresh content: unexpected error: %v", err)
	}

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("expected stale CA bundle file %q to have been swept, stat error: %v", stalePath, err)
	}
}

// TestWriteCABundleToFile_RecreatesFileRemovedBetweenStatAndChtimes covers
// the race a concurrent sweep can cause: the file exists at Stat time (so
// the "already present" branch is taken) but is gone by the time Chtimes
// runs. That should self-heal by recreating the file, not fail the caller.
func TestWriteCABundleToFile_RecreatesFileRemovedBetweenStatAndChtimes(t *testing.T) {
	caBundleCacheDir = t.TempDir()
	ca := newTestCA(t)

	// Establish the file so the Stat in writeCABundleToFile finds it.
	path, err := writeCABundleToFile(ca.caPEM)
	if err != nil {
		t.Fatalf("writeCABundleToFile(...): unexpected error: %v", err)
	}

	originalChtimesFn := chtimesFn
	chtimesFn = func(name string, atime, mtime time.Time) error {
		// Simulate a concurrent sweep having removed it in the window
		// between Stat and this call, then defer to the real Chtimes so
		// any OTHER path this test doesn't care about still behaves
		// normally.
		if name == path {
			if err := os.Remove(name); err != nil {
				t.Fatalf("removing %q to simulate a concurrent sweep: %v", name, err)
			}
		}
		return os.Chtimes(name, atime, mtime)
	}
	defer func() { chtimesFn = originalChtimesFn }()

	gotPath, err := writeCABundleToFile(ca.caPEM)
	if err != nil {
		t.Fatalf("writeCABundleToFile(...) after simulated concurrent removal: unexpected error: %v", err)
	}
	if gotPath != path {
		t.Fatalf("writeCABundleToFile(...): got path %q, want %q", gotPath, path)
	}
	got, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("reading recreated CA bundle: %v", err)
	}
	if !bytes.Equal(got, ca.caPEM) {
		t.Fatalf("recreated CA bundle content mismatch: got %q, want %q", got, ca.caPEM)
	}
}

// TestSweepStaleCABundles_SkipsDirectories proves the e.IsDir() check in
// sweepStaleCABundles actually does something - a stale-looking directory
// under caBundleCacheDir must survive the sweep, not be removed.
func TestSweepStaleCABundles_SkipsDirectories(t *testing.T) {
	caBundleCacheDir = t.TempDir()

	staleDir := filepath.Join(caBundleCacheDir, "not-a-bundle")
	if err := os.Mkdir(staleDir, 0750); err != nil {
		t.Fatalf("creating test directory: %v", err)
	}
	longAgo := time.Now().Add(-2 * caBundleTTL)
	if err := os.Chtimes(staleDir, longAgo, longAgo); err != nil {
		t.Fatalf("setting old mtime on test directory: %v", err)
	}

	sweepStaleCABundles(time.Now())

	if _, err := os.Stat(staleDir); err != nil {
		t.Fatalf("expected directory %q to survive the sweep, stat error: %v", staleDir, err)
	}
}

// TestReadSystemCABundle covers the lookup order: $SSL_CERT_FILE first, then
// systemCACandidates, returning nothing if neither is readable.
func TestReadSystemCABundle(t *testing.T) {
	t.Run("FindsFirstReadableCandidate", func(t *testing.T) {
		dir := t.TempDir()
		content := []byte("candidate bundle content")
		candidatePath := filepath.Join(dir, "ca-bundle.crt")
		if err := os.WriteFile(candidatePath, content, 0600); err != nil {
			t.Fatalf("writing candidate file: %v", err)
		}

		originalCandidates := systemCACandidates
		systemCACandidates = []string{filepath.Join(dir, "does-not-exist.crt"), candidatePath}
		defer func() { systemCACandidates = originalCandidates }()
		t.Setenv("SSL_CERT_FILE", "")

		got, path := readSystemCABundle()
		if path != candidatePath {
			t.Fatalf("readSystemCABundle(): path = %q, want %q", path, candidatePath)
		}
		if !bytes.Equal(got, content) {
			t.Fatalf("readSystemCABundle(): content = %q, want %q", got, content)
		}
	})

	t.Run("SSLCertFileTakesPriorityOverCandidates", func(t *testing.T) {
		dir := t.TempDir()
		candidateContent := []byte("candidate content")
		candidatePath := filepath.Join(dir, "ca-bundle.crt")
		if err := os.WriteFile(candidatePath, candidateContent, 0600); err != nil {
			t.Fatalf("writing candidate file: %v", err)
		}

		sslCertContent := []byte("SSL_CERT_FILE content")
		sslCertPath := filepath.Join(dir, "ssl-cert-file.crt")
		if err := os.WriteFile(sslCertPath, sslCertContent, 0600); err != nil {
			t.Fatalf("writing SSL_CERT_FILE: %v", err)
		}

		originalCandidates := systemCACandidates
		systemCACandidates = []string{candidatePath}
		defer func() { systemCACandidates = originalCandidates }()
		t.Setenv("SSL_CERT_FILE", sslCertPath)

		got, path := readSystemCABundle()
		if path != sslCertPath {
			t.Fatalf("readSystemCABundle(): path = %q, want %q (SSL_CERT_FILE should take priority)", path, sslCertPath)
		}
		if !bytes.Equal(got, sslCertContent) {
			t.Fatalf("readSystemCABundle(): content = %q, want %q", got, sslCertContent)
		}
	})

	t.Run("NothingReadableReturnsEmpty", func(t *testing.T) {
		dir := t.TempDir()
		originalCandidates := systemCACandidates
		systemCACandidates = []string{filepath.Join(dir, "does-not-exist.crt")}
		defer func() { systemCACandidates = originalCandidates }()
		t.Setenv("SSL_CERT_FILE", "")

		got, path := readSystemCABundle()
		if got != nil || path != "" {
			t.Fatalf("readSystemCABundle(): got (%q, %q), want (nil, \"\")", got, path)
		}
	})
}

// TestNewClient_CABundleWiring is a white-box test (same package) verifying
// that a supplied Args.CABundle is actually plumbed into the underlying Helm
// action structs' CaFile field with the right content - the part of this
// feature that's specific to this provider's wiring, as opposed to Helm's
// own CaFile handling, which is Helm's to test.
func TestNewClient_CABundleWiring(t *testing.T) {
	ca := newTestCA(t)
	// NewClient's action.Configuration.Init doesn't dial anything - it just
	// builds a REST client getter and a storage driver lazily, so a
	// non-dialable host is safe here; this test never calls an action that
	// would actually reach the Kubernetes API.
	restConfig := &rest.Config{Host: "http://127.0.0.1:0"}

	// Neutralize the real system CA bundle lookup so CaFile's content is
	// deterministic across machines/CI - without this, a Linux runner with
	// a real /etc/ssl/certs/ca-certificates.crt would make CaFile contain
	// more than just ca.caPEM, while a Mac dev machine without one wouldn't.
	caBundleCacheDir = t.TempDir()
	originalCandidates := systemCACandidates
	systemCACandidates = nil
	t.Cleanup(func() { systemCACandidates = originalCandidates })
	t.Setenv("SSL_CERT_FILE", "")

	t.Run("CABundleSetWritesCaFile", func(t *testing.T) {
		c, err := NewClient(logging.NewNopLogger(), restConfig, func(a *Args) {
			a.Namespace = "default"
			a.CABundle = ca.caPEM
		})
		if err != nil {
			t.Fatalf("NewClient(...): unexpected error: %v", err)
		}
		cc, ok := c.(*client)
		if !ok {
			t.Fatalf("NewClient(...) did not return a *client")
		}

		for name, caFile := range map[string]string{
			"pullClient":    cc.pullClient.CaFile,
			"installClient": cc.installClient.CaFile,
			"upgradeClient": cc.upgradeClient.CaFile,
		} {
			if caFile == "" {
				t.Errorf("%s.CaFile is empty, want a path to the written CA bundle", name)
				continue
			}
			got, err := os.ReadFile(caFile)
			if err != nil {
				t.Errorf("%s.CaFile = %q: failed to read: %v", name, caFile, err)
				continue
			}
			if !bytes.Equal(got, ca.caPEM) {
				t.Errorf("%s.CaFile content mismatch: got %q, want %q", name, got, ca.caPEM)
			}
		}
	})

	t.Run("NoCABundleLeavesCaFileEmpty", func(t *testing.T) {
		c, err := NewClient(logging.NewNopLogger(), restConfig, func(a *Args) {
			a.Namespace = "default"
		})
		if err != nil {
			t.Fatalf("NewClient(...): unexpected error: %v", err)
		}
		cc, ok := c.(*client)
		if !ok {
			t.Fatalf("NewClient(...) did not return a *client")
		}
		if cc.pullClient.CaFile != "" {
			t.Errorf("pullClient.CaFile = %q, want empty when no CABundle is supplied", cc.pullClient.CaFile)
		}
	})

	// This is the trust-store-parity fix itself: with a system bundle
	// present, the classic getter's CaFile must contain BOTH the system
	// bundle and the user's CABundle - otherwise (per the review) OCI
	// trusts system+bundle while classic repos trust bundle-only.
	t.Run("SystemCABundlePresent_CaFileContainsBoth", func(t *testing.T) {
		systemCA := newTestCA(t)
		systemBundlePath := filepath.Join(t.TempDir(), "system-ca-bundle.crt")
		if err := os.WriteFile(systemBundlePath, systemCA.caPEM, 0600); err != nil {
			t.Fatalf("writing fake system CA bundle: %v", err)
		}
		systemCACandidates = []string{systemBundlePath}
		defer func() { systemCACandidates = nil }()

		c, err := NewClient(logging.NewNopLogger(), restConfig, func(a *Args) {
			a.Namespace = "default"
			a.CABundle = ca.caPEM
		})
		if err != nil {
			t.Fatalf("NewClient(...): unexpected error: %v", err)
		}
		cc, ok := c.(*client)
		if !ok {
			t.Fatalf("NewClient(...) did not return a *client")
		}

		got, err := os.ReadFile(cc.pullClient.CaFile)
		if err != nil {
			t.Fatalf("reading pullClient.CaFile: %v", err)
		}
		if !bytes.Contains(got, systemCA.caPEM) {
			t.Errorf("pullClient.CaFile does not contain the system CA bundle: %q", got)
		}
		if !bytes.Contains(got, ca.caPEM) {
			t.Errorf("pullClient.CaFile does not contain the user-supplied CABundle: %q", got)
		}
	})
}

// TestOCIRegistryPull_WithCABundle is the end-to-end proof for the OCI
// registry path: a real chart is pushed to and pulled from a real
// in-process OCI registry served over TLS with a self-signed CA, using
// nothing but this package's own httpClientTrustingCABundle plus Helm's
// registry.Client - no mocking of the TLS handshake or the registry
// protocol. Proves both halves: the pull fails without the CA bundle, and
// succeeds with it.
func TestOCIRegistryPull_WithCABundle(t *testing.T) {
	ca := newTestCA(t)

	srv := httptest.NewUnstartedServer(registry.New())
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{ca.leafCert}}
	srv.StartTLS()
	defer srv.Close()

	host := srv.Listener.Addr().String() // 127.0.0.1:<port>
	ref := "oci://" + host + "/charts/testchart:0.1.0"

	trustingHTTPClient, err := httpClientTrustingCABundle(logging.NewNopLogger(), ca.caPEM)
	if err != nil {
		t.Fatalf("httpClientTrustingCABundle(...): unexpected error: %v", err)
	}

	pushClient, err := helmregistry.NewClient(helmregistry.ClientOptHTTPClient(trustingHTTPClient))
	if err != nil {
		t.Fatalf("helmregistry.NewClient(...) for push setup: unexpected error: %v", err)
	}

	chartDir := t.TempDir()
	chartPath, err := chartutil.Create("testchart", chartDir)
	if err != nil {
		t.Fatalf("chartutil.Create(...): unexpected error: %v", err)
	}
	tgzDir := t.TempDir()
	tgzPath, err := chartutil.Save(mustLoadChart(t, chartPath), tgzDir)
	if err != nil {
		t.Fatalf("chartutil.Save(...): unexpected error: %v", err)
	}
	tgzBytes, err := os.ReadFile(tgzPath)
	if err != nil {
		t.Fatalf("reading packaged chart: %v", err)
	}

	if _, err := pushClient.Push(tgzBytes, ref); err != nil {
		t.Fatalf("Push(...) to test registry: unexpected error: %v", err)
	}

	t.Run("WithoutCABundlePullFails", func(t *testing.T) {
		rc, err := helmregistry.NewClient()
		if err != nil {
			t.Fatalf("helmregistry.NewClient(...): unexpected error: %v", err)
		}
		if _, err := rc.Pull(ref); err == nil {
			t.Fatal("expected Pull(...) without a trusted CA to fail with a certificate error, got nil")
		}
	})

	t.Run("WithCABundlePullSucceeds", func(t *testing.T) {
		hc, err := httpClientTrustingCABundle(logging.NewNopLogger(), ca.caPEM)
		if err != nil {
			t.Fatalf("httpClientTrustingCABundle(...): unexpected error: %v", err)
		}
		rc, err := helmregistry.NewClient(helmregistry.ClientOptHTTPClient(hc))
		if err != nil {
			t.Fatalf("helmregistry.NewClient(...): unexpected error: %v", err)
		}
		result, err := rc.Pull(ref)
		if err != nil {
			t.Fatalf("Pull(...) with trusted CA: unexpected error: %v", err)
		}
		if len(result.Chart.Data) == 0 {
			t.Fatal("Pull(...) succeeded but returned no chart data")
		}
	})
}

// TestClassicRepoGet_TrustsSystemAndUserCABundleWhenConcatenated is the
// end-to-end proof for the trust-store-parity fix: helm's own HTTPGetter,
// configured via getter.WithTLSClientConfig exactly as action.Pull configures
// it from CaFile, against a real TLS server - no mocking of the TLS
// handshake. It proves the specific failure mode from review: a CaFile
// containing only a bundle unrelated to the server's issuing CA (standing in
// for "the system trust store, which doesn't happen to include this
// private CA") fails, while the concatenation NewClient actually builds
// (system bundle + user CABundle) succeeds - which is exactly what a real
// system CA bundle file's content plus the user's CABundle would produce.
func TestClassicRepoGet_TrustsSystemAndUserCABundleWhenConcatenated(t *testing.T) {
	userCA := newTestCA(t)
	unrelatedCA := newTestCA(t)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("index.yaml content"))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{userCA.leafCert}}
	srv.StartTLS()
	defer srv.Close()

	g, err := getter.NewHTTPGetter()
	if err != nil {
		t.Fatalf("getter.NewHTTPGetter(...): unexpected error: %v", err)
	}

	t.Run("UnrelatedBundleAloneFails", func(t *testing.T) {
		caFile := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(caFile, unrelatedCA.caPEM, 0600); err != nil {
			t.Fatalf("writing CA file: %v", err)
		}
		_, err := g.Get(srv.URL, getter.WithTLSClientConfig("", "", caFile))
		if err == nil {
			t.Fatal("expected Get(...) to fail trusting only a CA unrelated to the server's issuer, got no error")
		}
	})

	t.Run("ConcatenatedBundleSucceeds", func(t *testing.T) {
		// This is exactly what NewClient writes to CaFile when a system CA
		// bundle is found: the system (here, unrelated) bundle first, then
		// the user-supplied one.
		combined := append(append([]byte{}, unrelatedCA.caPEM...), append([]byte("\n"), userCA.caPEM...)...)
		caFile := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(caFile, combined, 0600); err != nil {
			t.Fatalf("writing CA file: %v", err)
		}
		buf, err := g.Get(srv.URL, getter.WithTLSClientConfig("", "", caFile))
		if err != nil {
			t.Fatalf("Get(...) with concatenated CA bundle: unexpected error: %v", err)
		}
		if buf.String() != "index.yaml content" {
			t.Fatalf("Get(...): got body %q, want %q", buf.String(), "index.yaml content")
		}
	})
}
