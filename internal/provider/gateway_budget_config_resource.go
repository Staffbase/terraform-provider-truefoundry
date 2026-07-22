package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
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
	_ resource.Resource                   = (*gatewayBudgetConfigResource)(nil)
	_ resource.ResourceWithConfigure      = (*gatewayBudgetConfigResource)(nil)
	_ resource.ResourceWithValidateConfig = (*gatewayBudgetConfigResource)(nil)
	_ resource.ResourceWithImportState    = (*gatewayBudgetConfigResource)(nil)
)

// budgetUnitValues are the supported budget limit units.
var budgetUnitValues = []string{"cost_per_day", "cost_per_week", "cost_per_month", "cost_per_quarter", "tokens_per_day", "tokens_per_month"}

// notificationTargetTypeValues are the supported budget alert receiver types.
var notificationTargetTypeValues = []string{"email", "slack-webhook", "slack-bot", "pagerduty", "ms-teams-webhook"}

func newGatewayBudgetConfigResource() resource.Resource {
	return &gatewayBudgetConfigResource{}
}

// gatewayBudgetConfigResource manages the tenant-wide AI gateway budget
// config via the manifest-based apply/delete API and the dedicated
// llm-gateway/config read endpoint.
type gatewayBudgetConfigResource struct {
	client *client.Client
}

type gatewayBudgetConfigModel struct {
	ID    types.String      `tfsdk:"id"`
	Name  types.String      `tfsdk:"name"`
	Rules []budgetRuleModel `tfsdk:"rules"`
}

type budgetRuleModel struct {
	ID               types.String      `tfsdk:"id"`
	When             budgetWhenModel   `tfsdk:"when"`
	LimitTo          types.Float64     `tfsdk:"limit_to"`
	Unit             types.String      `tfsdk:"unit"`
	BudgetAppliesPer []types.String    `tfsdk:"budget_applies_per"`
	Alerts           *budgetAlertModel `tfsdk:"alerts"`
	AuditMode        types.Bool        `tfsdk:"audit_mode"`
}

type budgetWhenModel struct {
	Subjects []types.String          `tfsdk:"subjects"`
	Models   []types.String          `tfsdk:"models"`
	Metadata map[string]types.String `tfsdk:"metadata"`
}

type budgetAlertModel struct {
	Thresholds          []types.Float64           `tfsdk:"thresholds"`
	NotificationTargets []notificationTargetModel `tfsdk:"notification_targets"`
}

type notificationTargetModel struct {
	Type                types.String   `tfsdk:"type"`
	NotificationChannel types.String   `tfsdk:"notification_channel"`
	ToEmails            []types.String `tfsdk:"to_emails"`
	Channels            []types.String `tfsdk:"channels"`
}

func (r *gatewayBudgetConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_budget_config"
}

