package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

const agentsPath = "/api/svc/v1/agents"

// AgentModelParams are the optional inference parameters of an agent's model.
type AgentModelParams struct {
	MaxTokens         *int64   `json:"max_tokens,omitempty"`
	ReasoningEffort   string   `json:"reasoning_effort,omitempty"`
	Temperature       *float64 `json:"temperature,omitempty"`
	TopP              *float64 `json:"top_p,omitempty"`
	TopK              *int64   `json:"top_k,omitempty"`
	ParallelToolCalls *bool    `json:"parallel_tool_calls,omitempty"`
}

// AgentModel is the model block of a truefoundry-agent manifest. Name is the
// gateway model, e.g. "my-account/gpt-5-mini".
type AgentModel struct {
	Name   string            `json:"name"`
	Params *AgentModelParams `json:"params,omitempty"`
}

// AgentSkillRef attaches an existing agent skill (by FQN) to an agent.
type AgentSkillRef struct {
	FQN     string `json:"fqn"`
	Preload bool   `json:"preload"`
}

// AgentMcpServer attaches a gateway MCP server (by name) to an agent. The
// tool lists accept tool names and the @all/@read/@write/@destructive groups.
type AgentMcpServer struct {
	Name                    string   `json:"name"`
	Preload                 *bool    `json:"preload,omitempty"`
	EnableTools             []string `json:"enable_tools,omitempty"`
	DisableTools            []string `json:"disable_tools,omitempty"`
	PreloadTools            []string `json:"preload_tools,omitempty"`
	RequireApprovalForTools []string `json:"require_approval_for_tools,omitempty"`
}

// AgentVariable declares a template variable usable in the agent's
// instructions and messages.
type AgentVariable struct {
	DefaultValue string `json:"default_value,omitempty"`
	Description  string `json:"description,omitempty"`
}

// SampleAgentInput is an example input shown in the agent playground.
type SampleAgentInput struct {
	Text      string            `json:"text,omitempty"`
	Variables map[string]string `json:"variables,omitempty"`
}

// AgentJSONSchema is the json_schema block of a "json_schema" response format.
type AgentJSONSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

// AgentResponseFormat constrains the agent's final response. Type "text" and
// "json_object" carry no further fields, "json_schema" carries JSONSchema.
type AgentResponseFormat struct {
	Type       string           `json:"type"`
	JSONSchema *AgentJSONSchema `json:"json_schema,omitempty"`
}

// AgentToggle is a config block consisting only of an enabled flag.
type AgentToggle struct {
	Enabled bool `json:"enabled"`
}

// AgentSandboxConfig enables the code execution sandbox for an agent.
type AgentSandboxConfig struct {
	Enabled       bool  `json:"enabled"`
	FileDownloads *bool `json:"file_downloads,omitempty"`
}

// AgentCompactionConfig configures conversation compaction.
type AgentCompactionConfig struct {
	Enabled                   bool   `json:"enabled"`
	CompactionThresholdTokens *int64 `json:"compaction_threshold_tokens,omitempty"`
}

// AgentContextManagement configures how the agent keeps its context small.
type AgentContextManagement struct {
	Compaction        *AgentCompactionConfig `json:"compaction,omitempty"`
	LargeToolResponse *AgentToggle           `json:"large_tool_response,omitempty"`
}

// AgentConfig is the runtime config block of a truefoundry-agent manifest.
type AgentConfig struct {
	IterationLimit    *int64                  `json:"iteration_limit,omitempty"`
	Sandbox           *AgentSandboxConfig     `json:"sandbox,omitempty"`
	DynamicSubAgents  *AgentToggle            `json:"dynamic_sub_agents,omitempty"`
	ContextManagement *AgentContextManagement `json:"context_management,omitempty"`
	GenerativeUI      *AgentToggle            `json:"generative_ui,omitempty"`
	AskUserQuestions  *AgentToggle            `json:"ask_user_questions,omitempty"`
}

