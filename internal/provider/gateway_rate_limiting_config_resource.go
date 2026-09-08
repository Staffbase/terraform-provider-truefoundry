package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
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
	_ resource.Resource                   = (*gatewayRateLimitingConfigResource)(nil)
	_ resource.ResourceWithConfigure      = (*gatewayRateLimitingConfigResource)(nil)
	_ resource.ResourceWithValidateConfig = (*gatewayRateLimitingConfigResource)(nil)
	_ resource.ResourceWithImportState    = (*gatewayRateLimitingConfigResource)(nil)
)

// rateLimitUnitValues are the supported rate limit units.
var rateLimitUnitValues = []string{
	"requests_per_minute", "requests_per_hour", "requests_per_day",
	"tokens_per_minute", "tokens_per_hour", "tokens_per_day",
}

// rateLimitAppliesPerValues are the supported counter partitionings, besides
// the "metadata.<key>" form. Note these differ from the "per-user"-style
// values of budget limiting v2.
var rateLimitAppliesPerValues = []string{"user", "virtualaccount", "model"}

// rateLimitAppliesPerMax is the number of partitionings the control plane
// accepts per rule.
const rateLimitAppliesPerMax = 2

// rateLimitRuleIDRegexp is the rule id pattern the control plane enforces.
// The braces allow templated ids.
var rateLimitRuleIDRegexp = regexp.MustCompile(`^[A-Za-z{}][A-Za-z0-9{}._-]{0,60}[A-Za-z0-9{}._]$`)

// rateLimitMetadataAppliesPerRegexp matches the "metadata.<key>" partitioning.
var rateLimitMetadataAppliesPerRegexp = regexp.MustCompile(`^metadata\.[A-Za-z0-9_-]+$`)

func newGatewayRateLimitingConfigResource() resource.Resource {
	return &gatewayRateLimitingConfigResource{}
}

// gatewayRateLimitingConfigResource manages the tenant-wide AI gateway rate
// limiting config via the manifest-based apply/delete API and the dedicated
// llm-gateway/config read endpoint.
type gatewayRateLimitingConfigResource struct {
	client *client.Client
}

type gatewayRateLimitingConfigModel struct {
	ID    types.String         `tfsdk:"id"`
	Name  types.String         `tfsdk:"name"`
	Rules []rateLimitRuleModel `tfsdk:"rules"`
}

type rateLimitRuleModel struct {
	ID                  types.String        `tfsdk:"id"`
	When                *rateLimitWhenModel `tfsdk:"when"`
	LimitTo             types.Int64         `tfsdk:"limit_to"`
	Unit                types.String        `tfsdk:"unit"`
	RateLimitAppliesPer []types.String      `tfsdk:"rate_limit_applies_per"`
}

type rateLimitWhenModel struct {
	Subjects []types.String          `tfsdk:"subjects"`
	Models   []types.String          `tfsdk:"models"`
	Metadata map[string]types.String `tfsdk:"metadata"`
}

func (r *gatewayRateLimitingConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_rate_limiting_config"
}

