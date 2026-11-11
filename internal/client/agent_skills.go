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
	"net/url"
	"strconv"
)

const (
	agentSkillsPath        = "/api/svc/v1/agent-skills"
	agentSkillVersionsPath = "/api/svc/v1/agent-skill-versions"
)

// AgentSkillSource is the source block of an agent-skill manifest: type
// "inline" carries SkillMd (a SKILL.md document that must start with YAML
// frontmatter whose name matches the manifest name), type "blob-storage"
// carries Description and URI. The control plane ingests every applied
// source and stores it as a blob-storage source with a TrueFoundry-managed
// URI, so applied sources do not round-trip through reads. The "local"
// source type is CLI-only and not modeled.
type AgentSkillSource struct {
	Type        string `json:"type"`
	SkillMd     string `json:"skill_md,omitempty"`
	Description string `json:"description,omitempty"`
	URI         string `json:"uri,omitempty"`
}

// AgentSkillManifest is an agent-skill manifest. Agent skills are versioned
// ML repo artifacts; MlRepo names the repo the skill lives in.
type AgentSkillManifest struct {
	Type     string            `json:"type"`
	Name     string            `json:"name"`
	MlRepo   string            `json:"ml_repo"`
	Metadata map[string]any    `json:"metadata"`
	Source   *AgentSkillSource `json:"source,omitempty"`
}

// AgentSkillVersion is one immutable version of an agent skill. Each
// successful PUT /api/svc/v1/agent-skill-versions creates a new version and
// returns it. Unlike the agents endpoint, this endpoint ignores a dryRun
// flag and always persists (verified against a real control plane
// 2026-07-27), so no dryRun parameter is offered.
type AgentSkillVersion struct {
	ID           string             `json:"id"`
	AgentSkillID string             `json:"agent_skill_id"`
	FQN          string             `json:"fqn"` // e.g. agent-skill:my-tenant/my-repo/my-skill:3
	MlRepoID     string             `json:"ml_repo_id"`
	Manifest     AgentSkillManifest `json:"manifest"`
}

// AgentSkill is one entry of GET /api/svc/v1/agent-skills. Unlike the agents
// list, this endpoint uses snake_case field names and embeds the latest
// version under latest_version.
type AgentSkill struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	FQN           string             `json:"fqn"` // e.g. agent-skill:my-tenant/my-repo/my-skill
	MlRepoID      string             `json:"ml_repo_id"`
	LatestVersion *AgentSkillVersion `json:"latest_version,omitempty"`
}

// ApplyAgentSkill creates an agent skill or a new version of an existing one.
func (c *Client) ApplyAgentSkill(ctx context.Context, manifest AgentSkillManifest) (*AgentSkillVersion, error) {
	body := map[string]any{"manifest": manifest}
	var out struct {
		Data AgentSkillVersion `json:"data"`
	}
	if err := c.do(ctx, http.MethodPut, agentSkillVersionsPath, nil, body, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ListAgentSkills returns the tenant's agent skills including their latest
// version details, following pagination. name optionally filters to the
// skill with that exact name.
func (c *Client) ListAgentSkills(ctx context.Context, name string) ([]AgentSkill, error) {
	var all []AgentSkill
	offset := 0
	for {
		q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {"100"}}
		if name != "" {
			q.Set("name", name)
		}
		var page listIntegrationsResponse[AgentSkill]
		if err := c.do(ctx, http.MethodGet, agentSkillsPath, q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		offset += len(page.Data)
		if len(page.Data) == 0 || offset >= page.Pagination.Total {
			return all, nil
		}
	}
}

// DeleteAgentSkill deletes an agent skill and all its versions by its
// control plane ID.
func (c *Client) DeleteAgentSkill(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, agentSkillsPath+"/"+url.PathEscape(id), nil, nil, nil)
}
