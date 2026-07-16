package client

import (
	"context"
	"net/http"
)

// DataRoutingStorage is where routed gateway data is stored: type
// "controlplane-managed" (optionally pinned to a Region: "us", "australia",
// or "india") or "customer-managed" (StorageIntegrationFQN references a blob
// storage integration).
type DataRoutingStorage struct {
	Type                  string `json:"type"`
	Region                string `json:"region,omitempty"`
	StorageIntegrationFQN string `json:"storage_integration_fqn,omitempty"`
}

// DataRoutingTarget is a storage plus retention settings; used as the
// default route of a data routing config.
type DataRoutingTarget struct {
	Storage                   DataRoutingStorage `json:"storage"`
	TracesRetentionPeriodDays *int64             `json:"traces_retention_period_days,omitempty"`
}

// DataRoutingDestination routes gateway data of matching requests to a
// storage. When conditions share the DataAccessFilter shape.
type DataRoutingDestination struct {
	Name                      string             `json:"name"`
	Enabled                   bool               `json:"enabled"`
	Description               string             `json:"description,omitempty"`
	When                      []DataAccessFilter `json:"when"`
	Storage                   DataRoutingStorage `json:"storage"`
	TracesRetentionPeriodDays *int64             `json:"traces_retention_period_days,omitempty"`
}

// GatewayDataRoutingConfigManifest is a "gateway-data-routing-config"
// manifest — a tenant-wide singleton routing the gateway's observability
// data to storages.
type GatewayDataRoutingConfigManifest struct {
	Type         string                   `json:"type"`
	Name         string                   `json:"name"`
	Default      DataRoutingTarget        `json:"default"`
	Destinations []DataRoutingDestination `json:"destinations,omitempty"`
}

// GatewayDataRoutingConfig is the stored gateway data routing config as
// returned by GET /api/svc/v1/llm-gateway/config/gateway-data-routing-config.
type GatewayDataRoutingConfig struct {
	ID         string                           `json:"id"`
	TenantName string                           `json:"tenantName"`
	Type       string                           `json:"type"`
	Manifest   GatewayDataRoutingConfigManifest `json:"manifest"`
}

// GetGatewayDataRoutingConfig returns the tenant's gateway data routing
// config, or nil if none is configured.
func (c *Client) GetGatewayDataRoutingConfig(ctx context.Context) (*GatewayDataRoutingConfig, error) {
	var out GatewayDataRoutingConfig
	err := c.do(ctx, http.MethodGet, "/api/svc/v1/llm-gateway/config/gateway-data-routing-config", nil, nil, &out)
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
