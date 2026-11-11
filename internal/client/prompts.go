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
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

const (
	promptsPath        = "/api/svc/v1/prompts"
	promptVersionsPath = "/api/svc/v1/prompt-versions"
)

// PromptMessage is one message of a chat_prompt manifest. Content is kept as
// raw JSON because the wire format is a union (plain string, content part
// list, or blob storage reference); this provider models the plain-string
// variant only.
type PromptMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content,omitempty"`
	Name    string          `json:"name,omitempty"`
}

// PromptParameters are the optional inference parameters of a prompt's model
// configuration.
type PromptParameters struct {
	MaxTokens   *int64   `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopK        *int64   `json:"top_k,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

// PromptModelConfiguration is the model_configuration block of a chat_prompt
// manifest. Provider is the gateway provider account name and Model the
// model name within it (e.g. provider "my-account", model "gpt-5-mini").
type PromptModelConfiguration struct {
	Provider        string            `json:"provider"`
	Model           string            `json:"model"`
	Parameters      *PromptParameters `json:"parameters,omitempty"`
	ExtraParameters json.RawMessage   `json:"extra_parameters,omitempty"`
}

// PromptManifest is a chat_prompt manifest. Prompts are versioned ML repo
// artifacts; MlRepo names the repo the prompt lives in. The advanced blocks
// (tools, mcp_servers, guardrails, response_format, routing_config,
// cache_config, tool_call_to_mcp_mapping, logging_config, sub_agents) are
// not modeled.
type PromptManifest struct {
	Type               string                    `json:"type"`
	Name               string                    `json:"name"`
	MlRepo             string                    `json:"ml_repo"`
	Metadata           map[string]any            `json:"metadata"`
	Description        string                    `json:"description,omitempty"`
	VersionAlias       string                    `json:"version_alias,omitempty"`
	Messages           []PromptMessage           `json:"messages"`
	Variables          map[string]string         `json:"variables,omitempty"`
	ModelConfiguration *PromptModelConfiguration `json:"model_configuration,omitempty"`
}

// PromptVersion is one immutable version of a prompt. Each successful
// PUT /api/svc/v1/prompt-versions creates a new version and returns it.
// Like the agent-skill endpoint (and unlike agents), a dryRun flag is not
// supported, so no dryRun parameter is offered.
type PromptVersion struct {
	ID       string         `json:"id"`
	PromptID string         `json:"prompt_id"`
	FQN      string         `json:"fqn"` // e.g. chat_prompt:my-tenant/my-repo/my-prompt:3
	MlRepoID string         `json:"ml_repo_id"`
	Manifest PromptManifest `json:"manifest"`
}

// Prompt is one entry of GET /api/svc/v1/prompts. Like the agent-skills
// list, this endpoint uses snake_case field names and embeds the latest
// version under latest_version.
type Prompt struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	FQN           string         `json:"fqn"` // e.g. chat_prompt:my-tenant/my-repo/my-prompt
	MlRepoID      string         `json:"ml_repo_id"`
	LatestVersion *PromptVersion `json:"latest_version,omitempty"`
}

// ApplyPrompt creates a prompt or a new version of an existing one.
func (c *Client) ApplyPrompt(ctx context.Context, manifest PromptManifest) (*PromptVersion, error) {
	body := map[string]any{"manifest": manifest}
	var out struct {
		Data PromptVersion `json:"data"`
	}
	if err := c.do(ctx, http.MethodPut, promptVersionsPath, nil, body, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ListPrompts returns the tenant's prompts including their latest version
// details, following pagination. name optionally filters to the prompt with
// that exact name.
func (c *Client) ListPrompts(ctx context.Context, name string) ([]Prompt, error) {
	var all []Prompt
	offset := 0
	for {
		q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {"100"}}
		if name != "" {
			q.Set("name", name)
		}
		var page listIntegrationsResponse[Prompt]
		if err := c.do(ctx, http.MethodGet, promptsPath, q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		offset += len(page.Data)
		if len(page.Data) == 0 || offset >= page.Pagination.Total {
			return all, nil
		}
	}
}

// DeletePrompt deletes a prompt and all its versions by its control plane ID.
func (c *Client) DeletePrompt(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, promptsPath+"/"+url.PathEscape(id), nil, nil, nil)
}
