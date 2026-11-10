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

package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

var (
	_ resource.Resource                   = (*agentResource)(nil)
	_ resource.ResourceWithConfigure      = (*agentResource)(nil)
	_ resource.ResourceWithValidateConfig = (*agentResource)(nil)
	_ resource.ResourceWithImportState    = (*agentResource)(nil)
)

// agentNameRegexp is (a Go-compatible equivalent of) the agent name pattern
// the control plane enforces: `^[a-z](?:[a-z0-9]|-(?!-)){1,30}[a-z0-9]$`.
// Go regexp has no lookahead, so double hyphens are excluded structurally and
// the 3-32 length is enforced by a separate validator.
var agentNameRegexp = regexp.MustCompile(`^[a-z](-?[a-z0-9]){2,31}$`)

// responseFormatTypeValues are the supported agent response format types.
var responseFormatTypeValues = []string{"text", "json_schema", "json_object"}

func newAgentResource() resource.Resource {
	return &agentResource{}
}

// agentResource manages a TrueFoundry-hosted AI Gateway agent
// ("truefoundry-agent" manifests) via the versioned PUT/GET/DELETE
// /api/svc/v1/agents endpoints.
type agentResource struct {
	client *client.Client
}

type agentResourceModel struct {
	ID             types.String                  `tfsdk:"id"`
	FQN            types.String                  `tfsdk:"fqn"`
	Name           types.String                  `tfsdk:"name"`
	Description    types.String                  `tfsdk:"description"`
	Model          *agentModelModel              `tfsdk:"model"`
	Instructions   types.String                  `tfsdk:"instructions"`
	Skills         []agentSkillModel             `tfsdk:"skills"`
	McpServers     []agentMcpServerModel         `tfsdk:"mcp_servers"`
	Variables      map[string]agentVariableModel `tfsdk:"variables"`
	SampleInputs   []agentSampleInputModel       `tfsdk:"sample_inputs"`
	ResponseFormat *agentResponseFormatModel     `tfsdk:"response_format"`
	Config         *agentConfigModel             `tfsdk:"config"`
	Collaborators  []collaboratorModel           `tfsdk:"collaborators"`
	Tags           map[string]types.String       `tfsdk:"tags"`
}

type agentModelModel struct {
	Name   types.String           `tfsdk:"name"`
	Params *agentModelParamsModel `tfsdk:"params"`
}

type agentModelParamsModel struct {
	MaxTokens         types.Int64   `tfsdk:"max_tokens"`
	ReasoningEffort   types.String  `tfsdk:"reasoning_effort"`
	Temperature       types.Float64 `tfsdk:"temperature"`
	TopP              types.Float64 `tfsdk:"top_p"`
	TopK              types.Int64   `tfsdk:"top_k"`
	ParallelToolCalls types.Bool    `tfsdk:"parallel_tool_calls"`
}

type agentSkillModel struct {
	FQN     types.String `tfsdk:"fqn"`
	Preload types.Bool   `tfsdk:"preload"`
}

type agentMcpServerModel struct {
	Name                    types.String   `tfsdk:"name"`
	Preload                 types.Bool     `tfsdk:"preload"`
	EnableTools             []types.String `tfsdk:"enable_tools"`
	DisableTools            []types.String `tfsdk:"disable_tools"`
	PreloadTools            []types.String `tfsdk:"preload_tools"`
	RequireApprovalForTools []types.String `tfsdk:"require_approval_for_tools"`
}

type agentVariableModel struct {
	DefaultValue types.String `tfsdk:"default_value"`
	Description  types.String `tfsdk:"description"`
}

type agentSampleInputModel struct {
	Text      types.String            `tfsdk:"text"`
	Variables map[string]types.String `tfsdk:"variables"`
}

type agentResponseFormatModel struct {
	Type       types.String          `tfsdk:"type"`
	JSONSchema *agentJSONSchemaModel `tfsdk:"json_schema"`
}

