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
	_ resource.Resource                   = (*gatewayLoadBalancingConfigResource)(nil)
	_ resource.ResourceWithConfigure      = (*gatewayLoadBalancingConfigResource)(nil)
	_ resource.ResourceWithValidateConfig = (*gatewayLoadBalancingConfigResource)(nil)
	_ resource.ResourceWithImportState    = (*gatewayLoadBalancingConfigResource)(nil)
)

// loadBalancingTypeValues are the supported routing strategies. Unlike a
// virtual model's routing_config, gateway rules also support latency-based
// routing.
var loadBalancingTypeValues = []string{"weight-based-routing", "latency-based-routing", "priority-based-routing"}

// loadBalancingRuleIDRegexp is the rule id pattern the control plane
// enforces. It is stricter than the rate limiting one: no dots, underscores,
// or braces.
var loadBalancingRuleIDRegexp = regexp.MustCompile(`^[a-zA-Z](?:[a-zA-Z0-9\-]{0,60}[a-zA-Z0-9])$`)

func newGatewayLoadBalancingConfigResource() resource.Resource {
	return &gatewayLoadBalancingConfigResource{}
}

// gatewayLoadBalancingConfigResource manages the tenant-wide AI gateway load
// balancing config via the manifest-based apply/delete API and the dedicated
// llm-gateway/config read endpoint.
type gatewayLoadBalancingConfigResource struct {
	client *client.Client
}

type gatewayLoadBalancingConfigModel struct {
	ID    types.String             `tfsdk:"id"`
	Name  types.String             `tfsdk:"name"`
	Rules []loadBalancingRuleModel `tfsdk:"rules"`
}

type loadBalancingRuleModel struct {
	ID                 types.String            `tfsdk:"id"`
	Type               types.String            `tfsdk:"type"`
	When               *loadBalancingWhenModel `tfsdk:"when"`
	LoadBalanceTargets []routingTargetModel    `tfsdk:"load_balance_targets"`
}

type loadBalancingWhenModel struct {
	Subjects []types.String          `tfsdk:"subjects"`
	Models   []types.String          `tfsdk:"models"`
	Metadata map[string]types.String `tfsdk:"metadata"`
}

func (r *gatewayLoadBalancingConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_load_balancing_config"
}

func (r *gatewayLoadBalancingConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the AI gateway load balancing configuration: which target models matching requests are " +
			"spread over, and how. Applied as a `gateway-load-balancing-config` manifest via `PUT /api/svc/v1/apply`. " +
			"Unlike the `routing` block of a `virtual-model` in `truefoundry_provider_account`, these rules route " +
			"requests that address a real model, without a virtual model in front of it. **The load balancing config " +
			"is a tenant-wide singleton** — define at most one of this resource per control plane; an apply replaces " +
			"all rules. Import the existing config with " +
			"`terraform import truefoundry_gateway_load_balancing_config.example gateway-load-balancing-config`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the gateway load balancing config.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the load balancing config.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"rules": schema.SetNestedAttribute{
				Required: true,
				Description: "Load balancing rules, normally scoped so that at most one matches a given request. " +
					"This is a set: the order rules are written in carries no meaning, and which rule wins when " +
					"several match is up to the control plane.",
				Validators: []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Required: true,
							Description: "Identifier of this rule, unique within the config. 2-62 characters of letters, digits, " +
								"and hyphens; must start with a letter and end with a letter or digit.",
							Validators: []validator.String{
								stringvalidator.RegexMatches(loadBalancingRuleIDRegexp, "must match "+loadBalancingRuleIDRegexp.String()),
							},
						},
						"type": schema.StringAttribute{
							Required: true,
							Description: fmt.Sprintf("Routing strategy. One of: `%s`. `weight-based-routing` needs a `weight` on every "+
								"target, `priority-based-routing` a `priority`; `latency-based-routing` takes neither and picks the "+
								"fastest target. The control plane ignores a `weight` or `priority` its strategy does not use, so "+
								"this provider rejects that combination at plan time rather than applying a value that has no effect.",
								strings.Join(loadBalancingTypeValues, "`, `")),
							Validators: []validator.String{stringvalidator.OneOf(loadBalancingTypeValues...)},
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
						"load_balance_targets": schema.SetNestedAttribute{
							Required: true,
							Description: "Target models matching requests are routed to. The control plane rejects targets that " +
								"do not exist.",
							Validators:   []validator.Set{setvalidator.SizeAtLeast(1)},
							NestedObject: schema.NestedAttributeObject{Attributes: routingTargetAttributes()},
						},
					},
				},
			},
		},
	}
}

func (r *gatewayLoadBalancingConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces unique rule IDs, well-formed model references, and
// the per-strategy target fields. Verified against a real control plane
// (2026-07-30): it rejects a weight-based target without `weight` and a
// priority-based one without `priority`, but silently accepts — and ignores —
// a `weight` or `priority` the strategy does not use. The "must not be set"
// half therefore only exists here, so a value that would have no effect is a
// plan-time error instead.
func (r *gatewayLoadBalancingConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config gatewayLoadBalancingConfigModel
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
				resp.Diagnostics.AddAttributeError(rulesPath, "Invalid load balancing config",
					fmt.Sprintf("Duplicate rule id %q.", name))
			}
			seen[name] = true
		}
		if rule.When != nil {
			for _, model := range rule.When.Models {
				if v := model.ValueString(); !strings.Contains(v, "/") {
					resp.Diagnostics.AddAttributeError(rulesPath, "Invalid load balancing rule",
						fmt.Sprintf("Rule %q: model %q must be a `<provider-account>/<model>` name.", name, v))
				}
			}
		}
		for _, target := range rule.LoadBalanceTargets {
			validateRoutingTarget(rulesPath, rule.Type.ValueString(), name, target, &resp.Diagnostics)
		}
	}
}