func (r *gatewayBudgetConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the AI gateway budget configuration: spend and token limits applied to matching " +
			"requests, with optional alerting. Applied as a `gateway-budget-config` manifest via `PUT /api/svc/v1/apply`. " +
			"**The budget config is a tenant-wide singleton** — define at most one of this resource per control plane; " +
			"an apply replaces all budget rules. Import the existing config with " +
			"`terraform import truefoundry_gateway_budget_config.example gateway-budget-config`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the gateway budget config.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the budget config (3-32 characters, lowercase alphanumeric and hyphens).",
				Validators: []validator.String{
					stringvalidator.LengthBetween(3, 32),
					stringvalidator.RegexMatches(nameRegexp, "must be lowercase alphanumeric with hyphens"),
				},
			},
			"rules": schema.SetNestedAttribute{
				Required:    true,
				Description: "Budget rules. A request that matches several rules is limited by each of them; order is not significant.",
				Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Required:    true,
							Description: "Identifier of this rule, unique within the config.",
							Validators:  []validator.String{stringvalidator.LengthBetween(1, 64)},
						},
						"when": schema.SingleNestedAttribute{
							Required:    true,
							Description: "Requests this rule applies to; all given conditions must match. An empty block matches every request.",
							Attributes: map[string]schema.Attribute{
								"subjects": schema.SetAttribute{
									Optional:    true,
									ElementType: types.StringType,
									Description: "Calling subjects to match, as `<subject_type>:<name>` with subject type `user`, `virtualaccount`, or `team`.",
								},
								"models": schema.SetAttribute{
									Optional:    true,
									ElementType: types.StringType,
									Description: "Model integration FQNs to match.",
								},
								"metadata": schema.MapAttribute{
									Optional:    true,
									ElementType: types.StringType,
									Description: "Request metadata key-value pairs to match, e.g. {tfy_gateway_cluster = \"...\"}.",
								},
							},
						},
						"limit_to": schema.Float64Attribute{
							Required:    true,
							Description: "Budget limit: cost in USD for cost units, token count for token units.",
						},
						"unit": schema.StringAttribute{
							Required:    true,
							Description: fmt.Sprintf("Unit and period of the limit. One of: `%s`.", strings.Join(budgetUnitValues, "`, `")),
							Validators: []validator.String{
								stringvalidator.OneOf(budgetUnitValues...),
							},
						},
						"budget_applies_per": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Dimensions the budget is tracked per (each distinct value gets its own budget) instead of one shared budget, e.g. [\"user\"].",
						},
						"alerts": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Alerting when usage crosses thresholds of the limit.",
							Attributes: map[string]schema.Attribute{
								"thresholds": schema.SetAttribute{
									Required:    true,
									ElementType: types.Float64Type,
									Description: "Usage percentages of the limit at which to alert, e.g. [50, 90].",
									Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
								},
								"notification_targets": schema.SetNestedAttribute{
									Required:    true,
									Description: "Receivers of the alerts.",
									Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
									NestedObject: schema.NestedAttributeObject{
										Attributes: map[string]schema.Attribute{
											"type": schema.StringAttribute{
												Required:    true,
												Description: fmt.Sprintf("Receiver type. One of: `%s`.", strings.Join(notificationTargetTypeValues, "`, `")),
												Validators: []validator.String{
													stringvalidator.OneOf(notificationTargetTypeValues...),
												},
											},
											"notification_channel": schema.StringAttribute{
												Required:    true,
												Description: "FQN of the notification channel integration to send through, e.g. my-tenant:slack:slack-webhooks:notification-channel:team-ai-foundation.",
											},
											"to_emails": schema.SetAttribute{
												Optional:    true,
												ElementType: types.StringType,
												Description: "Recipient addresses (email targets only).",
											},
											"channels": schema.SetAttribute{
												Optional:    true,
												ElementType: types.StringType,
												Description: "Slack channels to post to (slack-bot targets only).",
											},
										},
									},
								},
							},
						},
						"audit_mode": schema.BoolAttribute{
							Optional:    true,
							Description: "Only record violations instead of blocking requests over the limit.",
						},
					},
				},
			},
		},
	}
}