type agentJSONSchemaModel struct {
	Name        types.String         `tfsdk:"name"`
	Description types.String         `tfsdk:"description"`
	Schema      jsontypes.Normalized `tfsdk:"schema"`
	Strict      types.Bool           `tfsdk:"strict"`
}

type agentConfigModel struct {
	IterationLimit    types.Int64                  `tfsdk:"iteration_limit"`
	Sandbox           *agentSandboxModel           `tfsdk:"sandbox"`
	DynamicSubAgents  *agentToggleModel            `tfsdk:"dynamic_sub_agents"`
	ContextManagement *agentContextManagementModel `tfsdk:"context_management"`
	GenerativeUI      *agentToggleModel            `tfsdk:"generative_ui"`
	AskUserQuestions  *agentToggleModel            `tfsdk:"ask_user_questions"`
}

type agentSandboxModel struct {
	Enabled       types.Bool `tfsdk:"enabled"`
	FileDownloads types.Bool `tfsdk:"file_downloads"`
}

type agentToggleModel struct {
	Enabled types.Bool `tfsdk:"enabled"`
}

type agentContextManagementModel struct {
	Compaction        *agentCompactionModel `tfsdk:"compaction"`
	LargeToolResponse *agentToggleModel     `tfsdk:"large_tool_response"`
}

type agentCompactionModel struct {
	Enabled                   types.Bool  `tfsdk:"enabled"`
	CompactionThresholdTokens types.Int64 `tfsdk:"compaction_threshold_tokens"`
}

