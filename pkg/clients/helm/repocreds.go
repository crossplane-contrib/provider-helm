package helm

import registryAuth "oras.land/oras-go/v2/registry/remote/auth"

// RepoCreds keeps auth information to access a Helm Chart
type RepoCreds struct {
	Username string
	Password string

	// IdentityToken is a Docker credential-helper identity token.
	// ORAS models this value as a refresh token.
	// For OCI chart pulls, identity-token credentials take precedence over
	// Helm's basic-auth login path.
	IdentityToken string
}

func (c *RepoCreds) hasBasicAuth() bool {
	return c != nil && c.Username != "" && c.Password != ""
}

func (c *RepoCreds) hasIdentityToken() bool {
	return c != nil && c.IdentityToken != ""
}

func (c *RepoCreds) registryCredential() registryAuth.Credential {
	if c == nil {
		return registryAuth.EmptyCredential
	}

	return registryAuth.Credential{
		Username:     c.Username,
		Password:     c.Password,
		RefreshToken: c.IdentityToken,
	}
}
