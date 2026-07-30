package client

import (
	"context"
	"net/http"
)

// RateLimitWhen scopes a rate limiting rule to matching requests. All given
// parts must match; an empty value matches every request.
type RateLimitWhen struct {
	Subjects []string          `json:"subjects,omitempty"`
	Models   []string          `json:"models,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// RateLimitRule caps the request or token rate of matching requests. Unit
// selects what is counted and over which period; RateLimitAppliesPer buckets
// the counter per entity instead of sharing one counter across all matching
// requests.
type RateLimitRule struct {
	ID                  string        `json:"id"`
	When                RateLimitWhen `json:"when"`
	LimitTo             int64         `json:"limit_to"`
	Unit                string        `json:"unit"`
	RateLimitAppliesPer []string      `json:"rate_limit_applies_per,omitempty"`
}

// GatewayRateLimitingConfigManifest is a "gateway-rate-limiting-config"
// manifest — a tenant-wide singleton holding all rate limiting rules of the
// AI gateway.
type GatewayRateLimitingConfigManifest struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Rules []RateLimitRule `json:"rules"`
}

// GatewayRateLimitingConfig is the stored gateway rate limiting config as
// returned by
// GET /api/svc/v1/llm-gateway/config/gateway-rate-limiting-config.
type GatewayRateLimitingConfig struct {
	ID         string                            `json:"id"`
	TenantName string                            `json:"tenantName"`
	Type       string                            `json:"type"`
	Manifest   GatewayRateLimitingConfigManifest `json:"manifest"`
}

// GetGatewayRateLimitingConfig returns the tenant's gateway rate limiting
// config, or nil if none is configured.
func (c *Client) GetGatewayRateLimitingConfig(ctx context.Context) (*GatewayRateLimitingConfig, error) {
	var out GatewayRateLimitingConfig
	err := c.do(ctx, http.MethodGet, "/api/svc/v1/llm-gateway/config/gateway-rate-limiting-config", nil, nil, &out)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if out.ID == "" && out.Manifest.Type == "" {
		return nil, nil
	}
	return &out, nil
}