func (r *agentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

// agentNameAttribute is the shared name attribute of both agent resources.
func agentNameAttribute(kind string) schema.StringAttribute {
	return schema.StringAttribute{
		Required: true,
		Description: fmt.Sprintf("Name of the %s (3-32 characters, lowercase alphanumeric and single hyphens). "+
			"Changing it replaces the agent.", kind),
		Validators: []validator.String{
			stringvalidator.LengthBetween(minNameLen, maxAgentNameLen),
			stringvalidator.RegexMatches(agentNameRegexp, "must be lowercase alphanumeric with single hyphens"),
		},
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

// agentToggleAttribute is a nested config block consisting of an enabled flag.
func agentToggleAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: description,
		Attributes: map[string]schema.Attribute{
			"enabled": schema.BoolAttribute{
				Required:    true,
				Description: "Enable this feature.",
			},
		},
	}
}

// toolListAttribute is one of the per-MCP-server tool selection lists.
func toolListAttribute(description string) schema.ListAttribute {
	return schema.ListAttribute{
		Optional:    true,
		ElementType: types.StringType,
		Description: description + " Accepts tool names and the `@all`, `@read`, `@write`, and `@destructive` groups.",
		Validators:  []validator.List{listvalidator.SizeAtLeast(1)},
	}
}

func (r *agentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry-hosted AI Gateway agent (`truefoundry-agent` manifest) via " +
			"`PUT /api/svc/v1/agents`. Every update creates a new immutable agent version on the control plane. " +
			"Self-hosted agents are managed with `truefoundry_remote_agent`; agent skills and the legacy `agent` " +
			"manifest type are not supported. Existing agents can be imported by their name, e.g. " +
			"`terraform import truefoundry_agent.example my-agent`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the agent.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed:      true,
				Description:   "Fully qualified name of the agent, e.g. my-tenant:agent:my-agent.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": agentNameAttribute("agent"),
			"description": schema.StringAttribute{
				Required:    true,
				Description: "Human-readable description of what the agent does.",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"model": schema.SingleNestedAttribute{
				Required:    true,
				Description: "The gateway model the agent runs on.",
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Required:    true,
						Description: "Gateway model name in provider-account/model form, e.g. my-account/gpt-5-mini.",
						Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
					},
					"params": schema.SingleNestedAttribute{
						Optional:    true,
						Description: "Inference parameters passed to the model.",
						Attributes: map[string]schema.Attribute{
							"max_tokens": schema.Int64Attribute{
								Optional:    true,
								Description: "Maximum number of tokens the model may generate per turn.",
							},
							"reasoning_effort": schema.StringAttribute{
								Optional:    true,
								Description: "Reasoning effort for reasoning models, e.g. low, medium, or high.",
							},
							"temperature": schema.Float64Attribute{
								Optional:    true,
								Description: "Sampling temperature.",
							},
							"top_p": schema.Float64Attribute{
								Optional:    true,
								Description: "Nucleus sampling probability mass.",
							},
							"top_k": schema.Int64Attribute{
								Optional:    true,
								Description: "Top-k sampling cutoff.",
							},
							"parallel_tool_calls": schema.BoolAttribute{
								Optional:    true,
								Description: "Allow the model to call multiple tools in parallel.",
							},
						},
					},
				},
			},
			"instructions": schema.StringAttribute{
				Optional:    true,
				Description: "System instructions for the agent. May reference variables as {{name}}.",
			},
			"skills": schema.ListNestedAttribute{
				Optional:      true,
				Description:   "Agent skills attached to the agent, referenced by their FQN.",
				Validators:    []validator.List{listvalidator.SizeAtLeast(1)},
				PlanModifiers: []planmodifier.List{orderInsensitive()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"fqn": schema.StringAttribute{
							Required:    true,
							Description: "FQN of the agent skill.",
							Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
						},
						"preload": schema.BoolAttribute{
							Required:    true,
							Description: "Load the skill's content into the agent's context up front instead of on demand.",
						},
					},
				},
			},
			"mcp_servers": schema.ListNestedAttribute{
				Optional:      true,
				Description:   "Gateway MCP servers the agent may use, referenced by name. They must exist on the same tenant.",
				Validators:    []validator.List{listvalidator.SizeAtLeast(1)},
				PlanModifiers: []planmodifier.List{orderInsensitive()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name of the MCP server on the gateway.",
							Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
						},
						"preload": schema.BoolAttribute{
							Optional:    true,
							Description: "Load the server's tool list into the agent's context up front instead of on demand.",
						},
						"enable_tools":               toolListAttribute("Tools of this server the agent may use."),
						"disable_tools":              toolListAttribute("Tools of this server the agent must not use."),
						"preload_tools":              toolListAttribute("Tools whose definitions are loaded up front."),
						"require_approval_for_tools": toolListAttribute("Tools that require human approval before each call."),
					},
				},
			},
			"variables": schema.MapNestedAttribute{
				Optional:    true,
				Description: "Template variables usable in the instructions as {{name}}.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"default_value": schema.StringAttribute{
							Optional:    true,
							Description: "Value used when the caller does not provide one.",
						},
						"description": schema.StringAttribute{
							Optional:    true,
							Description: "What the variable means.",
						},
					},
				},
			},
			"sample_inputs": schema.ListNestedAttribute{
				Optional:    true,
				Description: "Example inputs shown in the agent playground.",
				Validators:  []validator.List{listvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"text": schema.StringAttribute{
							Optional:    true,
							Description: "Example user message.",
						},
						"variables": schema.MapAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Variable values for this example.",
						},
					},
				},
			},
			"response_format": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Constrains the agent's final response. Omitted means free-form text.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:    true,
						Description: "Response format: `text`, `json_schema` (structured output), or `json_object`.",
						Validators:  []validator.String{stringvalidator.OneOf(responseFormatTypeValues...)},
					},
					"json_schema": schema.SingleNestedAttribute{
						Optional:    true,
						Description: "JSON schema of the response (type `json_schema`).",
						Attributes: map[string]schema.Attribute{
							"name": schema.StringAttribute{
								Required:    true,
								Description: "Name of the schema.",
								Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
							},
							"description": schema.StringAttribute{
								Optional:    true,
								Description: "What the response contains.",
							},
							"schema": schema.StringAttribute{
								Optional:    true,
								CustomType:  jsontypes.NormalizedType{},
								Description: "The JSON schema as a JSON string, e.g. via jsonencode().",
							},
							"strict": schema.BoolAttribute{
								Optional:    true,
								Description: "Enforce the schema strictly.",
							},
						},
					},
				},
			},
			"config": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Runtime configuration of the agent.",
				Attributes: map[string]schema.Attribute{
					"iteration_limit": schema.Int64Attribute{
						Optional:    true,
						Description: "Maximum number of agent loop iterations per run.",
					},
					"sandbox": schema.SingleNestedAttribute{
						Optional:    true,
						Description: "Code execution sandbox for the agent.",
						Attributes: map[string]schema.Attribute{
							"enabled": schema.BoolAttribute{
								Required:    true,
								Description: "Enable the sandbox.",
							},
							"file_downloads": schema.BoolAttribute{
								Optional:    true,
								Description: "Allow downloading files produced in the sandbox.",
							},
						},
					},
					"dynamic_sub_agents": agentToggleAttribute("Let the agent spawn sub-agents dynamically."),
					"context_management": schema.SingleNestedAttribute{
						Optional:    true,
						Description: "How the agent keeps its context within limits.",
						Attributes: map[string]schema.Attribute{
							"compaction": schema.SingleNestedAttribute{
								Optional:    true,
								Description: "Conversation compaction.",
								Attributes: map[string]schema.Attribute{
									"enabled": schema.BoolAttribute{
										Required:    true,
										Description: "Enable compaction.",
									},
									"compaction_threshold_tokens": schema.Int64Attribute{
										Optional:    true,
										Description: "Token count above which the conversation is compacted.",
									},
								},
							},
							"large_tool_response": agentToggleAttribute("Offload large tool responses out of the context."),
						},
					},
					"generative_ui":      agentToggleAttribute("Let the agent render generative UI components."),
					"ask_user_questions": agentToggleAttribute("Let the agent ask the user clarifying questions."),
				},
			},
			"collaborators": collaboratorsAttribute("agent", "agent-access or agent-manager"),
			"tags":          agentTagsAttribute(),
		},
	}
}

