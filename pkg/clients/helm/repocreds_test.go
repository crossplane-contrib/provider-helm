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
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/registry"
	registryAuth "oras.land/oras-go/v2/registry/remote/auth"
)

func TestRepoCredsRegistryCredential(t *testing.T) {
	type want struct {
		basicAuth     bool
		identityToken bool
		registryCreds registryAuth.Credential
	}

	cases := map[string]struct {
		creds *RepoCreds
		want  want
	}{
		"Nil": {
			creds: nil,
			want: want{
				registryCreds: registryAuth.EmptyCredential,
			},
		},
		"UsernamePassword": {
			creds: &RepoCreds{
				Username: "testuser",
				Password: "testpass",
			},
			want: want{
				basicAuth: true,
				registryCreds: registryAuth.Credential{
					Username: "testuser",
					Password: "testpass",
				},
			},
		},
		"IdentityToken": {
			creds: &RepoCreds{
				Username:      "<token>",
				IdentityToken: "refresh-token",
			},
			want: want{
				identityToken: true,
				registryCreds: registryAuth.Credential{
					Username:     "<token>",
					RefreshToken: "refresh-token",
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.creds.hasBasicAuth(); got != tc.want.basicAuth {
				t.Errorf("hasBasicAuth() = %v, want %v", got, tc.want.basicAuth)
			}

			if got := tc.creds.hasIdentityToken(); got != tc.want.identityToken {
				t.Errorf("hasIdentityToken() = %v, want %v", got, tc.want.identityToken)
			}

			if diff := cmp.Diff(tc.want.registryCreds, tc.creds.registryCredential()); diff != "" {
				t.Errorf("registryCredential(): -want, +got:\n%s", diff)
			}
		})
	}
}

func TestRegistryHTTPClientInsecureSkipTLSVerify(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cases := map[string]struct {
		insecureSkipTLSVerify bool
		wantErr               bool
	}{
		"DefaultTLSVerification": {
			wantErr: true,
		},
		"InsecureSkipTLSVerify": {
			insecureSkipTLSVerify: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatalf("http.NewRequestWithContext() error: %v", err)
			}

			resp, err := registryHTTPClient(tc.insecureSkipTLSVerify).Do(req)
			if resp != nil {
				defer resp.Body.Close()
			}

			if tc.wantErr {
				if err == nil {
					t.Fatal("registryHTTPClient().Do() error: want error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("registryHTTPClient().Do() error: %v", err)
			}
		})
	}
}

func TestConfigurePullCredentials(t *testing.T) {
	cases := map[string]struct {
		chartURL  string
		chartRepo string
		creds     *RepoCreds
		wantUser  string
		wantPass  string
	}{
		"Nil": {},
		"UsernamePassword": {
			creds: &RepoCreds{
				Username: "testuser",
				Password: "testpass",
			},
			wantUser: "testuser",
			wantPass: "testpass",
		},
		"IdentityTokenOCI": {
			chartRepo: "oci://registry.example.com/charts",
			creds: &RepoCreds{
				Username:      "<token>",
				IdentityToken: "refresh-token",
			},
		},
		"IdentityTokenWithBasicAuthOCI": {
			chartURL: "oci://registry.example.com/charts/my-chart:1.0.0",
			creds: &RepoCreds{
				Username:      "testuser",
				Password:      "testpass",
				IdentityToken: "refresh-token",
			},
		},
		"IdentityTokenWithBasicAuthNonOCI": {
			chartRepo: "https://charts.example.com",
			creds: &RepoCreds{
				Username:      "testuser",
				Password:      "testpass",
				IdentityToken: "refresh-token",
			},
			wantUser: "testuser",
			wantPass: "testpass",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			pc := action.NewPull()
			pc.Username = "stale-user"
			pc.Password = "stale-pass"

			configurePullCredentials(pc, tc.chartURL, tc.chartRepo, tc.creds)

			if pc.Username != tc.wantUser {
				t.Errorf("Username = %q, want %q", pc.Username, tc.wantUser)
			}
			if pc.Password != tc.wantPass {
				t.Errorf("Password = %q, want %q", pc.Password, tc.wantPass)
			}
		})
	}
}

func TestConfigureRegistryClientResetsDefault(t *testing.T) {
	defaultClient, err := registry.NewClient()
	if err != nil {
		t.Fatalf("registry.NewClient() error: %v", err)
	}
	actionConfig := &action.Configuration{RegistryClient: defaultClient}
	hc := &client{
		pullClient:     action.NewPullWithOpts(action.WithConfig(actionConfig)),
		registryClient: defaultClient,
	}

	if err := hc.configureRegistryClient("oci://registry.example.com/charts", "", &RepoCreds{
		Username:      "<token>",
		IdentityToken: "refresh-token",
	}); err != nil {
		t.Fatalf("configureRegistryClient() identity token error: %v", err)
	}
	identityTokenClient := actionConfig.RegistryClient
	if identityTokenClient == defaultClient {
		t.Fatal("configureRegistryClient() did not install identity-token registry client")
	}

	if err := hc.configureRegistryClient("oci://registry.example.com/charts", "", &RepoCreds{
		Username: "testuser",
		Password: "testpass",
	}); err != nil {
		t.Fatalf("configureRegistryClient() basic auth error: %v", err)
	}
	if actionConfig.RegistryClient != defaultClient {
		t.Fatal("configureRegistryClient() did not reset to default registry client")
	}
}

func TestIdentityTokenRegistryClientUsesRefreshToken(t *testing.T) {
	const (
		refreshToken = "refresh-token"
		accessToken  = "access-token"
		service      = "test-service"
		scope        = "repository:test:pull"
	)

	var tokenRequested atomic.Bool
	var authorizedTagsRequested atomic.Bool
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/test/tags/list":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="%s",scope="%s"`, server.URL, service, scope))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			authorizedTagsRequested.Store(true)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"name":"test","tags":["1.0.0"]}`)
		case "/token":
			tokenRequested.Store(true)
			if r.Method != http.MethodPost {
				t.Errorf("token request method = %s, want %s", r.Method, http.MethodPost)
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("ParseForm() error: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if got := r.PostForm.Get("grant_type"); got != "refresh_token" {
				t.Errorf("grant_type = %q, want refresh_token", got)
			}
			if got := r.PostForm.Get("refresh_token"); got != refreshToken {
				t.Errorf("refresh_token = %q, want %q", got, refreshToken)
			}
			if got := r.PostForm.Get("service"); got != service {
				t.Errorf("service = %q, want %q", got, service)
			}
			if got := r.PostForm.Get("scope"); got != scope {
				t.Errorf("scope = %q, want %q", got, scope)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"access_token":%q}`, accessToken)
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	host := strings.TrimPrefix(server.URL, "http://")
	rc, err := identityTokenRegistryClient(host, &RepoCreds{
		Username:      "<token>",
		IdentityToken: refreshToken,
	}, false, true)
	if err != nil {
		t.Fatalf("identityTokenRegistryClient() error: %v", err)
	}

	tags, err := rc.Tags(host + "/test")
	if err != nil {
		t.Fatalf("Tags() error: %v", err)
	}
	if diff := cmp.Diff([]string{"1.0.0"}, tags); diff != "" {
		t.Errorf("Tags(): -want, +got:\n%s", diff)
	}
	if !tokenRequested.Load() {
		t.Fatal("token endpoint was not requested")
	}
	if !authorizedTagsRequested.Load() {
		t.Fatal("authorized tags endpoint was not requested")
	}
}
