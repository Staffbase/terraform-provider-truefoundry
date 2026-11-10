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
	_ resource.Resource                   = (*gatewayDataAccessConfigResource)(nil)
	_ resource.ResourceWithConfigure      = (*gatewayDataAccessConfigResource)(nil)
	_ resource.ResourceWithValidateConfig = (*gatewayDataAccessConfigResource)(nil)
	_ resource.ResourceWithImportState    = (*gatewayDataAccessConfigResource)(nil)
)

// dataAccessScopeValues are the supported data access rule scopes.
var dataAccessScopeValues = []string{"own_data", "team_data", "all_data"}

// gatewayDataTypeValues are the observability data kinds access rules cover.
var gatewayDataTypeValues = []string{"traces", "metrics"}

// dataAccessFilterFieldValues are the supported all_data filter fields.
var dataAccessFilterFieldValues = []string{"metadata", "created_by"}

func newGatewayDataAccessConfigResource() resource.Resource {
	return &gatewayDataAccessConfigResource{}
}

// gatewayDataAccessConfigResource manages the tenant-wide AI gateway data
// access config via the manifest-based apply/delete API and the dedicated
// llm-gateway/config read endpoint.
type gatewayDataAccessConfigResource struct {
	client *client.Client
}

type gatewayDataAccessConfigModel struct {
	ID    types.String          `tfsdk:"id"`
	Name  types.String          `tfsdk:"name"`
	Rules []dataAccessRuleModel `tfsdk:"rules"`
}

type dataAccessRuleModel struct {
	ID          types.String            `tfsdk:"id"`
	Scope       types.String            `tfsdk:"scope"`
	Description types.String            `tfsdk:"description"`
	Enabled     types.Bool              `tfsdk:"enabled"`
	Subjects    []types.String          `tfsdk:"subjects"`
	DataTypes   []types.String          `tfsdk:"data_types"`
	Filters     []dataAccessFilterModel `tfsdk:"filters"`
}

type dataAccessFilterModel struct {
	Field  types.String   `tfsdk:"field"`
	Key    types.String   `tfsdk:"key"`
	Values []types.String `tfsdk:"values"`
}

func (r *gatewayDataAccessConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_data_access_config"
}