func (r *gatewayBudgetConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces unique rule IDs and target-type field combinations.
func (r *gatewayBudgetConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config gatewayBudgetConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	seen := map[string]bool{}
	for _, rule := range config.Rules {
		// rules is a set, so elements have no addressable index; errors are
		// anchored at the attribute and name the offending rule in the message.
		rulesPath := path.Root("rules")
		if rule.ID.IsUnknown() || rule.ID.IsNull() {
			continue
		}
		if seen[rule.ID.ValueString()] {
			resp.Diagnostics.AddAttributeError(rulesPath,
				"Invalid budget config", fmt.Sprintf("Duplicate rule id %q.", rule.ID.ValueString()))
		}
		seen[rule.ID.ValueString()] = true
		if rule.Alerts == nil {
			continue
		}
		for _, t := range rule.Alerts.NotificationTargets {
			if t.ToEmails != nil && t.Type.ValueString() != "email" {
				resp.Diagnostics.AddAttributeError(rulesPath, "Invalid notification target",
					fmt.Sprintf("Rule %q, notification channel %q: `to_emails` is only valid on email targets.",
						rule.ID.ValueString(), t.NotificationChannel.ValueString()))
			}
			if t.Channels != nil && t.Type.ValueString() != "slack-bot" {
				resp.Diagnostics.AddAttributeError(rulesPath, "Invalid notification target",
					fmt.Sprintf("Rule %q, notification channel %q: `channels` is only valid on slack-bot targets.",
						rule.ID.ValueString(), t.NotificationChannel.ValueString()))
			}
		}
	}
}

func (r *gatewayBudgetConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayBudgetConfigModel
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

func (r *gatewayBudgetConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gatewayBudgetConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.GetGatewayBudgetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry gateway budget config failed", err.Error())
		return
	}
	if config == nil {
		tflog.Info(ctx, "no gateway budget config on the control plane; removing from state")
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(config.ID)
	state.Name = stringOrNull(config.Manifest.Name)
	state.Rules = nil
	for _, rule := range config.Manifest.Rules {
		state.Rules = append(state.Rules, budgetRuleToModel(rule))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gatewayBudgetConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan gatewayBudgetConfigModel
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

func (r *gatewayBudgetConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gatewayBudgetConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, budgetManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry gateway budget config failed", err.Error())
	}
}

// ImportState imports the tenant's gateway budget config; the import ID is
// ignored (the config is a singleton), by convention pass
// "gateway-budget-config".
func (r *gatewayBudgetConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// apply sends the manifest and refreshes the computed ID from the dedicated
// read endpoint (the generic apply response is not relied on for gateway
// configs).
func (r *gatewayBudgetConfigResource) apply(ctx context.Context, plan *gatewayBudgetConfigModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, budgetManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry gateway budget config manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied gateway budget config manifest", map[string]any{"action": result.Action})
	config, err := r.client.GetGatewayBudgetConfig(ctx)
	if err != nil {
		diags.AddError("Reading back TrueFoundry gateway budget config failed", err.Error())
		return
	}
	if config == nil {
		diags.AddError("Reading back TrueFoundry gateway budget config failed", "config not found after apply")
		return
	}
	plan.ID = types.StringValue(config.ID)
}

func budgetManifestFromModel(m *gatewayBudgetConfigModel) client.GatewayBudgetConfigManifest {
	manifest := client.GatewayBudgetConfigManifest{
		Type:  "gateway-budget-config",
		Name:  m.Name.ValueString(),
		Rules: []client.BudgetRule{},
	}
	for _, rule := range m.Rules {
		out := client.BudgetRule{
			ID:      rule.ID.ValueString(),
			LimitTo: rule.LimitTo.ValueFloat64(),
			Unit:    rule.Unit.ValueString(),
			When: client.BudgetWhen{
				Subjects: subjectsFromModel(rule.When.Subjects),
				Models:   subjectsFromModel(rule.When.Models),
			},
			BudgetAppliesPer: subjectsFromModel(rule.BudgetAppliesPer),
		}
		if len(rule.When.Metadata) > 0 {
			out.When.Metadata = map[string]string{}
			for k, v := range rule.When.Metadata {
				out.When.Metadata[k] = v.ValueString()
			}
		}
		if !rule.AuditMode.IsNull() {
			v := rule.AuditMode.ValueBool()
			out.AuditMode = &v
		}
		if rule.Alerts != nil {
			alert := &client.BudgetAlert{}
			for _, t := range rule.Alerts.Thresholds {
				alert.Thresholds = append(alert.Thresholds, t.ValueFloat64())
			}
			for _, t := range rule.Alerts.NotificationTargets {
				alert.NotificationTarget = append(alert.NotificationTarget, client.NotificationTarget{
					Type:                t.Type.ValueString(),
					NotificationChannel: t.NotificationChannel.ValueString(),
					ToEmails:            subjectsFromModel(t.ToEmails),
					Channels:            subjectsFromModel(t.Channels),
				})
			}
			out.Alerts = alert
		}
		manifest.Rules = append(manifest.Rules, out)
	}
	return manifest
}

func budgetRuleToModel(rule client.BudgetRule) budgetRuleModel {
	out := budgetRuleModel{
		ID:      types.StringValue(rule.ID),
		LimitTo: types.Float64Value(rule.LimitTo),
		Unit:    types.StringValue(rule.Unit),
		When: budgetWhenModel{
			Subjects: subjectsToModel(rule.When.Subjects),
			Models:   subjectsToModel(rule.When.Models),
		},
		BudgetAppliesPer: subjectsToModel(rule.BudgetAppliesPer),
	}
	if len(rule.When.Metadata) > 0 {
		out.When.Metadata = map[string]types.String{}
		for k, v := range rule.When.Metadata {
			out.When.Metadata[k] = types.StringValue(v)
		}
	}
	if rule.AuditMode != nil {
		out.AuditMode = types.BoolValue(*rule.AuditMode)
	} else {
		out.AuditMode = types.BoolNull()
	}
	if rule.Alerts != nil {
		alerts := &budgetAlertModel{}
		for _, t := range rule.Alerts.Thresholds {
			alerts.Thresholds = append(alerts.Thresholds, types.Float64Value(t))
		}
		for _, t := range rule.Alerts.NotificationTarget {
			alerts.NotificationTargets = append(alerts.NotificationTargets, notificationTargetModel{
				Type:                types.StringValue(t.Type),
				NotificationChannel: types.StringValue(t.NotificationChannel),
				ToEmails:            subjectsToModel(t.ToEmails),
				Channels:            subjectsToModel(t.Channels),
			})
		}
		out.Alerts = alerts
	}
	return out
}
