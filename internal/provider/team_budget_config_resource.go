package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	_ resource.Resource                   = (*teamBudgetConfigResource)(nil)
	_ resource.ResourceWithConfigure      = (*teamBudgetConfigResource)(nil)
	_ resource.ResourceWithValidateConfig = (*teamBudgetConfigResource)(nil)
	_ resource.ResourceWithImportState    = (*teamBudgetConfigResource)(nil)
)

// teamBudgetConfigManifestType is the manifest type of a team-scoped budget
// limiting v2 rule.
const teamBudgetConfigManifestType = "team-budget-config"

func newTeamBudgetConfigResource() resource.Resource {
	return &teamBudgetConfigResource{}
}

// teamBudgetConfigResource manages one team-scoped budget limiting v2 rule.
type teamBudgetConfigResource struct {
	client *client.Client
}

type teamBudgetConfigModel struct {
	ID        types.String            `tfsdk:"id"`
	Name      types.String            `tfsdk:"name"`
	TeamName  types.String            `tfsdk:"team_name"`
	Mode      types.String            `tfsdk:"mode"`
	Limits    *budgetV2LimitsModel    `tfsdk:"limits"`
	AppliesTo *budgetV2AppliesToModel `tfsdk:"applies_to"`
	When      *budgetV2WhenModel      `tfsdk:"when"`
	Alerts    *budgetV2AlertsModel    `tfsdk:"alerts"`
}

func (m *teamBudgetConfigModel) body() budgetV2Body {
	return budgetV2Body{Name: m.Name, Mode: m.Mode, Limits: m.Limits, AppliesTo: m.AppliesTo, When: m.When, Alerts: m.Alerts}
}

func (m *teamBudgetConfigModel) setBody(body budgetV2Body) {
	m.Name, m.Mode, m.Limits, m.AppliesTo, m.When, m.Alerts = body.Name, body.Mode, body.Limits, body.AppliesTo, body.When, body.Alerts
}

func (r *teamBudgetConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team_budget_config"
}

func (r *teamBudgetConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := budgetV2Attributes(true)
	attributes["team_name"] = schema.StringAttribute{
		Required: true,
		Description: "Name of the team that owns this budget. The control plane rejects unknown teams with `Invalid team id` and " +
			"enforces RBAC on this value: team managers and tenant admins may manage the rule. Changing it replaces the rule.",
		Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
	resp.Schema = schema.Schema{
		Description: "Manages one team-scoped budget rule of AI gateway **budget limiting v2**: a spend cap on matching requests of " +
			"one team, with optional alerting. Applied as a `team-budget-config` manifest via `PUT /api/svc/v1/apply`. " +
			"Rules are evaluated independently of each other and of tenant budgets — a request is blocked when any matching rule " +
			"in `enforce` mode is over budget. " +
			"Import an existing rule with `terraform import truefoundry_team_budget_config.example <name>`.",
		Attributes: attributes,
	}
}

func (r *teamBudgetConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *teamBudgetConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// See tenantBudgetConfigResource.ValidateConfig for why unknown configs
	// are skipped.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config teamBudgetConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateBudgetV2Body(config.body(), true, &resp.Diagnostics)
}

func (r *teamBudgetConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan teamBudgetConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = budgetV2Apply(ctx, r.client, r.manifest(&plan), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *teamBudgetConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state teamBudgetConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	budget, err := r.client.GetBudget(ctx, teamBudgetConfigManifestType, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry team budget rule failed", err.Error())
		return
	}
	if budget == nil {
		tflog.Info(ctx, "team budget rule not found on the control plane; removing from state", map[string]any{"name": state.Name.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(budget.ID)
	state.TeamName = types.StringValue(budget.Manifest.TeamName)
	state.setBody(budgetV2BodyFromManifest(budget.Manifest, state.body()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *teamBudgetConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan teamBudgetConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = budgetV2Apply(ctx, r.client, r.manifest(&plan), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *teamBudgetConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state teamBudgetConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, r.manifest(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry team budget rule failed", err.Error())
	}
}

// ImportState imports a team budget rule by its name; team_name comes from
// the stored manifest.
func (r *teamBudgetConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *teamBudgetConfigResource) manifest(m *teamBudgetConfigModel) client.BudgetV2Manifest {
	return budgetV2ManifestFrom(teamBudgetConfigManifestType, m.TeamName.ValueString(), m.body())
}
