/*
Copyright 2025 The Crossplane Authors.

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

package registryauth

import (
	"context"
	"io"
	"strings"

	ecr "github.com/awslabs/amazon-ecr-credential-helper/ecr-login"
	"github.com/chrismellard/docker-credential-acr-env/pkg/credhelper"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/google"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1beta1 "github.com/crossplane-contrib/provider-helm/apis/cluster/release/v1beta1"
	namespacedv1beta1 "github.com/crossplane-contrib/provider-helm/apis/namespaced/release/v1beta1"
	helmClient "github.com/crossplane-contrib/provider-helm/pkg/clients/helm"
)

var (
	amazonKeychain = authn.NewKeychainFromHelper(ecr.NewECRHelper(ecr.WithLogger(io.Discard)))
	azureKeychain  = authn.NewKeychainFromHelper(credhelper.NewACRCredentialsHelper())
)

const (
	keyRepoUsername      = "username"
	keyRepoPassword      = "password"
	errFailedToGetSecret = "failed to get registry secret"
	errFailedToParseRef  = "failed to parse registry reference"
)

func normalizeRegistryURL(registryURL string) string {
	ref := strings.TrimSpace(registryURL)

	switch {
	case strings.HasPrefix(ref, "oci://"):
		ref = strings.TrimPrefix(ref, "oci://")
	case strings.HasPrefix(ref, "https://"):
		ref = strings.TrimPrefix(ref, "https://")
	case strings.HasPrefix(ref, "http://"):
		ref = strings.TrimPrefix(ref, "http://")
	}

	return strings.TrimSuffix(ref, "/")
}

// Resolver resolves registry credentials based on authentication configuration
type Resolver struct {
	kube client.Client
}

// NewResolver creates a new registry auth resolver.
func NewResolver(kube client.Client) *Resolver {
	return &Resolver{
		kube: kube,
	}
}

// ResolveNamespaced resolves registry credentials for a namespaced Release
func (r *Resolver) ResolveNamespaced(ctx context.Context, release *namespacedv1beta1.Release) (*helmClient.RepoCreds, error) {
	registryURL := release.Spec.ForProvider.Chart.Repository
	if registryURL == "" {
		registryURL = release.Spec.ForProvider.Chart.URL
	}

	// If PullSecretRef is provided, use it
	if release.Spec.ForProvider.Chart.PullSecretRef.Name != "" {
		return r.resolveSecretCredentials(ctx, release.Namespace, release.Spec.ForProvider.Chart.PullSecretRef.Name)
	}

	// Build full repository path by combining registry URL and chart name
	chartName := release.Spec.ForProvider.Chart.Name
	fullRepoURL := registryURL
	if chartName != "" && !strings.HasSuffix(registryURL, chartName) {
		fullRepoURL = strings.TrimSuffix(registryURL, "/") + "/" + chartName
	}

	// Otherwise, use default credential chain (AWS IRSA, Azure/GCP Workload Identity, etc.)
	return r.resolveKeychainAuth(fullRepoURL)
}

// ResolveCluster resolves registry credentials for a cluster-scoped Release
func (r *Resolver) ResolveCluster(ctx context.Context, release *clusterv1beta1.Release) (*helmClient.RepoCreds, error) {
	registryURL := release.Spec.ForProvider.Chart.Repository
	if registryURL == "" {
		registryURL = release.Spec.ForProvider.Chart.URL
	}

	// If PullSecretRef is provided, use it
	if release.Spec.ForProvider.Chart.PullSecretRef.Name != "" {
		if release.Spec.ForProvider.Chart.PullSecretRef.Namespace == "" {
			return nil, errors.New("namespace required in PullSecretRef for cluster-scoped Release")
		}
		return r.resolveSecretCredentials(ctx, release.Spec.ForProvider.Chart.PullSecretRef.Namespace,
			release.Spec.ForProvider.Chart.PullSecretRef.Name)
	}

	// Build full repository path by combining registry URL and chart name
	chartName := release.Spec.ForProvider.Chart.Name
	fullRepoURL := registryURL
	if chartName != "" && !strings.HasSuffix(registryURL, chartName) {
		fullRepoURL = strings.TrimSuffix(registryURL, "/") + "/" + chartName
	}

	// Otherwise, use default credential chain (AWS IRSA, Azure/GCP Workload Identity, etc.)
	return r.resolveKeychainAuth(fullRepoURL)
}

func (r *Resolver) resolveSecretCredentials(ctx context.Context, namespace, name string) (*helmClient.RepoCreds, error) {
	secret := &corev1.Secret{}
	if err := r.kube.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret); err != nil {
		return nil, errors.Wrap(err, errFailedToGetSecret)
	}

	username := string(secret.Data[keyRepoUsername])
	password := string(secret.Data[keyRepoPassword])

	if username == "" || password == "" {
		return nil, errors.New("secret must contain 'username' and 'password' keys")
	}

	return &helmClient.RepoCreds{
		Username: username,
		Password: password,
	}, nil
}

// resolveKeychainAuth uses cloud provider credential helpers for authentication
// including AWS ECR (IRSA), GCP GAR/GCR (Workload Identity), Azure ACR (Workload Identity).
// For public registries, it returns empty credentials.
func (r *Resolver) resolveKeychainAuth(registryURL string) (*helmClient.RepoCreds, error) {
	ref, err := parseRegistryReference(registryURL)
	if err != nil {
		return nil, err
	}

	keychain, err := r.createKeychain()
	if err != nil {
		return nil, err
	}

	return r.resolveCredentialsFromKeychain(keychain, ref.Context())
}

// createKeychain creates a keychain that supports cloud provider authentication.
// Uses credential helpers (AWS ECR, GCP, Azure).
func (r *Resolver) createKeychain() (authn.Keychain, error) {
	// Create a custom MultiKeychain with cloud providers first, then fallbacks.
	//
	// Priority:
	// 1. amazonKeychain - AWS ECR via IRSA (AWS_WEB_IDENTITY_TOKEN_FILE, AWS_ROLE_ARN)
	// 2. google.Keychain - GCP GAR/GCR via Workload Identity or metadata service
	// 3. azureKeychain - Azure ACR via Workload Identity or metadata service
	// 4. authn.DefaultKeychain - Docker config.json and credential helpers

	keychains := []authn.Keychain{
		amazonKeychain,
		google.Keychain,
		azureKeychain,
		authn.DefaultKeychain,
	}

	return authn.NewMultiKeychain(keychains...), nil
}

// parseRegistryReference converts a Helm OCI URL to a container registry reference
func parseRegistryReference(registryURL string) (name.Reference, error) {
	imageRef := normalizeRegistryURL(registryURL)

	repo, err := name.NewRepository(imageRef)
	if err != nil {
		return nil, errors.Wrap(err, errFailedToParseRef)
	}

	// Fake a tag purely to satisfy interfaces, if needed
	return repo.Tag("latest"), nil
}

// resolveCredentialsFromKeychain resolves credentials from a keychain for a given resource.
// Returns empty credentials if authentication is not available (for public registries or anonymous access).
func (r *Resolver) resolveCredentialsFromKeychain(keychain authn.Keychain, resource authn.Resource) (*helmClient.RepoCreds, error) {
	authenticator, err := keychain.Resolve(resource)
	if err != nil {
		// If keychain resolution fails, return empty credentials for public/anonymous access
		return &helmClient.RepoCreds{}, nil
	}

	authConfig, err := authenticator.Authorization()
	if err != nil {
		// If authorization fails, return empty credentials for public/anonymous access
		return &helmClient.RepoCreds{}, nil
	}

	return &helmClient.RepoCreds{
		Username: authConfig.Username,
		Password: authConfig.Password,
	}, nil
}
