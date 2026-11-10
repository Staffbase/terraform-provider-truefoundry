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

// LoadBalancingWhen scopes a load balancing rule to matching requests. All
// given parts must match; an empty value matches every request.
type LoadBalancingWhen struct {
	Subjects []string          `json:"subjects,omitempty"`
	Models   []string          `json:"models,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// LoadBalancingRule spreads matching requests over target models. Type is the
// routing strategy ("weight-based-routing", "latency-based-routing", or
// "priority-based-routing") and decides which target fields apply.
type LoadBalancingRule struct {
	ID                 string            `json:"id"`
	Type               string            `json:"type"`
	When               LoadBalancingWhen `json:"when"`
	LoadBalanceTargets []RoutingTarget   `json:"load_balance_targets"`
}

// GatewayLoadBalancingConfigManifest is a "gateway-load-balancing-config"
// manifest — a tenant-wide singleton holding all load balancing rules of the
// AI gateway.
type GatewayLoadBalancingConfigManifest struct {
	Type  string              `json:"type"`
	Name  string              `json:"name"`
	Rules []LoadBalancingRule `json:"rules"`
}

// GatewayLoadBalancingConfig is the stored gateway load balancing config as
// returned by
// GET /api/svc/v1/llm-gateway/config/gateway-load-balancing-config.
type GatewayLoadBalancingConfig struct {
	ID         string                             `json:"id"`
	TenantName string                             `json:"tenantName"`
	Type       string                             `json:"type"`
	Manifest   GatewayLoadBalancingConfigManifest `json:"manifest"`
}

// GetGatewayLoadBalancingConfig returns the tenant's gateway load balancing
// config, or nil if none is configured.
func (c *Client) GetGatewayLoadBalancingConfig(ctx context.Context) (*GatewayLoadBalancingConfig, error) {
	var out GatewayLoadBalancingConfig
	err := c.do(ctx, http.MethodGet, "/api/svc/v1/llm-gateway/config/gateway-load-balancing-config", nil, nil, &out)
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
