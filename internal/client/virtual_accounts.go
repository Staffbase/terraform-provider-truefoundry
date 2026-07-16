package client

import (
	"context"
	"net/http"
	"net/url"
)

const virtualAccountsPath = "/api/svc/v1/virtual-accounts"

// Permission grants a role on a resource to a virtual account.
type Permission struct {
	ResourceFQN  string `json:"resource_fqn"`
	ResourceType string `json:"resource_type"`
	RoleID       string `json:"role_id"`
}

// SecretStoreConfig syncs the virtual account token into a secret store.
type SecretStoreConfig struct {
	IntegrationFQN string `json:"integration_fqn"`
	SecretPath     string `json:"secret_path"`
}

// VirtualAccountManifest is a "virtual-account" manifest. Auto-rotation,
// notification targets, ownership, and identity provider mappings are not
// modeled.
type VirtualAccountManifest struct {
	Name              string             `json:"name"`
	Type              string             `json:"type"` // always "virtual-account"
	Permissions       []Permission       `json:"permissions"`
	ExpirationDate    string             `json:"expiration_date,omitempty"`
	TokenType         string             `json:"token_type,omitempty"` // jwt or opaque
	Tags              map[string]string  `json:"tags,omitempty"`
	SecretStoreConfig *SecretStoreConfig `json:"secret_store_config,omitempty"`
}

// VirtualAccount is a virtual account as returned by the API.
type VirtualAccount struct {
	ID       string                 `json:"id"`
	Manifest VirtualAccountManifest `json:"manifest"`
}

type virtualAccountEnvelope struct {
	Data  VirtualAccount `json:"data"`
	Token string         `json:"token,omitempty"`
}

// ApplyVirtualAccount creates or updates a virtual account. The returned
// token is only present when the account was created.
func (c *Client) ApplyVirtualAccount(ctx context.Context, manifest VirtualAccountManifest, dryRun bool) (*VirtualAccount, string, error) {
	body := map[string]any{"manifest": manifest, "dryRun": dryRun}
	var out virtualAccountEnvelope
	if err := c.do(ctx, http.MethodPut, virtualAccountsPath, nil, body, &out); err != nil {
		return nil, "", err
	}
	return &out.Data, out.Token, nil
}

// GetVirtualAccount fetches a virtual account by ID.
func (c *Client) GetVirtualAccount(ctx context.Context, id string) (*VirtualAccount, error) {
	var out virtualAccountEnvelope
	if err := c.do(ctx, http.MethodGet, virtualAccountsPath+"/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// DeleteVirtualAccount deletes a virtual account by ID.
func (c *Client) DeleteVirtualAccount(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, virtualAccountsPath+"/"+url.PathEscape(id), nil, nil, nil)
}

// GetVirtualAccountToken fetches the current authentication token of a
// virtual account.
func (c *Client) GetVirtualAccountToken(ctx context.Context, id string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do(ctx, http.MethodGet, virtualAccountsPath+"/"+url.PathEscape(id)+"/token", nil, nil, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}
