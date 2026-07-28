package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

// Budget limiting v2 splits the single gateway-budget-config singleton into
// individually named rules, applied as "tenant-budget-config" or
// "team-budget-config" manifests. truefoundry_tenant_budget_config and
// truefoundry_team_budget_config share their whole schema except team_name,
// so the schema, conversion, and validation helpers live here.

// budgetV2ModeValues are the supported enforcement modes.
var budgetV2ModeValues = []string{"enforce", "audit"}

// budgetV2AppliesToTypeValues are the supported budget partitionings.
var budgetV2AppliesToTypeValues = []string{"aggregate", "per-user", "per-model", "per-virtual-account", "metadata"}

// budgetV2LimitPeriods are the limits attributes, in manifest field order.
var budgetV2LimitPeriods = []string{"cost_per_day", "cost_per_week", "cost_per_month", "cost_per_quarter", "cost_per_lifetime"}

// budgetV2OverrideEntityAttr maps an applies_to type to the overrides
// attribute that names its entities.
var budgetV2OverrideEntityAttr = map[string]string{
	"per-user":            "users",
	"per-model":           "models",
	"per-virtual-account": "virtual_accounts",
	"metadata":            "metadata_values",
}

type budgetV2LimitsModel struct {
	CostPerDay      types.Float64 `tfsdk:"cost_per_day"`
	CostPerWeek     types.Float64 `tfsdk:"cost_per_week"`
	CostPerMonth    types.Float64 `tfsdk:"cost_per_month"`
	CostPerQuarter  types.Float64 `tfsdk:"cost_per_quarter"`
	CostPerLifetime types.Float64 `tfsdk:"cost_per_lifetime"`
}

type budgetV2FilterModel struct {
	In    []types.String `tfsdk:"in"`
	NotIn []types.String `tfsdk:"not_in"`
}

type budgetV2SubjectsModel struct {
	Users           *budgetV2FilterModel `tfsdk:"users"`
	Teams           *budgetV2FilterModel `tfsdk:"teams"`
	VirtualAccounts *budgetV2FilterModel `tfsdk:"virtual_accounts"`
}

type budgetV2WhenModel struct {
	Subjects         *budgetV2SubjectsModel         `tfsdk:"subjects"`
	Models           *budgetV2FilterModel           `tfsdk:"models"`
	ProviderAccounts *budgetV2FilterModel           `tfsdk:"provider_accounts"`
	Metadata         map[string]budgetV2FilterModel `tfsdk:"metadata"`
}

type budgetV2OverrideModel struct {
	Users           []types.String       `tfsdk:"users"`
	Models          []types.String       `tfsdk:"models"`
	VirtualAccounts []types.String       `tfsdk:"virtual_accounts"`
	MetadataValues  []types.String       `tfsdk:"metadata_values"`
	Limits          *budgetV2LimitsModel `tfsdk:"limits"`
}

type budgetV2AppliesToModel struct {
	Type      types.String            `tfsdk:"type"`
	Metadata  types.String            `tfsdk:"metadata"`
	Overrides []budgetV2OverrideModel `tfsdk:"overrides"`
}

type budgetV2AlertsModel struct {
	Thresholds          []types.Float64           `tfsdk:"thresholds"`
	NotifyBreachingUser types.Bool                `tfsdk:"notify_breaching_user"`
	NotificationTargets []notificationTargetModel `tfsdk:"notification_targets"`
}

// budgetV2Body is the part of a budget rule both resources share.
type budgetV2Body struct {
	Name      types.String
	Mode      types.String
	Limits    *budgetV2LimitsModel
	AppliesTo *budgetV2AppliesToModel
	When      *budgetV2WhenModel
	Alerts    *budgetV2AlertsModel
}

