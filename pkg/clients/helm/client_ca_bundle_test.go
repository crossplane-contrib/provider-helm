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
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/google/go-containerregistry/pkg/registry"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
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
		hc, err := httpClientTrustingCABundle(ca.caPEM)
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
		_, err := httpClientTrustingCABundle([]byte("not a pem certificate"))
		if err == nil {
			t.Fatal("expected an error for invalid PEM input, got nil")
		}
	})
}

func TestWriteCABundleToTempFile(t *testing.T) {
	ca := newTestCA(t)

	path1, err := writeCABundleToTempFile(ca.caPEM)
	if err != nil {
		t.Fatalf("writeCABundleToTempFile(...): unexpected error: %v", err)
	}
	defer os.Remove(path1)

	got, err := os.ReadFile(path1)
	if err != nil {
		t.Fatalf("reading back written CA bundle: %v", err)
	}
	if !bytes.Equal(got, ca.caPEM) {
		t.Fatalf("written CA bundle content mismatch: got %q, want %q", got, ca.caPEM)
	}

	path2, err := writeCABundleToTempFile(ca.caPEM)
	if err != nil {
		t.Fatalf("writeCABundleToTempFile(...) second call: unexpected error: %v", err)
	}
	defer os.Remove(path2)

	if path1 == path2 {
		t.Fatalf("expected two calls to produce distinct temp files, got the same path %q twice", path1)
	}
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

	trustingHTTPClient, err := httpClientTrustingCABundle(ca.caPEM)
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
		hc, err := httpClientTrustingCABundle(ca.caPEM)
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
