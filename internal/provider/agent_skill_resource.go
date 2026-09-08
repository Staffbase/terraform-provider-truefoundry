package provider

import (
	"context"
	"fmt"
	"strings"

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
	_ resource.Resource                   = (*agentSkillResource)(nil)
	_ resource.ResourceWithConfigure      = (*agentSkillResource)(nil)
	_ resource.ResourceWithValidateConfig = (*agentSkillResource)(nil)
	_ resource.ResourceWithImportState    = (*agentSkillResource)(nil)
)

// skillSourceTypeValues are the supported agent skill source types. The
// "local" type is CLI-only and not supported here.
var skillSourceTypeValues = []string{"inline", "blob-storage"}

func newAgentSkillResource() resource.Resource {
	return &agentSkillResource{}
}

// agentSkillResource manages an agent skill ("agent-skill" manifests) via
// the versioned PUT /api/svc/v1/agent-skill-versions and GET/DELETE
// /api/svc/v1/agent-skills endpoints.
type agentSkillResource struct {
	client *client.Client
}

type agentSkillResourceModel struct {
	ID     types.String           `tfsdk:"id"`
	FQN    types.String           `tfsdk:"fqn"`
	Name   types.String           `tfsdk:"name"`
	MlRepo types.String           `tfsdk:"ml_repo"`
	Source *agentSkillSourceModel `tfsdk:"source"`
}

type agentSkillSourceModel struct {
	Type        types.String `tfsdk:"type"`
	SkillMd     types.String `tfsdk:"skill_md"`
	URI         types.String `tfsdk:"uri"`
	Description types.String `tfsdk:"description"`
}

func (r *agentSkillResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent_skill"
}

func (r *agentSkillResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an agent skill (`agent-skill` manifest) via `PUT /api/svc/v1/agent-skill-versions` — " +
			"a versioned artifact in an ML repo (`truefoundry_ml_repo`) that agents (`truefoundry_agent`) reference " +
			"by FQN. Every update creates a new immutable skill version on the control plane. The control plane " +
			"ingests the applied source into TrueFoundry-managed blob storage, so the configured source cannot be " +
			"read back: source changes made outside Terraform are not detected, and after `terraform import` (by " +
			"name, e.g. `terraform import truefoundry_agent_skill.example my-skill`) the first apply uploads the " +
			"configured source as a new version.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the agent skill.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed: true,
				Description: "Fully qualified name of the agent skill, e.g. agent-skill:my-tenant/my-repo/my-skill. " +
					"Agents reference a specific version by appending its number, e.g. `\"${truefoundry_agent_skill.example.fqn}:1\"`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the agent skill (lowercase alphanumeric and hyphens). Changing it replaces the skill.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.RegexMatches(nameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ml_repo": schema.StringAttribute{
				Required:      true,
				Description:   "Name of the ML repo the skill lives in. It must exist on the tenant. Changing it replaces the skill.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"source": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Where the skill's content comes from. `type` selects the variant; the other fields belong to one variant each.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:    true,
						Description: "Source variant: `inline` (skill_md) or `blob-storage` (uri and description).",
						Validators:  []validator.String{stringvalidator.OneOf(skillSourceTypeValues...)},
					},
					"skill_md": schema.StringAttribute{
						Optional: true,
						Description: "The SKILL.md document (type `inline`). Must start with YAML frontmatter whose " +
							"`name` field matches the skill name, e.g. via file() or a heredoc.",
					},
					"uri": schema.StringAttribute{
						Optional:    true,
						Description: "Blob storage URI the skill directory is ingested from (type `blob-storage`).",
					},
					"description": schema.StringAttribute{
						Optional:    true,
						Description: "What the skill does, shown to agents (type `blob-storage`; inline sources carry it in the frontmatter).",
					},
				},
			},
		},
	}
}