// budgetV2Attributes returns the schema attributes shared by both budget
// resources. teamScoped drops the tenant-only subject filter documentation.
func budgetV2Attributes(teamScoped bool) map[string]schema.Attribute {
	teamsDescription := "Teams to match."
	if teamScoped {
		teamsDescription = "Teams to match. **Not valid on team budgets** — a team budget is already scoped to one team; the control plane rejects it."
	}
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:      true,
			Description:   "Control plane ID of the budget rule.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"name": schema.StringAttribute{
			Required: true,
			Description: "Name of the budget rule, unique within the tenant. Must start with a letter, end with a letter or digit, " +
				"and contain only letters, digits, and hyphens (2-62 characters). Changing it replaces the rule.",
			Validators: []validator.String{
				stringvalidator.RegexMatches(budgetV2NameRegexp, "must start with a letter, end with a letter or digit, and contain only letters, digits, and hyphens"),
			},
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"mode": schema.StringAttribute{
			Required: true,
			Description: fmt.Sprintf("Enforcement mode. One of: `%s`. `enforce` blocks requests once the budget is exhausted, "+
				"`audit` only tracks usage and still fires alerts.", strings.Join(budgetV2ModeValues, "`, `")),
			Validators: []validator.String{stringvalidator.OneOf(budgetV2ModeValues...)},
		},
		"limits": schema.SingleNestedAttribute{
			Required: true,
			Description: "Spend caps in USD. Several periods can be set at once and each is enforced independently. " +
				"Cost periods reset at 00:00 UTC (weekly on Mondays, monthly on the 1st, quarterly on Jan/Apr/Jul/Oct 1); " +
				"`cost_per_lifetime` never resets and cannot be combined with the resetting periods — use a second rule for that. " +
				"Usage counting starts when the rule is created — spend from earlier in the current period is not counted.",
			Attributes: budgetV2LimitAttributes("Maximum"),
		},
		"applies_to": schema.SingleNestedAttribute{
			Required:    true,
			Description: "How the budget is partitioned: one shared budget, or a separate budget per entity.",
			Attributes: map[string]schema.Attribute{
				"type": schema.StringAttribute{
					Required: true,
					Description: fmt.Sprintf("Partitioning. One of: `%s`. The control plane does not allow changing this after "+
						"creation, so changing it replaces the rule.", strings.Join(budgetV2AppliesToTypeValues, "`, `")),
					Validators:    []validator.String{stringvalidator.OneOf(budgetV2AppliesToTypeValues...)},
					PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				},
				"metadata": schema.StringAttribute{
					Optional:    true,
					Description: "Metadata key whose distinct values each get their own budget. Required when `type` is `metadata`, invalid otherwise.",
				},
				"overrides": schema.ListNestedAttribute{
					Optional: true,
					Description: "Per-entity limit overrides. Each entry names the entities it applies to — in the attribute matching " +
						"`type` (`users`, `models`, `virtual_accounts`, or `metadata_values`) — plus its own limits. Not valid for `aggregate`.",
					NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"users": schema.SetAttribute{
								Optional:    true,
								ElementType: types.StringType,
								Description: "Users this override applies to (`per-user` budgets only).",
							},
							"models": schema.SetAttribute{
								Optional:    true,
								ElementType: types.StringType,
								Description: "Models this override applies to (`per-model` budgets only).",
							},
							"virtual_accounts": schema.SetAttribute{
								Optional:    true,
								ElementType: types.StringType,
								Description: "Virtual accounts this override applies to (`per-virtual-account` budgets only).",
							},
							"metadata_values": schema.SetAttribute{
								Optional:    true,
								ElementType: types.StringType,
								Description: "Metadata values this override applies to (`metadata` budgets only).",
							},
							"limits": schema.SingleNestedAttribute{
								Required:    true,
								Description: "Limits used for these entities instead of the rule's limits.",
								Attributes:  budgetV2LimitAttributes("Override"),
							},
						},
					},
				},
			},
		},
		"when": schema.SingleNestedAttribute{
			Optional: true,
			Description: "Requests this rule applies to; all given filters must match. Omit it to match every request within the " +
				"rule's scope. Unlike budget limiting v1, rules are order-independent: every matching rule is evaluated and a " +
				"request is blocked if any of them is over budget.",
			Attributes: map[string]schema.Attribute{
				"subjects": schema.SingleNestedAttribute{
					Optional:    true,
					Description: "Filters on the calling subject.",
					Attributes: map[string]schema.Attribute{
						"users":            budgetV2FilterAttribute("Users to match, by email."),
						"teams":            budgetV2FilterAttribute(teamsDescription),
						"virtual_accounts": budgetV2FilterAttribute("Virtual accounts to match."),
					},
				},
				"models":            budgetV2FilterAttribute("Models to match, by model name."),
				"provider_accounts": budgetV2FilterAttribute("Provider accounts to match — the account part of a model FQN, e.g. `openai-main`."),
				"metadata": schema.MapNestedAttribute{
					Optional:     true,
					Description:  "Filters on request metadata, keyed by the metadata key from the `X-TFY-METADATA` header.",
					NestedObject: schema.NestedAttributeObject{Attributes: budgetV2FilterAttributes("metadata values")},
				},
			},
		},
		"alerts": schema.SingleNestedAttribute{
			Optional:    true,
			Description: "Alerting when usage crosses thresholds of the limit. Each threshold fires once per budget period; the checker runs every 20 minutes.",
			Attributes: map[string]schema.Attribute{
				"thresholds": schema.SetAttribute{
					Required:    true,
					ElementType: types.Float64Type,
					Description: "Usage percentages of the limit at which to alert, e.g. [75, 90, 100].",
					Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
				},
				"notify_breaching_user": schema.BoolAttribute{
					Optional:    true,
					Description: "Also notify the user who caused the breach. Only valid on `per-user` budgets.",
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
								Validators:  []validator.String{stringvalidator.OneOf(notificationTargetTypeValues...)},
							},
							"notification_channel": schema.StringAttribute{
								Required:    true,
								Description: "FQN of the notification channel integration to send through, e.g. my-tenant:slack:slack-webhooks:notification-channel:team-ai-foundation.",
							},
							"to_emails": schema.SetAttribute{
								Optional:    true,
								ElementType: types.StringType,
								Description: "Recipient addresses (email targets only). The placeholder `{{user.email}}` resolves to the breaching user and is only valid on `per-user` budgets.",
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
	}
}

// budgetV2LimitAttributes returns the five spend period attributes, with
// descriptions prefixed by lead (e.g. "Maximum" or "Override").
func budgetV2LimitAttributes(lead string) map[string]schema.Attribute {
	periods := []struct{ attr, period string }{
		{"cost_per_day", "daily"},
		{"cost_per_week", "weekly"},
		{"cost_per_month", "monthly"},
		{"cost_per_quarter", "quarterly"},
		{"cost_per_lifetime", "lifetime (never resets)"},
	}
	out := map[string]schema.Attribute{}
	for _, p := range periods {
		out[p.attr] = schema.Float64Attribute{
			Optional:    true,
			Description: fmt.Sprintf("%s %s cost in USD.", lead, p.period),
		}
	}
	return out
}

// budgetV2FilterAttributes returns the in/not_in attributes of one filter.
func budgetV2FilterAttributes(subject string) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"in": schema.SetAttribute{
			Optional:    true,
			ElementType: types.StringType,
			Description: fmt.Sprintf("Match when the request's %s are in this list.", subject),
		},
		"not_in": schema.SetAttribute{
			Optional:    true,
			ElementType: types.StringType,
			Description: fmt.Sprintf("Match when the request's %s are not in this list.", subject),
		},
	}
}