func (r *gatewayRateLimitingConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the AI gateway rate limiting configuration: how many requests or tokens matching " +
			"requests may spend per period. Applied as a `gateway-rate-limiting-config` manifest via " +
			"`PUT /api/svc/v1/apply`. **The rate limiting config is a tenant-wide singleton** — define at most one " +
			"of this resource per control plane; an apply replaces all rules. Import the existing config with " +
			"`terraform import truefoundry_gateway_rate_limiting_config.example gateway-rate-limiting-config`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the gateway rate limiting config.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the rate limiting config.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"rules": schema.SetNestedAttribute{
				Required: true,
				Description: "Rate limiting rules. Every matching rule is enforced and a request is rejected if any of " +
					"them is over its limit; order is not significant.",
				Validators: []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Required: true,
							Description: "Identifier of this rule, unique within the config. 2-62 characters of letters, digits, " +
								"`.`, `_`, `-`, and `{}`; must not start with a digit or end with a hyphen.",
							Validators: []validator.String{
								stringvalidator.RegexMatches(rateLimitRuleIDRegexp, "must match "+rateLimitRuleIDRegexp.String()),
							},
						},
						"when": schema.SingleNestedAttribute{
							Required: true,
							Description: "Requests this rule applies to; all given parts must match. An empty block (`{}`) " +
								"matches every request.",
							Attributes: map[string]schema.Attribute{
								"subjects": schema.SetAttribute{
									Optional:    true,
									ElementType: types.StringType,
									Description: "Subject FQNs to match, e.g. `user:someone@example.com` or `team:everyone`. " +
										"The control plane rejects subjects that do not exist.",
									Validators: []validator.Set{setvalidator.SizeAtLeast(1)},
								},
								"models": schema.SetAttribute{
									Optional:    true,
									ElementType: types.StringType,
									Description: "Models to match, as `<provider-account>/<model>` names.",
									Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
								},
								"metadata": schema.MapAttribute{
									Optional:    true,
									ElementType: types.StringType,
									Description: "Request metadata key-value pairs to match, from the `X-TFY-METADATA` header.",
								},
							},
						},
						"limit_to": schema.Int64Attribute{
							Required:    true,
							Description: "Maximum number of requests or tokens per period, depending on `unit`.",
						},
						"unit": schema.StringAttribute{
							Required:    true,
							Description: fmt.Sprintf("What is counted, and over which period. One of: `%s`.", strings.Join(rateLimitUnitValues, "`, `")),
							Validators:  []validator.String{stringvalidator.OneOf(rateLimitUnitValues...)},
						},
						"rate_limit_applies_per": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: fmt.Sprintf("Buckets the counter per entity instead of sharing one counter across all matching "+
								"requests. One of: `%s`, or `metadata.<key>` (e.g. `metadata.appId`). At most %d values. "+
								"Note these differ from the `per-user`-style values of budget limiting v2.",
								strings.Join(rateLimitAppliesPerValues, "`, `"), rateLimitAppliesPerMax),
							Validators: []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.SizeAtMost(rateLimitAppliesPerMax)},
						},
					},
				},
			},
		},
	}
}

