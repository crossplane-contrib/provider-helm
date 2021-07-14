/*
Copyright 2021 The Crossplane Authors.

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

package release

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"k8s.io/apimachinery/pkg/util/net"
	restclient "k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	providerGCPAuthPlugin = "provider-gcp" // so that this is different than "gcp" that's already in client-go tree.
)

func init() {
	if err := restclient.RegisterAuthProviderPlugin(providerGCPAuthPlugin, newProviderGCPAuthProvider); err != nil {
		klog.Fatalf("Failed to register gcp auth plugin: %v", err)
	}
}

var (
	// defaultScopes:
	// - cloud-platform is the base scope to authenticate to GCP.
	// - userinfo.email is used to authenticate to GKE APIs with gserviceaccount
	//   email instead of numeric uniqueID.
	defaultScopes = []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email"}
)

// gcpAuthProvider is an auth provider plugin that uses GCP credentials to provide
// tokens for kubectl to authenticate itself to the apiserver. A sample json config
// is provided below with all recognized options described.
//
// {
//   'auth-provider': {
//     # Required
//     "name": "provider-gcp",
//
//     'config': {
//       # Authentication options
//       # These options are used while getting a token.
//
//       # comma-separated list of GCP API scopes. default value of this field
//       # is "https://www.googleapis.com/auth/cloud-platform,https://www.googleapis.com/auth/userinfo.email".
// 		 # to override the API scopes, specify this field explicitly.
//       "scopes": "https://www.googleapis.com/auth/cloud-platform"
//
//       # Caching options
//
//       # Raw string data representing cached access token.
//       "access-token": "ya29.CjWdA4GiBPTt",
//       # RFC3339Nano expiration timestamp for cached access token.
//       "expiry": "2016-10-31 22:31:9.123",
//
//       # golang reference time in the format that the expiration timestamp uses.
//       # If omitted, defaults to time.RFC3339Nano
//       "time-fmt": "2006-01-02 15:04:05.999999999"
//     }
//   }
// }
//
type providerGCPAuthProvider struct {
	tokenSource oauth2.TokenSource
	persister   restclient.AuthProviderConfigPersister
}

func newProviderGCPAuthProvider(_ string, providerGCPConfig map[string]string, persister restclient.AuthProviderConfigPersister) (restclient.AuthProvider, error) {
	ts, err := tokenSource(providerGCPConfig)
	if err != nil {
		return nil, err
	}
	cts, err := newCachedTokenSource(providerGCPConfig["access-token"], providerGCPConfig["expiry"], persister, ts, providerGCPConfig)
	if err != nil {
		return nil, err
	}
	return &providerGCPAuthProvider{cts, persister}, nil
}

func tokenSource(providerGCPConfig map[string]string) (oauth2.TokenSource, error) {
	// Google Application Credentials-based token source
	scopes := parseScopes(providerGCPConfig)
	ts, err := google.JWTAccessTokenSourceWithScope([]byte(providerGCPConfig["creds"]), scopes...)
	if err != nil {
		return nil, fmt.Errorf("cannot construct JWT token source: %v", err)
	}
	return ts, nil
}

// parseScopes constructs a list of scopes that should be included in token source
// from the config map.
func parseScopes(providerGCPConfig map[string]string) []string {
	scopes, ok := providerGCPConfig["scopes"]
	if !ok {
		return defaultScopes
	}
	if scopes == "" {
		return []string{}
	}
	return strings.Split(providerGCPConfig["scopes"], ",")
}

func (g *providerGCPAuthProvider) WrapTransport(rt http.RoundTripper) http.RoundTripper {
	var resetCache map[string]string
	if cts, ok := g.tokenSource.(*cachedTokenSource); ok {
		resetCache = cts.baseCache()
	} else {
		resetCache = make(map[string]string)
	}
	return &conditionalTransport{&oauth2.Transport{Source: g.tokenSource, Base: rt}, g.persister, resetCache}
}

func (g *providerGCPAuthProvider) Login() error { return nil }

type cachedTokenSource struct {
	lk          sync.Mutex
	source      oauth2.TokenSource
	accessToken string `datapolicy:"token"`
	expiry      time.Time
	persister   restclient.AuthProviderConfigPersister
	cache       map[string]string
}

func newCachedTokenSource(accessToken, expiry string, persister restclient.AuthProviderConfigPersister, ts oauth2.TokenSource, cache map[string]string) (*cachedTokenSource, error) {
	var expiryTime time.Time
	if parsedTime, err := time.Parse(time.RFC3339Nano, expiry); err == nil {
		expiryTime = parsedTime
	} 
	if cache == nil {
		cache = make(map[string]string)
	}
	return &cachedTokenSource{
		source:      ts,
		accessToken: accessToken,
		expiry:      expiryTime,
		persister:   persister,
		cache:       cache,
	}, nil
}

func (t *cachedTokenSource) Token() (*oauth2.Token, error) {
	tok := t.cachedToken()
	if tok.Valid() && !tok.Expiry.IsZero() {
		return tok, nil
	}
	tok, err := t.source.Token()
	if err != nil {
		return nil, err
	}
	cache := t.update(tok)
	if t.persister != nil {
		if err := t.persister.Persist(cache); err != nil {
			klog.V(4).Infof("Failed to persist token: %v", err)
		}
	}
	return tok, nil
}

func (t *cachedTokenSource) cachedToken() *oauth2.Token {
	t.lk.Lock()
	defer t.lk.Unlock()
	return &oauth2.Token{
		AccessToken: t.accessToken,
		TokenType:   "Bearer",
		Expiry:      t.expiry,
	}
}

func (t *cachedTokenSource) update(tok *oauth2.Token) map[string]string {
	t.lk.Lock()
	defer t.lk.Unlock()
	t.accessToken = tok.AccessToken
	t.expiry = tok.Expiry
	ret := map[string]string{}
	for k, v := range t.cache {
		ret[k] = v
	}
	ret["access-token"] = t.accessToken
	ret["expiry"] = t.expiry.Format(time.RFC3339Nano)
	return ret
}

// baseCache is the base configuration value for this TokenSource, without any cached ephemeral tokens.
func (t *cachedTokenSource) baseCache() map[string]string {
	t.lk.Lock()
	defer t.lk.Unlock()
	ret := map[string]string{}
	for k, v := range t.cache {
		ret[k] = v
	}
	delete(ret, "access-token")
	delete(ret, "expiry")
	return ret
}

type conditionalTransport struct {
	oauthTransport *oauth2.Transport
	persister      restclient.AuthProviderConfigPersister
	resetCache     map[string]string
}

var _ net.RoundTripperWrapper = &conditionalTransport{}

func (t *conditionalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(req.Header.Get("Authorization")) != 0 {
		return t.oauthTransport.Base.RoundTrip(req)
	}

	res, err := t.oauthTransport.RoundTrip(req)

	if err != nil {
		return nil, err
	}

	if res.StatusCode == 401 {
		klog.V(4).Infof("The credentials that were supplied are invalid for the target cluster")
		t.persister.Persist(t.resetCache)
	}

	return res, nil
}

func (t *conditionalTransport) WrappedRoundTripper() http.RoundTripper { return t.oauthTransport.Base }