// validateRoutingTarget reports weight/priority combinations the given
// routing strategy does not accept.
func validateRoutingTarget(p path.Path, strategy, ruleID string, target routingTargetModel, diags *diag.Diagnostics) {
	name := target.Target.ValueString()
	if v := name; !strings.Contains(v, "/") {
		diags.AddAttributeError(p, "Invalid load balancing target",
			fmt.Sprintf("Rule %q: target %q must be a `<provider-account>/<model>` name.", ruleID, v))
	}
	report := func(problem string) {
		diags.AddAttributeError(p, "Invalid load balancing target",
			fmt.Sprintf("Rule %q, target %q: %s", ruleID, name, problem))
	}
	switch strategy {
	case "weight-based-routing":
		if target.Weight.IsNull() {
			report("`weight` is required for weight-based-routing targets.")
		}
		if !target.Priority.IsNull() {
			report("`priority` must not be set for weight-based-routing targets.")
		}
	case "priority-based-routing":
		if target.Priority.IsNull() {
			report("`priority` is required for priority-based-routing targets.")
		}
		if !target.Weight.IsNull() {
			report("`weight` must not be set for priority-based-routing targets.")
		}
	case "latency-based-routing":
		if !target.Weight.IsNull() {
			report("`weight` must not be set for latency-based-routing targets.")
		}
		if !target.Priority.IsNull() {
			report("`priority` must not be set for latency-based-routing targets.")
		}
	}
}

func (r *gatewayLoadBalancingConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayLoadBalancingConfigModel
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

func (r *gatewayLoadBalancingConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gatewayLoadBalancingConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.GetGatewayLoadBalancingConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry gateway load balancing config failed", err.Error())
		return
	}
	if config == nil {
		tflog.Info(ctx, "no gateway load balancing config on the control plane; removing from state")
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(config.ID)
	state.Name = stringOrNull(config.Manifest.Name)
	state.Rules = nil
	for _, rule := range config.Manifest.Rules {
		state.Rules = append(state.Rules, loadBalancingRuleToModel(rule))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gatewayLoadBalancingConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan gatewayLoadBalancingConfigModel
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

func (r *gatewayLoadBalancingConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gatewayLoadBalancingConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, loadBalancingConfigManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry gateway load balancing config failed", err.Error())
	}
}

// ImportState imports the tenant's gateway load balancing config; the import
// ID is ignored (the config is a singleton), by convention pass
// "gateway-load-balancing-config".
func (r *gatewayLoadBalancingConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// apply sends the manifest and refreshes the computed ID from the dedicated
// read endpoint (the generic apply response carries no id/fqn for gateway
// configs).
func (r *gatewayLoadBalancingConfigResource) apply(ctx context.Context, plan *gatewayLoadBalancingConfigModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, loadBalancingConfigManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry gateway load balancing config manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied gateway load balancing config manifest", map[string]any{"action": result.Action})
	config, err := r.client.GetGatewayLoadBalancingConfig(ctx)
	if err != nil {
		diags.AddError("Reading back TrueFoundry gateway load balancing config failed", err.Error())
		return
	}
	if config == nil {
		diags.AddError("Reading back TrueFoundry gateway load balancing config failed", "config not found after apply")
		return
	}
	plan.ID = types.StringValue(config.ID)
}

func loadBalancingConfigManifestFromModel(m *gatewayLoadBalancingConfigModel) client.GatewayLoadBalancingConfigManifest {
	manifest := client.GatewayLoadBalancingConfigManifest{
		Type:  "gateway-load-balancing-config",
		Name:  m.Name.ValueString(),
		Rules: []client.LoadBalancingRule{},
	}
	for _, rule := range m.Rules {
		out := client.LoadBalancingRule{
			ID:                 rule.ID.ValueString(),
			Type:               rule.Type.ValueString(),
			LoadBalanceTargets: []client.RoutingTarget{},
		}
		if w := rule.When; w != nil {
			out.When = client.LoadBalancingWhen{
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
		for _, t := range rule.LoadBalanceTargets {
			out.LoadBalanceTargets = append(out.LoadBalanceTargets, routingTargetFromModel(t))
		}
		manifest.Rules = append(manifest.Rules, out)
	}
	return manifest
}

func loadBalancingRuleToModel(rule client.LoadBalancingRule) loadBalancingRuleModel {
	out := loadBalancingRuleModel{
		ID:   types.StringValue(rule.ID),
		Type: types.StringValue(rule.Type),
		// `when` is required in the schema, so an all-empty match round-trips
		// as an empty block rather than a null one.
		When: &loadBalancingWhenModel{
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
	for _, t := range rule.LoadBalanceTargets {
		out.LoadBalanceTargets = append(out.LoadBalanceTargets, routingTargetToModel(t))
	}
	return out
}
