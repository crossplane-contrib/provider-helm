package azure

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Azure/kubelogin/pkg/token"
	"github.com/pkg/errors"
	"github.com/spf13/pflag"
	"k8s.io/client-go/rest"
)

// Credentials Secret content is a json whose keys are below.
const (
	CredentialsKeyClientID       = "clientId"
	CredentialsKeyClientSecret   = "clientSecret"
	CredentialsKeyTenantID       = "tenantId"
	CredentialsKeyClientCert     = "clientCertificate"
	CredentialsKeyClientCertPass = "clientCertificatePassword"

	kubeloginCLIFlagServerID = "server-id"
)

func kubeloginTokenOptionsFromRESTConfig(rc *rest.Config) (*token.Options, error) {
	opts := &token.Options{}

	// opts are filled according to the provided args in the execProvider section of the kubeconfig
	// we are parsing serverID from here
	// add other flags if new login methods are introduced
	fs := pflag.NewFlagSet("kubelogin", pflag.ContinueOnError)
	fs.ParseErrorsWhitelist = pflag.ParseErrorsWhitelist{UnknownFlags: true}
	fs.StringVar(&opts.ServerID, kubeloginCLIFlagServerID, "", "Microsoft Entra (AAD) server application id")
	err := fs.Parse(rc.ExecProvider.Args)
	if err != nil {
		return nil, errors.Wrap(err, "could not parse execProvider arguments in kubeconfig")
	}

	return opts, nil
}

func WrapRESTConfig(_ context.Context, rc *rest.Config, credentials []byte, _ ...string) error {
	m := map[string]string{}
	if err := json.Unmarshal(credentials, &m); err != nil {
		return err
	}

	opts, err := kubeloginTokenOptionsFromRESTConfig(rc)
	if err != nil {
		return err
	}
	rc.ExecProvider = nil

	// TODO: support other login methods like MSI, Workload Identity in the future
	opts.LoginMethod = token.ServicePrincipalLogin
	opts.ClientID = m[CredentialsKeyClientID]
	opts.ClientSecret = m[CredentialsKeyClientSecret]
	opts.TenantID = m[CredentialsKeyTenantID]
	if cert, ok := m[CredentialsKeyClientCert]; ok {
		opts.ClientCert = cert
		if certpass, ok2 := m[CredentialsKeyClientCertPass]; ok2 {
			opts.ClientCertPassword = certpass
		}
	}

	p, err := token.GetTokenProvider(opts)
	if err != nil {
		return errors.Wrap(err, "cannot build azure token provider")
	}

	rc.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &tokenTransport{Provider: p, Base: rt}
	})

	return nil
}
