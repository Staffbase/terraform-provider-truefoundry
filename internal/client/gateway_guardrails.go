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
	"errors"
	"net/http"
	"net/url"
)

// ErrFoldedGuardrailsRetired distinguishes a retired singleton endpoint from
// a missing config. Callers must stop refresh and retain state for migration.
var ErrFoldedGuardrailsRetired = errors.New("the folded gateway guardrails config endpoint is retired on control plane v0.179+ (folded format removal: 15 November 2026). Migrate to truefoundry_gateway_guardrails_policy, one policy per rule; see https://github.com/Staffbase/terraform-provider-truefoundry/blob/main/docs/guides/named-guardrails-policies.md")

// InNotInCondition matches a value against Values with Condition "in" or
// "not_in".
type InNotInCondition struct {
	Condition string   `json:"condition"`
	Values    []string `json:"values"`
}

// GuardrailsPolicyManifest is one named guardrail policy (control plane v0.179+).
// Unlike the deprecated folded config, it has no rules array.
type GuardrailsPolicyManifest struct {
	Type                        string         `json:"type"`
	Name                        string         `json:"name"`
	When                        GuardrailsWhen `json:"when"`
	LLMInputGuardrails          []string       `json:"llm_input_guardrails"`
	LLMOutputGuardrails         []string       `json:"llm_output_guardrails"`
	MCPToolPreInvokeGuardrails  []string       `json:"mcp_tool_pre_invoke_guardrails"`
	MCPToolPostInvokeGuardrails []string       `json:"mcp_tool_post_invoke_guardrails"`
	CustomErrorMessage          string         `json:"custom_error_message,omitempty"`
}

// GuardrailsPolicy is a stored named gateway configuration instance.
type GuardrailsPolicy struct {
	ID         string                   `json:"id"`
	TenantName string                   `json:"tenantName"`
	Type       string                   `json:"type"`
	Manifest   GuardrailsPolicyManifest `json:"manifest"`
}

const guardrailsPoliciesPath = "/api/svc/v1/llm-gateway/config/gateway-guardrails-config/instances"

// GetGuardrailsPolicy returns the named policy, or nil if it does not exist.
func (c *Client) GetGuardrailsPolicy(ctx context.Context, name string) (*GuardrailsPolicy, error) {
	var out GuardrailsPolicy
	err := c.do(ctx, http.MethodGet, guardrailsPoliciesPath+"/"+url.PathEscape(name), nil, nil, &out)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListGuardrailsPolicies reads the bare array returned by the instances endpoint.
func (c *Client) ListGuardrailsPolicies(ctx context.Context) ([]GuardrailsPolicy, error) {
	var out []GuardrailsPolicy
	if err := c.do(ctx, http.MethodGet, guardrailsPoliciesPath, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteGuardrailsPolicy deletes by name; a missing policy is already deleted.
func (c *Client) DeleteGuardrailsPolicy(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodDelete, guardrailsPoliciesPath+"/"+url.PathEscape(name), nil, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// MCPToolTarget names an MCP server and which of its tools are covered.
type MCPToolTarget struct {
	Name           string   `json:"name"`
	EnableAllTools bool     `json:"enable_all_tools"`
	EnabledTools   []string `json:"enabled_tools,omitempty"`
}

// MCPToolsCondition matches MCP tools with Condition "in" or "not_in".
type MCPToolsCondition struct {
	Condition string          `json:"condition"`
	Values    []MCPToolTarget `json:"values"`
}

// GuardrailsTargetClause is the per-kind target conditions of a guardrails
// rule. Field names are camelCase on the wire (SDK quirk).
type GuardrailsTargetClause struct {
	Model      *InNotInCondition  `json:"model,omitempty"`
	MCPServers *InNotInCondition  `json:"mcpServers,omitempty"`
	MCPTools   *MCPToolsCondition `json:"mcpTools,omitempty"`
}

// GuardrailsTargetGroup groups target conditions; the only supported
// operator is "or".
type GuardrailsTargetGroup struct {
	Operator   string                 `json:"operator"`
	Conditions GuardrailsTargetClause `json:"conditions"`
}

// GuardrailsSubjectClause matches calling subjects by FQN.
type GuardrailsSubjectClause struct {
	In    []string `json:"in,omitempty"`
	NotIn []string `json:"not_in,omitempty"`
}

// GuardrailsSubjectGroup groups subject conditions with operator "and" or
// "or".
type GuardrailsSubjectGroup struct {
	Operator   string                  `json:"operator"`
	Conditions GuardrailsSubjectClause `json:"conditions"`
}

// GuardrailsWhen scopes a guardrails rule; all given parts must match.
type GuardrailsWhen struct {
	Target   *GuardrailsTargetGroup  `json:"target,omitempty"`
	Subjects *GuardrailsSubjectGroup `json:"subjects,omitempty"`
	Metadata map[string]string       `json:"metadata,omitempty"`
}

// GuardrailsRule applies guardrails (referenced as
// "<guardrail-group>/<guardrail-config>") to matching requests. The four
// guardrail lists are required by the API and may be empty.
type GuardrailsRule struct {
	ID                          string         `json:"id"`
	When                        GuardrailsWhen `json:"when"`
	LLMInputGuardrails          []string       `json:"llm_input_guardrails"`
	LLMOutputGuardrails         []string       `json:"llm_output_guardrails"`
	MCPToolPreInvokeGuardrails  []string       `json:"mcp_tool_pre_invoke_guardrails"`
	MCPToolPostInvokeGuardrails []string       `json:"mcp_tool_post_invoke_guardrails"`
	CustomErrorMessage          string         `json:"custom_error_message,omitempty"`
}

// GatewayGuardrailsConfigManifest is a "gateway-guardrails-config" manifest
// — a tenant-wide singleton wiring guardrail integrations into the request
// flow of the AI gateway.
type GatewayGuardrailsConfigManifest struct {
	Type  string           `json:"type"`
	Name  string           `json:"name"`
	Rules []GuardrailsRule `json:"rules"`
}

// GatewayGuardrailsConfig is the stored gateway guardrails config as
// returned by GET /api/svc/v1/llm-gateway/config/gateway-guardrails-config.
type GatewayGuardrailsConfig struct {
	ID         string                          `json:"id"`
	TenantName string                          `json:"tenantName"`
	Type       string                          `json:"type"`
	Manifest   GatewayGuardrailsConfigManifest `json:"manifest"`
}

// GetGatewayGuardrailsConfig returns the tenant's gateway guardrails config,
// or nil if none is configured. An empty successful response on v0.179+
// returns ErrFoldedGuardrailsRetired, not a missing config.
func (c *Client) GetGatewayGuardrailsConfig(ctx context.Context) (*GatewayGuardrailsConfig, error) {
	var out GatewayGuardrailsConfig
	err := c.do(ctx, http.MethodGet, "/api/svc/v1/llm-gateway/config/gateway-guardrails-config", nil, nil, &out)
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
