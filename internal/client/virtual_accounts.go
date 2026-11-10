package client

import (
	"context"
	"net/http"
	"net/url"
)

const virtualAccountsPath = "/api/svc/v1/virtual-accounts"

// Permission grants a role on a resource to a virtual account.
//
// Deprecated on the control plane since v0.172.0 in favour of role-binding
// manifests and rejected after 15 Nov 2026. An omitted permissions field
// leaves the account's existing grants untouched; an empty list is rejected.
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

// AutoRotate turns on periodic control-plane-side token rotation for a
// virtual account. Its presence in the manifest is what activates rotation;
// there is no separate enabled flag. Shape per the published
// truefoundry-typescript-sdk / API docs (2026-09-10) — unverified against a
// real control plane: no live account with rotation was available, so there is
// no live account to dryRun apply against. Verify against a real control
// plane before relying on the exact field names/bounds here.
type AutoRotate struct {
	IntervalDays int64 `json:"auto_rotate_interval"` // days between rotations, minimum 30
	GracePeriod  int64 `json:"grace_period"`         // days the pre-rotation token stays valid, minimum 1
}

// VirtualAccountManifest is a "virtual-account" manifest. Notification
// targets, ownership, and identity provider mappings are not modeled.
type VirtualAccountManifest struct {
	Name              string             `json:"name"`
	Type              string             `json:"type"` // always "virtual-account"
	Permissions       []Permission       `json:"permissions,omitempty"`
	ExpirationDate    string             `json:"expiration_date,omitempty"`
	TokenType         string             `json:"token_type,omitempty"` // jwt or opaque
	Tags              map[string]string  `json:"tags,omitempty"`
	SecretStoreConfig *SecretStoreConfig `json:"secret_store_config,omitempty"`
	AutoRotate        *AutoRotate        `json:"auto_rotate,omitempty"`
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
