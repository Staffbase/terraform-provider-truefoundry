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

// OtelSpanAttributeFilter selects which spans an exporter emits by request
// metadata. Field is always "spanAttributes" and Operator always "IN".
type OtelSpanAttributeFilter struct {
	Field    string   `json:"field"`
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Value    []string `json:"value"`
}

// OtelExporterConfig is an OTLP exporter. Type is "http" or "grpc"; Encoding
// ("proto" or "json") only applies to HTTP. The traces-only fields are unset
// on a metrics exporter.
type OtelExporterConfig struct {
	Type     string            `json:"type"`
	Endpoint string            `json:"endpoint"`
	Encoding string            `json:"encoding,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`

	AdditionalResourceAttributes map[string]string         `json:"additional_resource_attributes,omitempty"`
	ExcludeRequestData           *bool                     `json:"exclude_request_data,omitempty"`
	When                         []OtelSpanAttributeFilter `json:"when,omitempty"`
}

// GatewayOtelConfigManifest is a "gateway-otel-config" manifest — a
// tenant-wide singleton forwarding the AI gateway's traces and metrics to
// OTLP collectors. Unlike the other gateway configs it carries no name.
type GatewayOtelConfigManifest struct {
	Type                      string              `json:"type"`
	OtelTracesExporterConfig  *OtelExporterConfig `json:"otel_traces_exporter_config,omitempty"`
	OtelMetricsExporterConfig *OtelExporterConfig `json:"otel_metrics_exporter_config,omitempty"`
}

// GatewayOtelConfig is the stored gateway OTel config as returned by
// GET /api/svc/v1/llm-gateway/config/gateway-otel-config.
type GatewayOtelConfig struct {
	ID         string                    `json:"id"`
	TenantName string                    `json:"tenantName"`
	Type       string                    `json:"type"`
	Manifest   GatewayOtelConfigManifest `json:"manifest"`
}

// GetGatewayOtelConfig returns the tenant's gateway OTel config, or nil if
// none is configured.
func (c *Client) GetGatewayOtelConfig(ctx context.Context) (*GatewayOtelConfig, error) {
	var out GatewayOtelConfig
	err := c.do(ctx, http.MethodGet, "/api/svc/v1/llm-gateway/config/gateway-otel-config", nil, nil, &out)
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
