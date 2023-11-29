package azure

import (
	"context"
	"encoding/json"
	"github.com/Azure/kubelogin/pkg/token"
	"github.com/pkg/errors"
	"k8s.io/client-go/rest"
	"net/http"
)

// Credentials Secret content is a json whose keys are below.
const (
	CredentialsKeyClientID                       = "clientId"
	CredentialsKeyClientSecret                   = "clientSecret"
	CredentialsKeyTenantID                       = "tenantId"
	CredentialsKeySubscriptionID                 = "subscriptionId"
	CredentialsKeyActiveDirectoryEndpointURL     = "activeDirectoryEndpointUrl"
	CredentialsKeyResourceManagerEndpointURL     = "resourceManagerEndpointUrl"
	CredentialsKeyActiveDirectoryGraphResourceID = "activeDirectoryGraphResourceId"
	CredentialsKeySQLManagementEndpointURL       = "sqlManagementEndpointUrl"
	CredentialsKeyGalleryEndpointURL             = "galleryEndpointUrl"
	CredentialsManagementEndpointURL             = "managementEndpointUrl"

	ServicePrincipalLogin = "spn"
	AzureCLILogin         = "azurecli"
)

func WrapRESTConfig(_ context.Context, rc *rest.Config, credentials []byte, _ ...string) error {
	m := map[string]string{}
	if err := json.Unmarshal(credentials, &m); err != nil {
		return err
	}
	rc.ExecProvider = nil

	opts := &token.Options{
		LoginMethod:  ServicePrincipalLogin,
		ClientID:     m[CredentialsKeyClientID],
		ClientSecret: m[CredentialsKeyClientSecret],
		TenantID:     m[CredentialsKeyTenantID],
		ServerID:     "6dae42f8-4368-4678-94ff-3960e28e3630",
	}

	p, err := token.NewTokenProvider(opts)
	if err != nil {
		return errors.New("cannot build azure token provider")
	}

	rc.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &tokenTransport{Provider: p, Base: rt}
	})

	return nil
}
