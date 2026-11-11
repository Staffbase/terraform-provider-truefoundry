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

// GatewayRetrySettings configures automatic request retries of the AI
// gateway. Delay is the wait between attempts in milliseconds; OnStatusCodes
// lists the HTTP status codes (as strings, e.g. "429") that trigger a retry.
type GatewayRetrySettings struct {
	Attempts      int64    `json:"attempts"`
	Delay         *int64   `json:"delay,omitempty"`
	OnStatusCodes []string `json:"on_status_codes,omitempty"`
}

// GatewayLoggingSettings configures request/response logging. Type is
// "always", "never", or "header_controlled"; LogBodyByDefault applies to
// header_controlled only and sets the behavior when no header overrides it.
type GatewayLoggingSettings struct {
	Type             string `json:"type"`
	LogBodyByDefault *bool  `json:"log_body_by_default,omitempty"`
}

// GatewaySemanticCacheSettings configures the semantic response cache;
// SemanticCacheEmbeddingModel is the FQN of the embedding model it uses.
type GatewaySemanticCacheSettings struct {
	SemanticCacheEmbeddingModel string `json:"semantic_cache_embedding_model"`
}

// GatewayGuardrailSettings configures guardrail execution.
type GatewayGuardrailSettings struct {
	TimeoutMS               *int64 `json:"timeout_ms,omitempty"`
	EnableParallelExecution *bool  `json:"enable_parallel_execution,omitempty"`
}

// GatewayModelConfiguration restricts which models the gateway serves. Not in
// the published TypeScript SDK yet; shape taken from a deployed control
// plane. AllowedModels has no omitempty: an empty allowlist is meaningful and
// must serialize as [].
type GatewayModelConfiguration struct {
	AllowedModels []string `json:"allowed_models"`
}

// GatewayGlobalSettingsManifest is a "gateway-global-settings" manifest — a
// tenant-wide singleton holding global AI gateway settings. Unlike the other
// gateway config manifests it has no name field.
type GatewayGlobalSettingsManifest struct {
	Type                  string                        `json:"type"`
	RetrySettings         *GatewayRetrySettings         `json:"retry_settings,omitempty"`
	LoggingSettings       *GatewayLoggingSettings       `json:"logging_settings,omitempty"`
	SemanticCacheSettings *GatewaySemanticCacheSettings `json:"semantic_cache_settings,omitempty"`
	GuardrailSettings     *GatewayGuardrailSettings     `json:"guardrail_settings,omitempty"`
	ModelConfiguration    *GatewayModelConfiguration    `json:"model_configuration,omitempty"`
}

// GatewayGlobalSettings is the stored gateway global settings as returned by
// GET /api/svc/v1/llm-gateway/config/gateway-global-settings.
type GatewayGlobalSettings struct {
	ID         string                        `json:"id"`
	TenantName string                        `json:"tenantName"`
	Type       string                        `json:"type"`
	Manifest   GatewayGlobalSettingsManifest `json:"manifest"`
}

// GetGatewayGlobalSettings returns the tenant's gateway global settings, or
// nil if none are configured. A stored manifest with all settings unset still
// has an ID and counts as existing.
func (c *Client) GetGatewayGlobalSettings(ctx context.Context) (*GatewayGlobalSettings, error) {
	var out GatewayGlobalSettings
	err := c.do(ctx, http.MethodGet, "/api/svc/v1/llm-gateway/config/gateway-global-settings", nil, nil, &out)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, nil
	}
	return &out, nil
}