// budgetV2FilterAttribute returns one optional in/not_in filter attribute.
func budgetV2FilterAttribute(description string) schema.Attribute {
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: description + " Set `in`, `not_in`, or both.",
		Attributes:  budgetV2FilterAttributes("values"),
	}
}

// budgetV2ManifestFrom builds the manifest of a budget rule. teamName is
// empty for tenant-scoped rules.
func budgetV2ManifestFrom(manifestType, teamName string, body budgetV2Body) client.BudgetV2Manifest {
	manifest := client.BudgetV2Manifest{
		Type:      manifestType,
		Name:      body.Name.ValueString(),
		TeamName:  teamName,
		Mode:      body.Mode.ValueString(),
		Limits:    budgetV2LimitsFromModel(body.Limits),
		AppliesTo: client.BudgetV2AppliesTo{},
	}
	if body.AppliesTo != nil {
		manifest.AppliesTo.Type = body.AppliesTo.Type.ValueString()
		manifest.AppliesTo.Metadata = body.AppliesTo.Metadata.ValueString()
		for _, o := range body.AppliesTo.Overrides {
			manifest.AppliesTo.Overrides = append(manifest.AppliesTo.Overrides, client.BudgetV2Override{
				Users:           subjectsFromModel(o.Users),
				Models:          subjectsFromModel(o.Models),
				VirtualAccounts: subjectsFromModel(o.VirtualAccounts),
				MetadataValues:  subjectsFromModel(o.MetadataValues),
				Limits:          budgetV2LimitsFromModel(o.Limits),
			})
		}
	}
	if body.When != nil {
		manifest.When = client.BudgetV2When{
			Models:           budgetV2FilterFromModel(body.When.Models),
			ProviderAccounts: budgetV2FilterFromModel(body.When.ProviderAccounts),
		}
		if body.When.Subjects != nil {
			manifest.When.Subjects = &client.BudgetV2Subjects{
				Users:           budgetV2FilterFromModel(body.When.Subjects.Users),
				Teams:           budgetV2FilterFromModel(body.When.Subjects.Teams),
				VirtualAccounts: budgetV2FilterFromModel(body.When.Subjects.VirtualAccounts),
			}
		}
		if len(body.When.Metadata) > 0 {
			manifest.When.Metadata = map[string]client.BudgetV2Filter{}
			for k, v := range body.When.Metadata {
				manifest.When.Metadata[k] = *budgetV2FilterFromModel(&v)
			}
		}
	}
	if body.Alerts != nil {
		alerts := &client.BudgetV2Alerts{}
		for _, t := range body.Alerts.Thresholds {
			alerts.Thresholds = append(alerts.Thresholds, t.ValueFloat64())
		}
		if !body.Alerts.NotifyBreachingUser.IsNull() {
			v := body.Alerts.NotifyBreachingUser.ValueBool()
			alerts.NotifyBreachingUser = &v
		}
		for _, t := range body.Alerts.NotificationTargets {
			alerts.NotificationTarget = append(alerts.NotificationTarget, client.NotificationTarget{
				Type:                t.Type.ValueString(),
				NotificationChannel: t.NotificationChannel.ValueString(),
				ToEmails:            subjectsFromModel(t.ToEmails),
				Channels:            subjectsFromModel(t.Channels),
			})
		}
		manifest.Alerts = alerts
	}
	return manifest
}