func (r *gatewayDataAccessConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the AI gateway data access configuration: which subjects can see the gateway's " +
			"observability data (traces and metrics). Applied as a `gateway-data-access-config` manifest via " +
			"`PUT /api/svc/v1/apply`. **The data access config is a tenant-wide singleton** — define at most one " +
			"of this resource per control plane; an apply replaces all rules. Import the existing config with " +
			"`terraform import truefoundry_gateway_data_access_config.example gateway-data-access-config`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the gateway data access config.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the data access config.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"rules": schema.SetNestedAttribute{
				Required:    true,
				Description: "Data access rules; a subject sees data allowed by any matching rule.",
				Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Required:    true,
							Description: "Identifier of this rule, unique within the config.",
							Validators:  []validator.String{stringvalidator.LengthBetween(1, 64)},
						},
						"scope": schema.StringAttribute{
							Required: true,
							Description: fmt.Sprintf("Data the rule grants access to: `own_data` (the subject's own requests), "+
								"`team_data` (requests of the subject's teams), or `all_data`. One of: `%s`.", strings.Join(dataAccessScopeValues, "`, `")),
							Validators: []validator.String{
								stringvalidator.OneOf(dataAccessScopeValues...),
							},
						},
						"description": schema.StringAttribute{
							Optional:    true,
							Description: "Human-readable description of the rule.",
						},
						"enabled": schema.BoolAttribute{
							Optional:    true,
							Description: "Whether the rule is active.",
						},
						"subjects": schema.SetAttribute{
							Required:    true,
							ElementType: types.StringType,
							Description: "Subjects the rule applies to, as `<subject_type>:<name>` FQNs, e.g. team:everyone or role:tenant-admin.",
							Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
						},
						"data_types": schema.SetAttribute{
							Required:    true,
							ElementType: types.StringType,
							Description: fmt.Sprintf("Kinds of observability data covered. Elements are one of: `%s`.", strings.Join(gatewayDataTypeValues, "`, `")),
							Validators: []validator.Set{
								setvalidator.SizeAtLeast(1),
								setvalidator.ValueStringsAre(stringvalidator.OneOf(gatewayDataTypeValues...)),
							},
						},
						"filters": schema.SetNestedAttribute{
							Optional: true,
							Description: "Narrows an `all_data` rule to matching requests; not allowed on other scopes. " +
								"Omit the attribute (rather than an empty set) for an unfiltered rule.",
							Validators: []validator.Set{setvalidator.SizeAtLeast(1)},
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"field": schema.StringAttribute{
										Required:    true,
										Description: fmt.Sprintf("Request property to filter on. One of: `%s`.", strings.Join(dataAccessFilterFieldValues, "`, `")),
										Validators: []validator.String{
											stringvalidator.OneOf(dataAccessFilterFieldValues...),
										},
									},
									"key": schema.StringAttribute{
										Optional:    true,
										Description: "Metadata key to match (required for `metadata` filters, not allowed otherwise).",
									},
									"values": schema.SetAttribute{
										Required:    true,
										ElementType: types.StringType,
										Description: "Values to match against (IN semantics).",
										Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *gatewayDataAccessConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces unique rule IDs and the scope/filter combinations.
func (r *gatewayDataAccessConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config gatewayDataAccessConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	seen := map[string]bool{}
	for _, rule := range config.Rules {
		// rules is a set, so elements have no addressable index; errors are
		// anchored at the attribute and name the offending rule in the message.
		rulesPath := path.Root("rules")
		ruleID := rule.ID.ValueString()
		if !rule.ID.IsUnknown() && !rule.ID.IsNull() {
			if seen[ruleID] {
				resp.Diagnostics.AddAttributeError(rulesPath,
					"Invalid data access config", fmt.Sprintf("Duplicate rule id %q.", ruleID))
			}
			seen[ruleID] = true
		}
		if len(rule.Filters) > 0 && !rule.Scope.IsUnknown() && rule.Scope.ValueString() != "all_data" {
			resp.Diagnostics.AddAttributeError(rulesPath, "Invalid data access rule",
				fmt.Sprintf("Rule %q: `filters` are only allowed on all_data rules.", ruleID))
		}
		for _, f := range rule.Filters {
			if f.Field.IsUnknown() {
				continue
			}
			switch f.Field.ValueString() {
			case "metadata":
				if f.Key.IsNull() {
					resp.Diagnostics.AddAttributeError(rulesPath, "Invalid data access filter",
						fmt.Sprintf("Rule %q: `key` is required on metadata filters.", ruleID))
				}
			default:
				if !f.Key.IsNull() {
					resp.Diagnostics.AddAttributeError(rulesPath, "Invalid data access filter",
						fmt.Sprintf("Rule %q: `key` is only valid on metadata filters.", ruleID))
				}
			}
		}
	}
}

func (r *gatewayDataAccessConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayDataAccessConfigModel
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

func (r *gatewayDataAccessConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gatewayDataAccessConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.GetGatewayDataAccessConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry gateway data access config failed", err.Error())
		return
	}
	if config == nil {
		tflog.Info(ctx, "no gateway data access config on the control plane; removing from state")
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(config.ID)
	state.Name = stringOrNull(config.Manifest.Name)
	state.Rules = nil
	for _, rule := range config.Manifest.Rules {
		state.Rules = append(state.Rules, dataAccessRuleToModel(rule))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gatewayDataAccessConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan gatewayDataAccessConfigModel
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

func (r *gatewayDataAccessConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gatewayDataAccessConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, dataAccessManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry gateway data access config failed", err.Error())
	}
}

// ImportState imports the tenant's gateway data access config; the import ID
// is ignored (the config is a singleton), by convention pass
// "gateway-data-access-config".
func (r *gatewayDataAccessConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// apply sends the manifest and refreshes the computed ID from the dedicated
// read endpoint (the generic apply response carries no id/fqn for gateway
// configs).
func (r *gatewayDataAccessConfigResource) apply(ctx context.Context, plan *gatewayDataAccessConfigModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, dataAccessManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry gateway data access config manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied gateway data access config manifest", map[string]any{"action": result.Action})
	config, err := r.client.GetGatewayDataAccessConfig(ctx)
	if err != nil {
		diags.AddError("Reading back TrueFoundry gateway data access config failed", err.Error())
		return
	}
	if config == nil {
		diags.AddError("Reading back TrueFoundry gateway data access config failed", "config not found after apply")
		return
	}
	plan.ID = types.StringValue(config.ID)
}

func dataAccessManifestFromModel(m *gatewayDataAccessConfigModel) client.GatewayDataAccessConfigManifest {
	manifest := client.GatewayDataAccessConfigManifest{
		Type:  "gateway-data-access-config",
		Name:  m.Name.ValueString(),
		Rules: []client.DataAccessRule{},
	}
	for _, rule := range m.Rules {
		out := client.DataAccessRule{
			ID:          rule.ID.ValueString(),
			Scope:       rule.Scope.ValueString(),
			Description: rule.Description.ValueString(),
			Subjects:    subjectsFromModel(rule.Subjects),
			DataTypes:   subjectsFromModel(rule.DataTypes),
		}
		if !rule.Enabled.IsNull() {
			v := rule.Enabled.ValueBool()
			out.Enabled = &v
		}
		for _, f := range rule.Filters {
			out.Filters = append(out.Filters, client.DataAccessFilter{
				Field:    f.Field.ValueString(),
				Key:      f.Key.ValueString(),
				Operator: "IN",
				Value:    subjectsFromModel(f.Values),
			})
		}
		manifest.Rules = append(manifest.Rules, out)
	}
	return manifest
}

func dataAccessRuleToModel(rule client.DataAccessRule) dataAccessRuleModel {
	out := dataAccessRuleModel{
		ID:          types.StringValue(rule.ID),
		Scope:       types.StringValue(rule.Scope),
		Description: stringOrNull(rule.Description),
		Subjects:    subjectsToModel(rule.Subjects),
		DataTypes:   subjectsToModel(rule.DataTypes),
	}
	if rule.Enabled != nil {
		out.Enabled = types.BoolValue(*rule.Enabled)
	} else {
		out.Enabled = types.BoolNull()
	}
	for _, f := range rule.Filters {
		out.Filters = append(out.Filters, dataAccessFilterModel{
			Field:  types.StringValue(f.Field),
			Key:    stringOrNull(f.Key),
			Values: subjectsToModel(f.Value),
		})
	}
	return out
}
