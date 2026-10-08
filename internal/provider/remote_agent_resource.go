package provider

import (
	"context"
	"fmt"

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
	_ resource.Resource                   = (*remoteAgentResource)(nil)
	_ resource.ResourceWithConfigure      = (*remoteAgentResource)(nil)
	_ resource.ResourceWithValidateConfig = (*remoteAgentResource)(nil)
	_ resource.ResourceWithImportState    = (*remoteAgentResource)(nil)
)

func newRemoteAgentResource() resource.Resource {
	return &remoteAgentResource{}
}

// remoteAgentResource manages a self-hosted agent registered on the AI
// Gateway ("remote-agent/self-hosted" manifests) via the versioned
// PUT/GET/DELETE /api/svc/v1/agents endpoints.
type remoteAgentResource struct {
	client *client.Client
}

type remoteAgentModel struct {
	ID            types.String            `tfsdk:"id"`
	FQN           types.String            `tfsdk:"fqn"`
	Name          types.String            `tfsdk:"name"`
	Description   types.String            `tfsdk:"description"`
	URL           types.String            `tfsdk:"url"`
	Framework     *remoteAgentFramework   `tfsdk:"framework"`
	Auth          *remoteAgentAuthModel   `tfsdk:"auth"`
	Collaborators []collaboratorModel     `tfsdk:"collaborators"`
	Tags          map[string]types.String `tfsdk:"tags"`
}

type remoteAgentFramework struct {
	Type          types.String `tfsdk:"type"`
	AgentCardPath types.String `tfsdk:"agent_card_path"`
}

type remoteAgentAuthModel struct {
	Type    types.String            `tfsdk:"type"`
	Headers map[string]types.String `tfsdk:"headers"`
}

func (r *remoteAgentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_remote_agent"
}

func (r *remoteAgentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a self-hosted agent registered on the TrueFoundry AI Gateway " +
			"(`remote-agent/self-hosted` manifest) via `PUT /api/svc/v1/agents`. Every update creates a new " +
			"immutable agent version on the control plane. TrueFoundry-hosted agents are managed with " +
			"`truefoundry_agent`. Existing agents can be imported by their name, e.g. " +
			"`terraform import truefoundry_remote_agent.example my-agent`.",
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
			"name": agentNameAttribute("remote agent"),
			"description": schema.StringAttribute{
				Required:    true,
				Description: "Human-readable description of what the agent does.",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"url": schema.StringAttribute{
				Required:    true,
				Description: "URL of the self-hosted agent endpoint, e.g. https://agents.example.com/my-agent.",
			},
			"framework": schema.SingleNestedAttribute{
				Required:    true,
				Description: "The protocol the self-hosted agent speaks.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:    true,
						Description: "Framework: `a2a` (Agent2Agent protocol) or `custom`.",
						Validators:  []validator.String{stringvalidator.OneOf("a2a", "custom")},
					},
					"agent_card_path": schema.StringAttribute{
						Optional:    true,
						Description: "Path of the agent card relative to the URL, e.g. /.well-known/agent.json (type `a2a`).",
					},
				},
			},
			"auth": schema.SingleNestedAttribute{
				Optional: true,
				Description: "How the gateway authenticates against the agent. Omitted means no authentication. " +
					"`type` selects the mode; `headers` belongs to mode `header`.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:    true,
						Description: "Authentication mode: `header` (static headers) or `passthrough` (forward the caller's credentials).",
						Validators:  []validator.String{stringvalidator.OneOf("header", "passthrough")},
					},
					"headers": schema.MapAttribute{
						Optional:    true,
						Sensitive:   true,
						ElementType: types.StringType,
						Description: "Headers sent to the agent, e.g. {\"Authorization\" = \"Bearer ...\"} (type `header`). " +
							"Values accept TrueFoundry secret references (recommended); the control plane returns them as stored, so drift is detected.",
					},
				},
			},
			"collaborators": collaboratorsAttribute("agent", "agent-access or agent-manager"),
			"tags":          agentTagsAttribute(),
		},
	}
}