// budgetV2BodyFromManifest converts a stored manifest back into the shared
// model fields. prior is the current state, used to keep the config's shape
// for values the manifest cannot distinguish (an omitted `when` block versus
// an empty one).
func budgetV2BodyFromManifest(manifest client.BudgetV2Manifest, prior budgetV2Body) budgetV2Body {
	body := budgetV2Body{
		Name:   types.StringValue(manifest.Name),
		Mode:   types.StringValue(manifest.Mode),
		Limits: budgetV2LimitsToModel(manifest.Limits),
		AppliesTo: &budgetV2AppliesToModel{
			Type:     types.StringValue(manifest.AppliesTo.Type),
			Metadata: stringOrNull(manifest.AppliesTo.Metadata),
		},
	}
	for _, o := range manifest.AppliesTo.Overrides {
		body.AppliesTo.Overrides = append(body.AppliesTo.Overrides, budgetV2OverrideModel{
			Users:           subjectsToModel(o.Users),
			Models:          subjectsToModel(o.Models),
			VirtualAccounts: subjectsToModel(o.VirtualAccounts),
			MetadataValues:  subjectsToModel(o.MetadataValues),
			Limits:          budgetV2LimitsToModel(o.Limits),
		})
	}

	when := &budgetV2WhenModel{
		Models:           budgetV2FilterToModel(manifest.When.Models),
		ProviderAccounts: budgetV2FilterToModel(manifest.When.ProviderAccounts),
	}
	if s := manifest.When.Subjects; s != nil {
		when.Subjects = &budgetV2SubjectsModel{
			Users:           budgetV2FilterToModel(s.Users),
			Teams:           budgetV2FilterToModel(s.Teams),
			VirtualAccounts: budgetV2FilterToModel(s.VirtualAccounts),
		}
	}
	if len(manifest.When.Metadata) > 0 {
		when.Metadata = map[string]budgetV2FilterModel{}
		for k, v := range manifest.When.Metadata {
			when.Metadata[k] = *budgetV2FilterToModel(&v)
		}
	}
	// An empty `when` round-trips as `{}` regardless of whether the config
	// omitted the block or wrote an empty one; keep whichever the state has.
	if budgetV2WhenEmpty(when) {
		body.When = prior.When
	} else {
		body.When = when
	}

	if a := manifest.Alerts; a != nil {
		alerts := &budgetV2AlertsModel{}
		for _, t := range a.Thresholds {
			alerts.Thresholds = append(alerts.Thresholds, types.Float64Value(t))
		}
		if a.NotifyBreachingUser != nil {
			alerts.NotifyBreachingUser = types.BoolValue(*a.NotifyBreachingUser)
		} else {
			alerts.NotifyBreachingUser = types.BoolNull()
		}
		for _, t := range a.NotificationTarget {
			alerts.NotificationTargets = append(alerts.NotificationTargets, notificationTargetModel{
				Type:                types.StringValue(t.Type),
				NotificationChannel: types.StringValue(t.NotificationChannel),
				ToEmails:            subjectsToModel(t.ToEmails),
				Channels:            subjectsToModel(t.Channels),
			})
		}
		body.Alerts = alerts
	}
	return body
}