func (r *agentSkillResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the per-source-type field combinations and the
// SKILL.md frontmatter rules the control plane checks at apply time.
func (r *agentSkillResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config agentSkillResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || config.Source == nil {
		return
	}

	source := config.Source
	sourcePath := path.Root("source")
	switch source.Type.ValueString() {
	case "inline":
		for name, set := range map[string]bool{"uri": !source.URI.IsNull(), "description": !source.Description.IsNull()} {
			if set {
				resp.Diagnostics.AddAttributeError(sourcePath.AtName(name), "Invalid agent skill source",
					fmt.Sprintf("`%s` must not be set when the source type is \"inline\".", name))
			}
		}
		if source.SkillMd.IsNull() {
			resp.Diagnostics.AddAttributeError(sourcePath.AtName("skill_md"), "Invalid agent skill source",
				"`skill_md` is required for source type \"inline\".")
			return
		}
		frontmatterName, ok := skillFrontmatterName(source.SkillMd.ValueString())
		if !ok {
			resp.Diagnostics.AddAttributeError(sourcePath.AtName("skill_md"), "Invalid agent skill source",
				"`skill_md` must start with YAML frontmatter (a line `---`, then fields, then a closing `---`).")
			return
		}
		if !config.Name.IsNull() && frontmatterName != config.Name.ValueString() {
			resp.Diagnostics.AddAttributeError(sourcePath.AtName("skill_md"), "Invalid agent skill source",
				fmt.Sprintf("The SKILL.md frontmatter name %q must match the skill name %q.", frontmatterName, config.Name.ValueString()))
		}
	case "blob-storage":
		if !source.SkillMd.IsNull() {
			resp.Diagnostics.AddAttributeError(sourcePath.AtName("skill_md"), "Invalid agent skill source",
				"`skill_md` must not be set when the source type is \"blob-storage\".")
		}
		for name, set := range map[string]bool{"uri": !source.URI.IsNull(), "description": !source.Description.IsNull()} {
			if !set {
				resp.Diagnostics.AddAttributeError(sourcePath.AtName(name), "Invalid agent skill source",
					fmt.Sprintf("`%s` is required for source type \"blob-storage\".", name))
			}
		}
	}
}

// skillFrontmatterName extracts the name field of a SKILL.md YAML
// frontmatter block. ok is false when the document does not start with one.
func skillFrontmatterName(md string) (name string, ok bool) {
	rest, found := strings.CutPrefix(md, "---\n")
	if !found {
		return "", false
	}
	block, _, found := strings.Cut(rest, "\n---")
	if !found {
		return "", false
	}
	for line := range strings.SplitSeq(block, "\n") {
		if v, found := strings.CutPrefix(line, "name:"); found {
			name = strings.TrimSpace(v)
		}
	}
	return name, true
}

func (r *agentSkillResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan agentSkillResourceModel
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

func (r *agentSkillResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state agentSkillResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	skill, found, err := getAgentSkillByName(ctx, r.client, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry agent skill failed", err.Error())
		return
	}
	if !found {
		tflog.Info(ctx, "agent skill not found on the control plane; removing from state",
			map[string]any{"name": state.Name.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if skill.LatestVersion == nil {
		resp.Diagnostics.AddError("Reading TrueFoundry agent skill failed",
			fmt.Sprintf("agent skill %s has no latest version details in the list response", skill.FQN))
		return
	}
	manifest := skill.LatestVersion.Manifest

	state.ID = types.StringValue(skill.ID)
	state.FQN = types.StringValue(skill.FQN)
	state.Name = types.StringValue(manifest.Name)
	state.MlRepo = types.StringValue(manifest.MlRepo)
	// The control plane stores every applied source as a blob-storage source
	// with a TrueFoundry-managed URI, so the configured source never round-
	// trips. Keep the source as configured; only populate it from the stored
	// manifest when the state has none yet (import).
	if state.Source == nil && manifest.Source != nil {
		state.Source = &agentSkillSourceModel{
			Type:        types.StringValue(manifest.Source.Type),
			SkillMd:     stringOrNull(manifest.Source.SkillMd),
			URI:         stringOrNull(manifest.Source.URI),
			Description: stringOrNull(manifest.Source.Description),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *agentSkillResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan agentSkillResourceModel
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

func (r *agentSkillResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state agentSkillResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteAgentSkill(ctx, state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry agent skill failed", err.Error())
	}
}

// ImportState imports an agent skill by its name, e.g. "my-skill".
func (r *agentSkillResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// apply sends the manifest; the response's new skill version carries the
// skill ID and the versioned FQN.
func (r *agentSkillResource) apply(ctx context.Context, plan *agentSkillResourceModel, diags *diag.Diagnostics) {
	version, err := r.client.ApplyAgentSkill(ctx, agentSkillManifestFromModel(plan))
	if err != nil {
		diags.AddError("Applying TrueFoundry agent skill manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied agent skill manifest", map[string]any{"fqn": version.FQN})
	plan.ID = types.StringValue(version.AgentSkillID)
	plan.FQN = types.StringValue(agentFQNFromVersionFQN(version.FQN))
}

// getAgentSkillByName fetches a single agent skill by name via the list endpoint.
func getAgentSkillByName(ctx context.Context, c *client.Client, name string) (*client.AgentSkill, bool, error) {
	skills, err := c.ListAgentSkills(ctx, name)
	if err != nil {
		return nil, false, err
	}
	if len(skills) == 0 {
		return nil, false, nil
	}
	return &skills[0], true, nil
}

func agentSkillManifestFromModel(m *agentSkillResourceModel) client.AgentSkillManifest {
	manifest := client.AgentSkillManifest{
		Type:     "agent-skill",
		Name:     m.Name.ValueString(),
		MlRepo:   m.MlRepo.ValueString(),
		Metadata: map[string]any{},
	}
	if s := m.Source; s != nil {
		manifest.Source = &client.AgentSkillSource{
			Type:        s.Type.ValueString(),
			SkillMd:     s.SkillMd.ValueString(),
			URI:         s.URI.ValueString(),
			Description: s.Description.ValueString(),
		}
	}
	return manifest
}