func (r *remoteAgentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the per-framework and per-auth-type field
// combinations.
func (r *remoteAgentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config remoteAgentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if framework := config.Framework; framework != nil {
		frameworkPath := path.Root("framework")
		switch framework.Type.ValueString() {
		case "a2a":
			if framework.AgentCardPath.IsNull() {
				resp.Diagnostics.AddAttributeError(frameworkPath.AtName("agent_card_path"), "Invalid remote agent framework",
					"`agent_card_path` is required for framework type \"a2a\".")
			}
		case "custom":
			if !framework.AgentCardPath.IsNull() {
				resp.Diagnostics.AddAttributeError(frameworkPath.AtName("agent_card_path"), "Invalid remote agent framework",
					"`agent_card_path` must not be set when the framework type is \"custom\".")
			}
		}
	}

	if auth := config.Auth; auth != nil {
		authPath := path.Root("auth")
		switch auth.Type.ValueString() {
		case "header":
			if len(auth.Headers) == 0 {
				resp.Diagnostics.AddAttributeError(authPath.AtName("headers"), "Invalid remote agent auth configuration",
					"`headers` is required for auth type \"header\".")
			}
		case "passthrough":
			if len(auth.Headers) > 0 {
				resp.Diagnostics.AddAttributeError(authPath.AtName("headers"), "Invalid remote agent auth configuration",
					"`headers` must not be set when auth type is \"passthrough\".")
			}
		}
	}
}

func (r *remoteAgentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan remoteAgentModel
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

func (r *remoteAgentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state remoteAgentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := consumeImportMark(ctx, req, resp)

	agent, found, err := getAgentByName(ctx, r.client, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry remote agent failed", err.Error())
		return
	}
	if !found {
		tflog.Info(ctx, "remote agent not found on the control plane; removing from state",
			map[string]any{"name": state.Name.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	manifest, err := latestAgentManifest(agent)
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry remote agent failed", err.Error())
		return
	}
	if manifest.Type != "remote-agent/self-hosted" {
		resp.Diagnostics.AddError("Unexpected agent type",
			fmt.Sprintf("%s is of type %q; use the matching resource (truefoundry_agent for truefoundry-agent).",
				agent.FQN, manifest.Type))
		return
	}

	state.ID = types.StringValue(agent.ID)
	state.FQN = types.StringValue(agent.FQN)
	state.Name = types.StringValue(manifest.Name)
	state.Description = types.StringValue(manifest.Description)
	state.URL = types.StringValue(manifest.URL)
	state.Framework = nil
	if f := manifest.Framework; f != nil {
		state.Framework = &remoteAgentFramework{
			Type:          types.StringValue(f.Type),
			AgentCardPath: stringOrNull(f.AgentCardPath),
		}
	}
	state.Auth = nil
	if auth := manifest.AuthData; auth != nil {
		state.Auth = &remoteAgentAuthModel{
			Type:    types.StringValue(auth.Type),
			Headers: stringMapToModel(auth.Headers),
		}
	}
	state.Collaborators = refreshCollaborators(state.Collaborators, manifest.Collaborators, imported)
	state.Tags = stringMapToModel(manifest.Tags)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *remoteAgentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan remoteAgentModel
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

func (r *remoteAgentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state remoteAgentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteAgent(ctx, state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry remote agent failed", err.Error())
	}
}

// ImportState imports a remote agent by its name, e.g. "my-agent".
func (r *remoteAgentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
	markImported(ctx, resp)
}

// apply sends the manifest; the response's new agent version carries the
// agent ID and the versioned FQN.
func (r *remoteAgentResource) apply(ctx context.Context, plan *remoteAgentModel, diags *diag.Diagnostics) {
	version, err := r.client.ApplyAgent(ctx, remoteAgentManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry remote agent manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied remote agent manifest", map[string]any{
		"fqn":     version.FQN,
		"version": version.Version,
	})
	plan.ID = types.StringValue(version.AgentID)
	plan.FQN = types.StringValue(agentFQNFromVersionFQN(version.FQN))
}

func remoteAgentManifestFromModel(m *remoteAgentModel) client.AgentManifest {
	manifest := client.AgentManifest{
		Type:          "remote-agent/self-hosted",
		Name:          m.Name.ValueString(),
		Description:   m.Description.ValueString(),
		URL:           m.URL.ValueString(),
		Collaborators: collaboratorsToManifest(m.Collaborators),
	}
	if f := m.Framework; f != nil {
		manifest.Framework = &client.AgentFramework{
			Type:          f.Type.ValueString(),
			AgentCardPath: f.AgentCardPath.ValueString(),
		}
	}
	if auth := m.Auth; auth != nil {
		manifest.AuthData = &client.AgentServerAuth{
			Type:    auth.Type.ValueString(),
			Headers: stringMapFromModel(auth.Headers),
		}
	}
	manifest.Tags = stringMapFromModel(m.Tags)
	return manifest
}