func budgetV2LimitsFromModel(m *budgetV2LimitsModel) client.BudgetV2Limits {
	if m == nil {
		return client.BudgetV2Limits{}
	}
	return client.BudgetV2Limits{
		CostPerDay:      float64OrNil(m.CostPerDay),
		CostPerWeek:     float64OrNil(m.CostPerWeek),
		CostPerMonth:    float64OrNil(m.CostPerMonth),
		CostPerQuarter:  float64OrNil(m.CostPerQuarter),
		CostPerLifetime: float64OrNil(m.CostPerLifetime),
	}
}

func budgetV2LimitsToModel(l client.BudgetV2Limits) *budgetV2LimitsModel {
	return &budgetV2LimitsModel{
		CostPerDay:      float64PtrToModel(l.CostPerDay),
		CostPerWeek:     float64PtrToModel(l.CostPerWeek),
		CostPerMonth:    float64PtrToModel(l.CostPerMonth),
		CostPerQuarter:  float64PtrToModel(l.CostPerQuarter),
		CostPerLifetime: float64PtrToModel(l.CostPerLifetime),
	}
}

func float64PtrToModel(v *float64) types.Float64 {
	if v == nil {
		return types.Float64Null()
	}
	return types.Float64Value(*v)
}

func budgetV2FilterFromModel(m *budgetV2FilterModel) *client.BudgetV2Filter {
	if m == nil {
		return nil
	}
	return &client.BudgetV2Filter{In: subjectsFromModel(m.In), NotIn: subjectsFromModel(m.NotIn)}
}

func budgetV2FilterToModel(f *client.BudgetV2Filter) *budgetV2FilterModel {
	if f == nil {
		return nil
	}
	return &budgetV2FilterModel{In: subjectsToModel(f.In), NotIn: subjectsToModel(f.NotIn)}
}

