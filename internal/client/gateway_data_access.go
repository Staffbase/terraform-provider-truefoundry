// Copyright 2026, Staffbase SE and contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
//
// You may obtain a copy of the License at
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client

import (
	"context"
	"net/http"
)

// DataAccessFilter narrows an all_data access rule. Field "metadata" matches
// a metadata key against Values (Key is required), field "created_by"
// matches the creating subject against Values.
type DataAccessFilter struct {
	Field    string   `json:"field"`
	Key      string   `json:"key,omitempty"`
	Operator string   `json:"operator"` // always "IN"
	Value    []string `json:"value"`
}

// DataAccessRule is one rule of a gateway data access config: it grants
// subjects access to gateway observability data. Scope is "own_data",
// "team_data", or "all_data"; Filters apply to all_data rules only.
type DataAccessRule struct {
	ID          string             `json:"id"`
	Scope       string             `json:"scope"`
	Description string             `json:"description,omitempty"`
	Enabled     *bool              `json:"enabled,omitempty"`
	Subjects    []string           `json:"subjects"`
	DataTypes   []string           `json:"data_types"`
	Filters     []DataAccessFilter `json:"filters,omitempty"`
}

// GatewayDataAccessConfigManifest is a "gateway-data-access-config" manifest
// — a tenant-wide singleton controlling who can see traces and metrics of
// the AI gateway.
type GatewayDataAccessConfigManifest struct {
	Type  string           `json:"type"`
	Name  string           `json:"name"`
	Rules []DataAccessRule `json:"rules"`
}

// GatewayDataAccessConfig is the stored gateway data access config as
// returned by GET /api/svc/v1/llm-gateway/config/gateway-data-access-config.
type GatewayDataAccessConfig struct {
	ID         string                          `json:"id"`
	TenantName string                          `json:"tenantName"`
	Type       string                          `json:"type"`
	Manifest   GatewayDataAccessConfigManifest `json:"manifest"`
}

// GetGatewayDataAccessConfig returns the tenant's gateway data access
// config, or nil if none is configured.
func (c *Client) GetGatewayDataAccessConfig(ctx context.Context) (*GatewayDataAccessConfig, error) {
	var out GatewayDataAccessConfig
	err := c.do(ctx, http.MethodGet, "/api/svc/v1/llm-gateway/config/gateway-data-access-config", nil, nil, &out)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if out.ID == "" && len(out.Manifest.Rules) == 0 {
		return nil, nil
	}
	return &out, nil
}