// AgentServerAuth is the auth_data block of a self-hosted remote agent:
// type "header" sends static Headers (raw secrets or TrueFoundry secret
// references), type "passthrough" forwards the caller's credentials.
type AgentServerAuth struct {
	Type    string            `json:"type"`
	Headers map[string]string `json:"headers,omitempty"`
}

// AgentFramework describes the protocol a self-hosted remote agent speaks:
// type "a2a" with the agent card path, or "custom".
type AgentFramework struct {
	Type          string `json:"type"`
	AgentCardPath string `json:"agent_card_path,omitempty"`
}

// AgentManifest is an agent manifest, type "truefoundry-agent"
// (Model through Config) or "remote-agent/self-hosted" (URL/AuthData/
// Framework). The legacy "agent" type (hosted A2A / prompt-sourced) and the
// messages/ownedBy fields are not modeled.
type AgentManifest struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`

	// truefoundry-agent
	Model          *AgentModel              `json:"model,omitempty"`
	Instructions   string                   `json:"instructions,omitempty"`
	Skills         []AgentSkillRef          `json:"skills,omitempty"`
	McpServers     []AgentMcpServer         `json:"mcp_servers,omitempty"`
	Variables      map[string]AgentVariable `json:"variables,omitempty"`
	SampleInputs   []SampleAgentInput       `json:"sample_inputs,omitempty"`
	ResponseFormat *AgentResponseFormat     `json:"response_format,omitempty"`
	Config         *AgentConfig             `json:"config,omitempty"`

	// remote-agent/self-hosted
	URL       string           `json:"url,omitempty"`
	AuthData  *AgentServerAuth `json:"auth_data,omitempty"`
	Framework *AgentFramework  `json:"framework,omitempty"`

	Collaborators []Collaborator    `json:"collaborators"`
	Tags          map[string]string `json:"tags,omitempty"`
}

// AgentVersion is one immutable version of an agent. Each successful
// PUT /api/svc/v1/agents creates a new version and returns it.
type AgentVersion struct {
	ID       string        `json:"id"`
	AgentID  string        `json:"agentId"`
	FQN      string        `json:"fqn"` // e.g. my-tenant:agent:my-agent:3
	Manifest AgentManifest `json:"manifest"`
	Version  int64         `json:"version"`
}

// Agent is one entry of GET /api/svc/v1/agents.
type Agent struct {
	ID                   string        `json:"id"`
	Name                 string        `json:"name"`
	FQN                  string        `json:"fqn"` // e.g. my-tenant:agent:my-agent
	Type                 string        `json:"type"`
	LatestVersion        int64         `json:"latestVersion"`
	LatestVersionDetails *AgentVersion `json:"latestVersionDetails,omitempty"`
}

// ApplyAgent creates an agent or a new version of an existing one.
func (c *Client) ApplyAgent(ctx context.Context, manifest AgentManifest, dryRun bool) (*AgentVersion, error) {
	body := map[string]any{"manifest": manifest, "dryRun": dryRun}
	var out struct {
		Data AgentVersion `json:"data"`
	}
	if err := c.do(ctx, http.MethodPut, agentsPath, nil, body, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ListAgents returns the tenant's agents including their latest version
// details, following pagination. name optionally filters to the agent with
// that exact name.
func (c *Client) ListAgents(ctx context.Context, name string) ([]Agent, error) {
	var all []Agent
	offset := 0
	for {
		q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {"100"}}
		if name != "" {
			q.Set("name", name)
		}
		var page listIntegrationsResponse[Agent]
		if err := c.do(ctx, http.MethodGet, agentsPath, q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		offset += len(page.Data)
		if len(page.Data) == 0 || offset >= page.Pagination.Total {
			return all, nil
		}
	}
}

// DeleteAgent deletes an agent and all its versions by its control plane ID.
func (c *Client) DeleteAgent(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, agentsPath+"/"+url.PathEscape(id), nil, nil, nil)
}