// validateBudgetV2Limits reports limits blocks the control plane rejects: an
// empty block, or cost_per_lifetime combined with a resetting period.
func validateBudgetV2Limits(p path.Path, m *budgetV2LimitsModel, diags *diag.Diagnostics) {
	if m == nil {
		return
	}
	if budgetV2LimitsEmpty(m) {
		diags.AddAttributeError(p, "Invalid budget limits",
			fmt.Sprintf("Set at least one of %s.", strings.Join(budgetV2LimitPeriods, ", ")))
		return
	}
	if m.CostPerLifetime.IsNull() {
		return
	}
	for _, v := range []types.Float64{m.CostPerDay, m.CostPerWeek, m.CostPerMonth, m.CostPerQuarter} {
		if !v.IsNull() {
			diags.AddAttributeError(p, "Invalid budget limits",
				"`cost_per_lifetime` cannot be combined with `cost_per_day`, `cost_per_week`, `cost_per_month`, or `cost_per_quarter`. "+
					"Use a second budget rule for the resetting limit.")
			return
		}
	}
}

// validateBudgetV2Body reports config errors the control plane would reject,
// as attribute-level diagnostics. teamScoped selects the team-budget rules.
func validateBudgetV2Body(body budgetV2Body, teamScoped bool, diags *diag.Diagnostics) {
	validateBudgetV2Limits(path.Root("limits"), body.Limits, diags)

	appliesToType := ""
	if body.AppliesTo != nil {
		appliesToType = body.AppliesTo.Type.ValueString()
		metadataKeySet := !body.AppliesTo.Metadata.IsNull() && !body.AppliesTo.Metadata.IsUnknown()
		switch {
		case appliesToType == "metadata" && !metadataKeySet:
			diags.AddAttributeError(path.Root("applies_to").AtName("metadata"), "Missing metadata key",
				"`applies_to.metadata` is required when `applies_to.type` is `metadata`.")
		case appliesToType != "metadata" && metadataKeySet:
			diags.AddAttributeError(path.Root("applies_to").AtName("metadata"), "Unexpected metadata key",
				"`applies_to.metadata` is only valid when `applies_to.type` is `metadata`.")
		}

		entityAttr := budgetV2OverrideEntityAttr[appliesToType]
		for i, o := range body.AppliesTo.Overrides {
			overridePath := path.Root("applies_to").AtName("overrides").AtListIndex(i)
			if appliesToType == "aggregate" {
				diags.AddAttributeError(overridePath, "Unexpected override",
					"`applies_to.overrides` is not valid when `applies_to.type` is `aggregate`; every request shares one budget.")
				break
			}
			entities := map[string][]types.String{
				"users":            o.Users,
				"models":           o.Models,
				"virtual_accounts": o.VirtualAccounts,
				"metadata_values":  o.MetadataValues,
			}
			for attr, values := range entities {
				if attr == entityAttr || values == nil {
					continue
				}
				diags.AddAttributeError(overridePath.AtName(attr), "Unexpected override entities",
					fmt.Sprintf("`%s` is not valid for `%s` budgets; use `%s`.", attr, appliesToType, entityAttr))
			}
			if entityAttr != "" && len(entities[entityAttr]) == 0 {
				diags.AddAttributeError(overridePath.AtName(entityAttr), "Missing override entities",
					fmt.Sprintf("`%s` must list at least one entry for `%s` budgets.", entityAttr, appliesToType))
			}
			if o.Limits != nil {
				validateBudgetV2Limits(overridePath.AtName("limits"), o.Limits, diags)
			}
		}
	}

	if body.When != nil {
		filters := map[string]*budgetV2FilterModel{
			"models":            body.When.Models,
			"provider_accounts": body.When.ProviderAccounts,
		}
		for attr, f := range filters {
			validateBudgetV2Filter(path.Root("when").AtName(attr), f, diags)
		}
		if s := body.When.Subjects; s != nil {
			subjectPath := path.Root("when").AtName("subjects")
			validateBudgetV2Filter(subjectPath.AtName("users"), s.Users, diags)
			validateBudgetV2Filter(subjectPath.AtName("virtual_accounts"), s.VirtualAccounts, diags)
			validateBudgetV2Filter(subjectPath.AtName("teams"), s.Teams, diags)
			if teamScoped && s.Teams != nil {
				diags.AddAttributeError(subjectPath.AtName("teams"), "Unexpected team filter",
					"`when.subjects.teams` is not valid on a team budget; the rule is already scoped to `team_name`.")
			}
		}
		for key, f := range body.When.Metadata {
			validateBudgetV2Filter(path.Root("when").AtName("metadata").AtMapKey(key), &f, diags)
		}
	}

	if body.Alerts == nil {
		return
	}
	for _, t := range body.Alerts.Thresholds {
		if v := t.ValueFloat64(); v <= 0 || v > 100 {
			diags.AddAttributeError(path.Root("alerts").AtName("thresholds"), "Invalid alert threshold",
				fmt.Sprintf("Thresholds are percentages of the limit and must be greater than 0 and at most 100, got %g.", v))
		}
	}
	if body.Alerts.NotifyBreachingUser.ValueBool() && appliesToType != "per-user" {
		diags.AddAttributeError(path.Root("alerts").AtName("notify_breaching_user"), "Unexpected notify_breaching_user",
			"`notify_breaching_user` is only valid when `applies_to.type` is `per-user`.")
	}
	for _, t := range body.Alerts.NotificationTargets {
		targetPath := path.Root("alerts").AtName("notification_targets")
		if t.ToEmails != nil && t.Type.ValueString() != "email" {
			diags.AddAttributeError(targetPath, "Invalid notification target",
				fmt.Sprintf("Notification channel %q: `to_emails` is only valid on email targets.", t.NotificationChannel.ValueString()))
		}
		if t.Channels != nil && t.Type.ValueString() != "slack-bot" {
			diags.AddAttributeError(targetPath, "Invalid notification target",
				fmt.Sprintf("Notification channel %q: `channels` is only valid on slack-bot targets.", t.NotificationChannel.ValueString()))
		}
	}
}

