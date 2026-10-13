package client

import (
	"context"
	"net/http"
	"net/url"
)

// Gateway installations ("gateway-installation" manifests) register a
// self-hosted gateway plane and its URL on the control plane (Settings ->
// Gateway Endpoints). Verified against a self-hosted control plane on
// 2026-10-12:
//
//	PUT    /api/svc/v1/apply                                 – create/update (honors dryRun)
//	GET    /api/svc/v1/llm-gateway/installations             – list, {data: [...]}, no pagination, ignores ?name=
//	GET    /api/svc/v1/llm-gateway/installations/{id}        – one installation, {data: {...}}
//	DELETE /api/svc/v1/llm-gateway/installations/{id}        – the generic delete endpoint rejects this type
//	GET    /api/svc/v1/llm-gateway/installations/token?name= – mint the gateway's token, {"token": "..."}
const gatewayInstallationsPath = "/api/svc/v1/llm-gateway/installations"

// GatewayInstallationManifest is a "gateway-installation" manifest. IsDefault
// is required by the control plane, so it is never omitted.
type GatewayInstallationManifest struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Enabled   bool   `json:"enabled"`
	IsDefault bool   `json:"isDefault"`
}

// GatewayInstallation is a gateway installation as returned by the
// installations endpoints. The stored manifest may omit isDefault, so the
// top-level IsDefault is authoritative.
type GatewayInstallation struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	Manifest  struct {
		URL     string `json:"url"`
		Enabled bool   `json:"enabled"`
	} `json:"manifest"`
}

// ListGatewayInstallations returns the tenant's gateway installations.
func (c *Client) ListGatewayInstallations(ctx context.Context) ([]GatewayInstallation, error) {
	var out struct {
		Data []GatewayInstallation `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, gatewayInstallationsPath, nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// GetGatewayInstallation fetches a gateway installation by ID.
func (c *Client) GetGatewayInstallation(ctx context.Context, id string) (*GatewayInstallation, error) {
	var out struct {
		Data GatewayInstallation `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, gatewayInstallationsPath+"/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// DeleteGatewayInstallation deletes a gateway installation by ID.
func (c *Client) DeleteGatewayInstallation(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, gatewayInstallationsPath+"/"+url.PathEscape(id), nil, nil, nil)
}

// GatewayInstallationToken mints a token the gateway plane authenticates to
// the control plane with. TrueFoundry documents it as a one-time call; the
// provider calls it only when it creates an installation.
func (c *Client) GatewayInstallationToken(ctx context.Context, name string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	q := url.Values{"name": {name}}
	if err := c.do(ctx, http.MethodGet, gatewayInstallationsPath+"/token", q, nil, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}