// agentTagsAttribute is the shared tags attribute of both agent resources.
func agentTagsAttribute() schema.MapAttribute {
	return schema.MapAttribute{
		Optional:    true,
		ElementType: types.StringType,
		Description: "Free-form key-value tags on the agent.",
	}
}

func (r *agentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

// ValidateConfig enforces the response format field combinations.
func (r *agentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config agentResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || config.ResponseFormat == nil {
		return
	}

	format := config.ResponseFormat
	formatPath := path.Root("response_format")
	switch format.Type.ValueString() {
	case "json_schema":
		if format.JSONSchema == nil {
			resp.Diagnostics.AddAttributeError(formatPath.AtName("json_schema"), "Invalid agent response format",
				"`json_schema` is required for response format type \"json_schema\".")
		}
	case "text", "json_object":
		if format.JSONSchema != nil {
			resp.Diagnostics.AddAttributeError(formatPath.AtName("json_schema"), "Invalid agent response format",
				fmt.Sprintf("`json_schema` must not be set when the response format type is %q.", format.Type.ValueString()))
		}
	}
}

func (r *agentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan agentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *agentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state agentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := consumeImportMark(ctx, req, resp)

	agent, found, err := getAgentByName(ctx, r.client, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry agent failed", err.Error())
		return
	}
	if !found {
		tflog.Info(ctx, "agent not found on the control plane; removing from state",
			map[string]any{"name": state.Name.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	manifest, err := latestAgentManifest(agent)
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry agent failed", err.Error())
		return
	}
	if manifest.Type != "truefoundry-agent" {
		resp.Diagnostics.AddError("Unexpected agent type",
			fmt.Sprintf("%s is of type %q; use the matching resource (truefoundry_remote_agent for remote-agent/self-hosted).",
				agent.FQN, manifest.Type))
		return
	}

	state.ID = types.StringValue(agent.ID)
	state.FQN = types.StringValue(agent.FQN)
	state.Name = types.StringValue(manifest.Name)
	state.Description = types.StringValue(manifest.Description)
	state.Model = agentModelToModel(manifest.Model)
	state.Instructions = stringOrNull(manifest.Instructions)
	state.Skills = nil
	for _, skill := range manifest.Skills {
		state.Skills = append(state.Skills, agentSkillModel{
			FQN:     types.StringValue(skill.FQN),
			Preload: types.BoolValue(skill.Preload),
		})
	}
	state.McpServers = nil
	for _, server := range manifest.McpServers {
		state.McpServers = append(state.McpServers, agentMcpServerToModel(server))
	}
	state.Variables = nil
	if len(manifest.Variables) > 0 {
		state.Variables = map[string]agentVariableModel{}
		for name, v := range manifest.Variables {
			state.Variables[name] = agentVariableModel{
				DefaultValue: stringOrNull(v.DefaultValue),
				Description:  stringOrNull(v.Description),
			}
		}
	}
	state.SampleInputs = nil
	for _, input := range manifest.SampleInputs {
		state.SampleInputs = append(state.SampleInputs, agentSampleInputModel{
			Text:      stringOrNull(input.Text),
			Variables: stringMapToModel(input.Variables),
		})
	}
	state.ResponseFormat = agentResponseFormatToModel(manifest.ResponseFormat)
	state.Config = agentConfigToModel(manifest.Config)
	state.Collaborators = refreshCollaborators(state.Collaborators, manifest.Collaborators, imported)
	state.Tags = stringMapToModel(manifest.Tags)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *agentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan agentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *agentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state agentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteAgent(ctx, state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry agent failed", err.Error())
	}
}

// ImportState imports an agent by its name, e.g. "my-agent".
func (r *agentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
	markImported(ctx, resp)
}

// apply sends the manifest; the response's new agent version carries the
// agent ID and the versioned FQN.
func (r *agentResource) apply(ctx context.Context, plan *agentResourceModel, diags *diag.Diagnostics) {
	version, err := r.client.ApplyAgent(ctx, agentManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry agent manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied agent manifest", map[string]any{
		"fqn":     version.FQN,
		"version": version.Version,
	})
	plan.ID = types.StringValue(version.AgentID)
	plan.FQN = types.StringValue(agentFQNFromVersionFQN(version.FQN))
}

// getAgentByName fetches a single agent by name via the list endpoint.
func getAgentByName(ctx context.Context, c *client.Client, name string) (*client.Agent, bool, error) {
	agents, err := c.ListAgents(ctx, name)
	if err != nil {
		return nil, false, err
	}
	if len(agents) == 0 {
		return nil, false, nil
	}
	return &agents[0], true, nil
}

// latestAgentManifest returns the manifest of an agent's latest version.
func latestAgentManifest(agent *client.Agent) (*client.AgentManifest, error) {
	if agent.LatestVersionDetails == nil {
		return nil, fmt.Errorf("agent %s has no latest version details in the list response", agent.FQN)
	}
	return &agent.LatestVersionDetails.Manifest, nil
}

// agentFQNFromVersionFQN strips the trailing version number of an agent
// version FQN: my-tenant:agent:my-agent:3 → my-tenant:agent:my-agent.
func agentFQNFromVersionFQN(versionFQN string) string {
	if i := strings.LastIndex(versionFQN, ":"); i > 0 {
		return versionFQN[:i]
	}
	return versionFQN
}

func agentManifestFromModel(m *agentResourceModel) client.AgentManifest {
	manifest := client.AgentManifest{
		Type:          "truefoundry-agent",
		Name:          m.Name.ValueString(),
		Description:   m.Description.ValueString(),
		Instructions:  m.Instructions.ValueString(),
		Collaborators: collaboratorsToManifest(m.Collaborators),
	}
	if m.Model != nil {
		model := &client.AgentModel{Name: m.Model.Name.ValueString()}
		if p := m.Model.Params; p != nil {
			model.Params = &client.AgentModelParams{
				MaxTokens:         int64OrNil(p.MaxTokens),
				ReasoningEffort:   p.ReasoningEffort.ValueString(),
				Temperature:       float64OrNil(p.Temperature),
				TopP:              float64OrNil(p.TopP),
				TopK:              int64OrNil(p.TopK),
				ParallelToolCalls: boolOrNil(p.ParallelToolCalls),
			}
		}
		manifest.Model = model
	}
	for _, skill := range m.Skills {
		manifest.Skills = append(manifest.Skills, client.AgentSkillRef{
			FQN:     skill.FQN.ValueString(),
			Preload: skill.Preload.ValueBool(),
		})
	}
	for _, server := range m.McpServers {
		manifest.McpServers = append(manifest.McpServers, client.AgentMcpServer{
			Name:                    server.Name.ValueString(),
			Preload:                 boolOrNil(server.Preload),
			EnableTools:             stringListFromModel(server.EnableTools),
			DisableTools:            stringListFromModel(server.DisableTools),
			PreloadTools:            stringListFromModel(server.PreloadTools),
			RequireApprovalForTools: stringListFromModel(server.RequireApprovalForTools),
		})
	}
	if len(m.Variables) > 0 {
		manifest.Variables = map[string]client.AgentVariable{}
		for name, v := range m.Variables {
			manifest.Variables[name] = client.AgentVariable{
				DefaultValue: v.DefaultValue.ValueString(),
				Description:  v.Description.ValueString(),
			}
		}
	}
	for _, input := range m.SampleInputs {
		manifest.SampleInputs = append(manifest.SampleInputs, client.SampleAgentInput{
			Text:      input.Text.ValueString(),
			Variables: stringMapFromModel(input.Variables),
		})
	}
	if f := m.ResponseFormat; f != nil {
		format := &client.AgentResponseFormat{Type: f.Type.ValueString()}
		if s := f.JSONSchema; s != nil {
			format.JSONSchema = &client.AgentJSONSchema{
				Name:        s.Name.ValueString(),
				Description: s.Description.ValueString(),
				Strict:      boolOrNil(s.Strict),
			}
			if !s.Schema.IsNull() {
				format.JSONSchema.Schema = []byte(s.Schema.ValueString())
			}
		}
		manifest.ResponseFormat = format
	}
	if c := m.Config; c != nil {
		config := &client.AgentConfig{
			IterationLimit:   int64OrNil(c.IterationLimit),
			DynamicSubAgents: agentToggleFromModel(c.DynamicSubAgents),
			GenerativeUI:     agentToggleFromModel(c.GenerativeUI),
			AskUserQuestions: agentToggleFromModel(c.AskUserQuestions),
		}
		if s := c.Sandbox; s != nil {
			config.Sandbox = &client.AgentSandboxConfig{
				Enabled:       s.Enabled.ValueBool(),
				FileDownloads: boolOrNil(s.FileDownloads),
			}
		}
		if cm := c.ContextManagement; cm != nil {
			config.ContextManagement = &client.AgentContextManagement{
				LargeToolResponse: agentToggleFromModel(cm.LargeToolResponse),
			}
			if comp := cm.Compaction; comp != nil {
				config.ContextManagement.Compaction = &client.AgentCompactionConfig{
					Enabled:                   comp.Enabled.ValueBool(),
					CompactionThresholdTokens: int64OrNil(comp.CompactionThresholdTokens),
				}
			}
		}
		manifest.Config = config
	}
	manifest.Tags = stringMapFromModel(m.Tags)
	return manifest
}

func agentModelToModel(model *client.AgentModel) *agentModelModel {
	if model == nil {
		return nil
	}
	m := &agentModelModel{Name: types.StringValue(model.Name)}
	if p := model.Params; p != nil {
		m.Params = &agentModelParamsModel{
			MaxTokens:         int64OrNull(p.MaxTokens),
			ReasoningEffort:   stringOrNull(p.ReasoningEffort),
			Temperature:       float64OrNull(p.Temperature),
			TopP:              float64OrNull(p.TopP),
			TopK:              int64OrNull(p.TopK),
			ParallelToolCalls: boolOrNull(p.ParallelToolCalls),
		}
	}
	return m
}

func agentMcpServerToModel(server client.AgentMcpServer) agentMcpServerModel {
	return agentMcpServerModel{
		Name:                    types.StringValue(server.Name),
		Preload:                 boolOrNull(server.Preload),
		EnableTools:             stringListToModel(server.EnableTools),
		DisableTools:            stringListToModel(server.DisableTools),
		PreloadTools:            stringListToModel(server.PreloadTools),
		RequireApprovalForTools: stringListToModel(server.RequireApprovalForTools),
	}
}

func agentResponseFormatToModel(format *client.AgentResponseFormat) *agentResponseFormatModel {
	if format == nil {
		return nil
	}
	m := &agentResponseFormatModel{Type: types.StringValue(format.Type)}
	if s := format.JSONSchema; s != nil {
		m.JSONSchema = &agentJSONSchemaModel{
			Name:        types.StringValue(s.Name),
			Description: stringOrNull(s.Description),
			Schema:      jsontypes.NewNormalizedNull(),
			Strict:      boolOrNull(s.Strict),
		}
		if len(s.Schema) > 0 {
			m.JSONSchema.Schema = jsontypes.NewNormalizedValue(string(s.Schema))
		}
	}
	return m
}

func agentConfigToModel(config *client.AgentConfig) *agentConfigModel {
	if config == nil {
		return nil
	}
	m := &agentConfigModel{
		IterationLimit:   int64OrNull(config.IterationLimit),
		DynamicSubAgents: agentToggleToModel(config.DynamicSubAgents),
		GenerativeUI:     agentToggleToModel(config.GenerativeUI),
		AskUserQuestions: agentToggleToModel(config.AskUserQuestions),
	}
	if s := config.Sandbox; s != nil {
		m.Sandbox = &agentSandboxModel{
			Enabled:       types.BoolValue(s.Enabled),
			FileDownloads: boolOrNull(s.FileDownloads),
		}
	}
	if cm := config.ContextManagement; cm != nil {
		m.ContextManagement = &agentContextManagementModel{
			LargeToolResponse: agentToggleToModel(cm.LargeToolResponse),
		}
		if comp := cm.Compaction; comp != nil {
			m.ContextManagement.Compaction = &agentCompactionModel{
				Enabled:                   types.BoolValue(comp.Enabled),
				CompactionThresholdTokens: int64OrNull(comp.CompactionThresholdTokens),
			}
		}
	}
	return m
}

func agentToggleFromModel(m *agentToggleModel) *client.AgentToggle {
	if m == nil {
		return nil
	}
	return &client.AgentToggle{Enabled: m.Enabled.ValueBool()}
}

func agentToggleToModel(toggle *client.AgentToggle) *agentToggleModel {
	if toggle == nil {
		return nil
	}
	return &agentToggleModel{Enabled: types.BoolValue(toggle.Enabled)}
}

// stringListFromModel converts a model list to a wire list, nil when empty.
func stringListFromModel(list []types.String) []string {
	if len(list) == 0 {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		out = append(out, v.ValueString())
	}
	return out
}

// stringListToModel converts a wire list to a model list, nil when empty.
// The control plane normalizes omitted tool lists to empty arrays; mapping
// both to null keeps unset lists drift-free.
func stringListToModel(list []string) []types.String {
	if len(list) == 0 {
		return nil
	}
	out := make([]types.String, 0, len(list))
	for _, v := range list {
		out = append(out, types.StringValue(v))
	}
	return out
}

// boolOrNil maps a null or unknown bool to a nil pointer.
func boolOrNil(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	b := v.ValueBool()
	return &b
}