func (r *gatewayRateLimitingConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces unique rule IDs, positive limits, well-formed model
// references, and the applies-per vocabulary. The control plane accepts a
// non-positive limit_to and silently ignores unknown `when` fields, so these
// checks only exist here.
func (r *gatewayRateLimitingConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config gatewayRateLimitingConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// rules is a set, so elements have no addressable index; errors are
	// anchored at the attribute and name the offending rule in the message.
	rulesPath := path.Root("rules")
	seen := map[string]bool{}
	for _, rule := range config.Rules {
		name := rule.ID.ValueString()
		if !rule.ID.IsUnknown() && !rule.ID.IsNull() {
			if seen[name] {
				resp.Diagnostics.AddAttributeError(rulesPath, "Invalid rate limiting config",
					fmt.Sprintf("Duplicate rule id %q.", name))
			}
			seen[name] = true
		}
		if !rule.LimitTo.IsNull() && !rule.LimitTo.IsUnknown() && rule.LimitTo.ValueInt64() <= 0 {
			resp.Diagnostics.AddAttributeError(rulesPath, "Invalid rate limiting rule",
				fmt.Sprintf("Rule %q: `limit_to` must be greater than 0.", name))
		}
		if rule.When != nil {
			for _, model := range rule.When.Models {
				if v := model.ValueString(); !strings.Contains(v, "/") {
					resp.Diagnostics.AddAttributeError(rulesPath, "Invalid rate limiting rule",
						fmt.Sprintf("Rule %q: model %q must be a `<provider-account>/<model>` name.", name, v))
				}
			}
		}
		for _, per := range rule.RateLimitAppliesPer {
			v := per.ValueString()
			if rateLimitMetadataAppliesPerRegexp.MatchString(v) {
				continue
			}
			if !slices.Contains(rateLimitAppliesPerValues, v) {
				resp.Diagnostics.AddAttributeError(rulesPath, "Invalid rate limiting rule",
					fmt.Sprintf("Rule %q: `rate_limit_applies_per` value %q must be one of %s, or `metadata.<key>`.",
						name, v, "`"+strings.Join(rateLimitAppliesPerValues, "`, `")+"`"))
			}
		}
	}
}

func (r *gatewayRateLimitingConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayRateLimitingConfigModel
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

func (r *gatewayRateLimitingConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gatewayRateLimitingConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.GetGatewayRateLimitingConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry gateway rate limiting config failed", err.Error())
		return
	}
	if config == nil {
		tflog.Info(ctx, "no gateway rate limiting config on the control plane; removing from state")
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(config.ID)
	state.Name = stringOrNull(config.Manifest.Name)
	state.Rules = nil
	for _, rule := range config.Manifest.Rules {
		state.Rules = append(state.Rules, rateLimitRuleToModel(rule))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gatewayRateLimitingConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan gatewayRateLimitingConfigModel
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

func (r *gatewayRateLimitingConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gatewayRateLimitingConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, rateLimitingConfigManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry gateway rate limiting config failed", err.Error())
	}
}

// ImportState imports the tenant's gateway rate limiting config; the import ID
// is ignored (the config is a singleton), by convention pass
// "gateway-rate-limiting-config".
func (r *gatewayRateLimitingConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// apply sends the manifest and refreshes the computed ID from the dedicated
// read endpoint (the generic apply response carries no id/fqn for gateway
// configs).
func (r *gatewayRateLimitingConfigResource) apply(ctx context.Context, plan *gatewayRateLimitingConfigModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, rateLimitingConfigManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry gateway rate limiting config manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied gateway rate limiting config manifest", map[string]any{"action": result.Action})
	config, err := r.client.GetGatewayRateLimitingConfig(ctx)
	if err != nil {
		diags.AddError("Reading back TrueFoundry gateway rate limiting config failed", err.Error())
		return
	}
	if config == nil {
		diags.AddError("Reading back TrueFoundry gateway rate limiting config failed", "config not found after apply")
		return
	}
	plan.ID = types.StringValue(config.ID)
}

func rateLimitingConfigManifestFromModel(m *gatewayRateLimitingConfigModel) client.GatewayRateLimitingConfigManifest {
	manifest := client.GatewayRateLimitingConfigManifest{
		Type:  "gateway-rate-limiting-config",
		Name:  m.Name.ValueString(),
		Rules: []client.RateLimitRule{},
	}
	for _, rule := range m.Rules {
		out := client.RateLimitRule{
			ID:                  rule.ID.ValueString(),
			LimitTo:             rule.LimitTo.ValueInt64(),
			Unit:                rule.Unit.ValueString(),
			RateLimitAppliesPer: subjectsFromModel(rule.RateLimitAppliesPer),
		}
		if w := rule.When; w != nil {
			out.When = client.RateLimitWhen{
				Subjects: subjectsFromModel(w.Subjects),
				Models:   subjectsFromModel(w.Models),
			}
			if len(w.Metadata) > 0 {
				out.When.Metadata = map[string]string{}
				for k, v := range w.Metadata {
					out.When.Metadata[k] = v.ValueString()
				}
			}
		}
		manifest.Rules = append(manifest.Rules, out)
	}
	return manifest
}

func rateLimitRuleToModel(rule client.RateLimitRule) rateLimitRuleModel {
	out := rateLimitRuleModel{
		ID:                  types.StringValue(rule.ID),
		LimitTo:             types.Int64Value(rule.LimitTo),
		Unit:                types.StringValue(rule.Unit),
		RateLimitAppliesPer: subjectsToModel(rule.RateLimitAppliesPer),
		// `when` is required in the schema, so an all-empty match round-trips
		// as an empty block rather than a null one.
		When: &rateLimitWhenModel{
			Subjects: subjectsToModel(rule.When.Subjects),
			Models:   subjectsToModel(rule.When.Models),
		},
	}
	if len(rule.When.Metadata) > 0 {
		out.When.Metadata = map[string]types.String{}
		for k, v := range rule.When.Metadata {
			out.When.Metadata[k] = types.StringValue(v)
		}
	}
	return out
}