// validateBudgetV2Filter rejects a filter block that sets neither list.
func validateBudgetV2Filter(p path.Path, f *budgetV2FilterModel, diags *diag.Diagnostics) {
	if f == nil || len(f.In) > 0 || len(f.NotIn) > 0 {
		return
	}
	diags.AddAttributeError(p, "Empty filter", "Set `in`, `not_in`, or both.")
}

func budgetV2LimitsEmpty(m *budgetV2LimitsModel) bool {
	for _, v := range []types.Float64{m.CostPerDay, m.CostPerWeek, m.CostPerMonth, m.CostPerQuarter, m.CostPerLifetime} {
		if !v.IsNull() {
			return false
		}
	}
	return true
}

// budgetV2WhenEmpty reports whether a when block carries no filter at all.
func budgetV2WhenEmpty(w *budgetV2WhenModel) bool {
	return w == nil || (w.Subjects == nil && w.Models == nil && w.ProviderAccounts == nil && len(w.Metadata) == 0)
}

// budgetV2Apply sends the manifest and returns the control plane ID, read
// back from the budgets list endpoint (the apply response's ID is not
// authoritative for dry runs and mirrors the list anyway).
func budgetV2Apply(ctx context.Context, c *client.Client, manifest client.BudgetV2Manifest, diags *diag.Diagnostics) types.String {
	if _, err := c.Apply(ctx, manifest, false); err != nil {
		diags.AddError("Applying TrueFoundry budget rule manifest failed", err.Error())
		return types.StringNull()
	}
	budget, err := c.GetBudget(ctx, manifest.Type, manifest.Name)
	if err != nil {
		diags.AddError("Reading back TrueFoundry budget rule failed", err.Error())
		return types.StringNull()
	}
	if budget == nil {
		diags.AddError("Reading back TrueFoundry budget rule failed", fmt.Sprintf("budget rule %q not found after apply", manifest.Name))
		return types.StringNull()
	}
	return types.StringValue(budget.ID)
}
