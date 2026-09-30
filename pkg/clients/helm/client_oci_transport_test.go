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
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/google/go-containerregistry/pkg/registry"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	helmregistry "helm.sh/helm/v4/pkg/registry"
	"k8s.io/client-go/rest"
)

// pushTestChart packages a new chart named testchart, version 0.1.0, and
// pushes it to <host>/charts with rc.
func pushTestChart(t *testing.T, rc *helmregistry.Client, host string) {
	t.Helper()
	chartPath, err := chartutil.Create("testchart", t.TempDir())
	if err != nil {
		t.Fatalf("chartutil.Create(...): unexpected error: %v", err)
	}
	tgzPath, err := chartutil.Save(mustLoadChart(t, chartPath), t.TempDir())
	if err != nil {
		t.Fatalf("chartutil.Save(...): unexpected error: %v", err)
	}
	tgz, err := os.ReadFile(tgzPath)
	if err != nil {
		t.Fatalf("reading packaged chart: %v", err)
	}
	if _, err := rc.Push(tgz, host+"/charts/testchart:0.1.0"); err != nil {
		t.Fatalf("Push(...) to test registry: unexpected error: %v", err)
	}
}

// pullWithArgs pulls testchart 0.1.0 from oci://<host>/charts the way a
// Release does: through NewClient and pullChart, with the given Args.
func pullWithArgs(t *testing.T, host string, apply ArgsApplier) error {
	t.Helper()
	return pullWithCreds(t, host, &RepoCreds{}, apply)
}

// isolateRegistryCredentials gives Helm and Docker empty configs and hides
// any docker-credential-* helper. Login stores credentials in Helm's registry
// config, or in the OS keychain when a native helper is on PATH (the Helm
// registry client auto-detects one), so without this a test would write to
// the user's keychain and later pulls could reuse the stored credentials.
func isolateRegistryCredentials(t *testing.T) {
	t.Helper()
	t.Setenv("HELM_CONFIG_HOME", t.TempDir())
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("PATH", t.TempDir())
}

// pullWithCreds is pullWithArgs with registry credentials, which make
// pullChart log in first. Every pull starts from empty credential stores, so
// a pull without credentials can't reuse an earlier login.
func pullWithCreds(t *testing.T, host string, creds *RepoCreds, apply ArgsApplier) error {
	t.Helper()
	isolateRegistryCredentials(t)
	originalCache := chartCache
	chartCache = t.TempDir()
	t.Cleanup(func() { chartCache = originalCache })

	// Init doesn't dial the Kubernetes API, so a non-dialable host is fine.
	restConfig := &rest.Config{Host: "http://127.0.0.1:0"}
	c, err := NewClient(logging.NewNopLogger(), restConfig, func(a *Args) { a.Namespace = "default" }, apply)
	if err != nil {
		t.Fatalf("NewClient(...): unexpected error: %v", err)
	}
	cc, ok := c.(*client)
	if !ok {
		t.Fatalf("NewClient(...) did not return a *client")
	}
	return cc.pullChart("", "testchart", "0.1.0", "oci://"+host+"/charts", "", creds, t.TempDir())
}

// requireBasicAuth puts HTTP basic auth in front of h.
func requireBasicAuth(h http.Handler, username, password string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != username || p != password {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// TestOCIRegistryPull_PlainHTTP: spec.forProvider.plainHTTP must reach the
// OCI registry client. Helm v4's OCI getter uses the preset registry client
// as-is, and v4 no longer falls back from HTTPS to HTTP on its own.
func TestOCIRegistryPull_PlainHTTP(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := srv.Listener.Addr().String()

	pushClient, err := helmregistry.NewClient(helmregistry.ClientOptPlainHTTP())
	if err != nil {
		t.Fatalf("helmregistry.NewClient(...) for push setup: unexpected error: %v", err)
	}
	pushTestChart(t, pushClient, host)

	t.Run("WithPlainHTTPPullSucceeds", func(t *testing.T) {
		if err := pullWithArgs(t, host, func(a *Args) { a.PlainHTTP = true }); err != nil {
			t.Fatalf("pullChart(...) with PlainHTTP: unexpected error: %v", err)
		}
	})

	t.Run("WithoutPlainHTTPPullFails", func(t *testing.T) {
		if err := pullWithArgs(t, host, func(*Args) {}); err == nil {
			t.Fatal("expected pullChart(...) over HTTPS against a plain-HTTP registry to fail, got nil")
		}
	})
}

// TestOCIRegistryPull_InsecureSkipTLSVerify: spec.forProvider.insecureSkipTLSVerify
// must reach the OCI registry client, so a registry with a certificate the
// system doesn't trust can be pulled from.
func TestOCIRegistryPull_InsecureSkipTLSVerify(t *testing.T) {
	ca := newTestCA(t)
	srv := httptest.NewUnstartedServer(registry.New())
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{ca.leafCert}}
	srv.StartTLS()
	defer srv.Close()
	host := srv.Listener.Addr().String()

	trusting, err := httpClientTrustingCABundle(logging.NewNopLogger(), ca.caPEM)
	if err != nil {
		t.Fatalf("httpClientTrustingCABundle(...): unexpected error: %v", err)
	}
	pushClient, err := helmregistry.NewClient(helmregistry.ClientOptHTTPClient(trusting))
	if err != nil {
		t.Fatalf("helmregistry.NewClient(...) for push setup: unexpected error: %v", err)
	}
	pushTestChart(t, pushClient, host)

	t.Run("WithInsecureSkipTLSVerifyPullSucceeds", func(t *testing.T) {
		if err := pullWithArgs(t, host, func(a *Args) { a.InsecureSkipTLSVerify = true }); err != nil {
			t.Fatalf("pullChart(...) with InsecureSkipTLSVerify: unexpected error: %v", err)
		}
	})

	t.Run("WithoutInsecureSkipTLSVerifyPullFails", func(t *testing.T) {
		if err := pullWithArgs(t, host, func(*Args) {}); err == nil {
			t.Fatal("expected pullChart(...) against an untrusted certificate to fail, got nil")
		}
	})

	t.Run("WithCABundlePullSucceeds", func(t *testing.T) {
		caBundleCacheDir = t.TempDir()
		if err := pullWithArgs(t, host, func(a *Args) { a.CABundle = ca.caPEM }); err != nil {
			t.Fatalf("pullChart(...) with CABundle: unexpected error: %v", err)
		}
	})
}

// TestOCIRegistryPull_WithLogin: with credentials, pullChart logs in before
// the pull. The login must honor plainHTTP too, and must not undo the
// registry client's plainHTTP and insecureSkipTLSVerify for the pull.
func TestOCIRegistryPull_WithLogin(t *testing.T) {
	const username, password = "user", "secret"
	creds := &RepoCreds{Username: username, Password: password}
	isolateRegistryCredentials(t)

	t.Run("PlainHTTP", func(t *testing.T) {
		srv := httptest.NewServer(requireBasicAuth(registry.New(), username, password))
		defer srv.Close()
		host := srv.Listener.Addr().String()

		pushClient, err := helmregistry.NewClient(helmregistry.ClientOptPlainHTTP(), helmregistry.ClientOptBasicAuth(username, password))
		if err != nil {
			t.Fatalf("helmregistry.NewClient(...) for push setup: unexpected error: %v", err)
		}
		pushTestChart(t, pushClient, host)

		t.Run("WithCredentialsPullSucceeds", func(t *testing.T) {
			if err := pullWithCreds(t, host, creds, func(a *Args) { a.PlainHTTP = true }); err != nil {
				t.Fatalf("pullChart(...) with PlainHTTP and credentials: unexpected error: %v", err)
			}
		})

		t.Run("WithoutCredentialsPullFails", func(t *testing.T) {
			if err := pullWithArgs(t, host, func(a *Args) { a.PlainHTTP = true }); err == nil {
				t.Fatal("expected pullChart(...) without credentials from a registry that requires them to fail, got nil")
			}
		})
	})

	t.Run("InsecureSkipTLSVerify", func(t *testing.T) {
		ca := newTestCA(t)
		srv := httptest.NewUnstartedServer(requireBasicAuth(registry.New(), username, password))
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{ca.leafCert}}
		srv.StartTLS()
		defer srv.Close()
		host := srv.Listener.Addr().String()

		trusting, err := httpClientTrustingCABundle(logging.NewNopLogger(), ca.caPEM)
		if err != nil {
			t.Fatalf("httpClientTrustingCABundle(...): unexpected error: %v", err)
		}
		pushClient, err := helmregistry.NewClient(helmregistry.ClientOptHTTPClient(trusting), helmregistry.ClientOptBasicAuth(username, password))
		if err != nil {
			t.Fatalf("helmregistry.NewClient(...) for push setup: unexpected error: %v", err)
		}
		pushTestChart(t, pushClient, host)

		t.Run("WithCredentialsPullSucceeds", func(t *testing.T) {
			if err := pullWithCreds(t, host, creds, func(a *Args) { a.InsecureSkipTLSVerify = true }); err != nil {
				t.Fatalf("pullChart(...) with InsecureSkipTLSVerify and credentials: unexpected error: %v", err)
			}
		})
	})
}
