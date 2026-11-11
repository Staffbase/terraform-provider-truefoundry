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

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
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
	_ resource.Resource                   = (*gatewayGuardrailsConfigResource)(nil)
	_ resource.ResourceWithConfigure      = (*gatewayGuardrailsConfigResource)(nil)
	_ resource.ResourceWithValidateConfig = (*gatewayGuardrailsConfigResource)(nil)
	_ resource.ResourceWithImportState    = (*gatewayGuardrailsConfigResource)(nil)
)

// inNotInValues are the supported match conditions.
var inNotInValues = []string{"in", "not_in"}

func newGatewayGuardrailsConfigResource() resource.Resource {
	return &gatewayGuardrailsConfigResource{}
}

// gatewayGuardrailsConfigResource manages the tenant-wide AI gateway
// guardrails config via the manifest-based apply/delete API and the
// dedicated llm-gateway/config read endpoint.
type gatewayGuardrailsConfigResource struct {
	client *client.Client
}

type gatewayGuardrailsConfigModel struct {
	ID    types.String          `tfsdk:"id"`
	Name  types.String          `tfsdk:"name"`
	Rules []guardrailsRuleModel `tfsdk:"rules"`
}

type guardrailsRuleModel struct {
	ID                          types.String         `tfsdk:"id"`
	When                        *guardrailsWhenModel `tfsdk:"when"`
	LLMInputGuardrails          []types.String       `tfsdk:"llm_input_guardrails"`
	LLMOutputGuardrails         []types.String       `tfsdk:"llm_output_guardrails"`
	MCPToolPreInvokeGuardrails  []types.String       `tfsdk:"mcp_tool_pre_invoke_guardrails"`
	MCPToolPostInvokeGuardrails []types.String       `tfsdk:"mcp_tool_post_invoke_guardrails"`
	CustomErrorMessage          types.String         `tfsdk:"custom_error_message"`
}

type guardrailsWhenModel struct {
	Target   *guardrailsTargetModel   `tfsdk:"target"`
	Subjects *guardrailsSubjectsModel `tfsdk:"subjects"`
	Metadata map[string]types.String  `tfsdk:"metadata"`
}

type guardrailsTargetModel struct {
	Model      *inNotInModel  `tfsdk:"model"`
	MCPServers *inNotInModel  `tfsdk:"mcp_servers"`
	MCPTools   *mcpToolsModel `tfsdk:"mcp_tools"`
}

type inNotInModel struct {
	Condition types.String   `tfsdk:"condition"`
	Values    []types.String `tfsdk:"values"`
}

type mcpToolsModel struct {
	Condition types.String         `tfsdk:"condition"`
	Values    []mcpToolTargetModel `tfsdk:"values"`
}

type mcpToolTargetModel struct {
	Name           types.String   `tfsdk:"name"`
	EnableAllTools types.Bool     `tfsdk:"enable_all_tools"`
	EnabledTools   []types.String `tfsdk:"enabled_tools"`
}

type guardrailsSubjectsModel struct {
	Operator types.String   `tfsdk:"operator"`
	In       []types.String `tfsdk:"in"`
	NotIn    []types.String `tfsdk:"not_in"`
}

func (r *gatewayGuardrailsConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_guardrails_config"
}

// guardrailRefList is the schema of a guardrail reference list attribute.
func guardrailRefList(description string) schema.ListAttribute {
	return schema.ListAttribute{
		Optional:    true,
		ElementType: types.StringType,
		Description: description + " References are `<guardrail-group>/<guardrail-config>` names. Omit the attribute (rather than an empty list) when unused.",
		Validators:  []validator.List{listvalidator.SizeAtLeast(1)},
	}
}

// inNotInSchema is the schema of an in/not_in condition block.
func inNotInSchema(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: description,
		Attributes: map[string]schema.Attribute{
			"condition": schema.StringAttribute{
				Required:    true,
				Description: fmt.Sprintf("Match semantics. One of: `%s`.", strings.Join(inNotInValues, "`, `")),
				Validators: []validator.String{
					stringvalidator.OneOf(inNotInValues...),
				},
			},
			"values": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: "Values to match against.",
				Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
			},
		},
	}
}

func (r *gatewayGuardrailsConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		DeprecationMessage: "Use truefoundry_gateway_guardrails_policy (one named policy per rule). The folded config is removed on 15 November 2026; read-back fails on v0.179+ control planes.",
		Description: "**Deprecated:** migrate to `truefoundry_gateway_guardrails_policy` before 15 November 2026. " +
			"Control plane v0.179+ returns no folded config on read, so this resource's apply read-back fails. " +
			"See the [named guardrails migration guide](../guides/named-guardrails-policies.md).\n\n" +
			"Manages the AI gateway guardrails configuration: which guardrails (e.g. from a " +
			"`truefoundry_guardrail_group`) run on which requests. Applied as a `gateway-guardrails-config` " +
			"manifest via `PUT /api/svc/v1/apply`. **The guardrails config is a tenant-wide singleton** — define " +
			"at most one of this resource per control plane; an apply replaces all rules. Import the existing " +
			"config with `terraform import truefoundry_gateway_guardrails_config.example gateway-guardrails-config`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the gateway guardrails config.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the guardrails config.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"rules": schema.SetNestedAttribute{
				Required:    true,
				Description: "Guardrails rules. Every matching rule's guardrails run on a request; order is not significant.",
				Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: guardrailsRuleSchema(),
				},
			},
		},
	}
}

// guardrailsRuleSchema is shared by folded rules and named policies.
func guardrailsRuleSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Required:    true,
			Description: "Identifier of this rule, unique within the config.",
			Validators:  []validator.String{stringvalidator.LengthBetween(1, 64)},
		},
		"when": schema.SingleNestedAttribute{
			Required:    true,
			Description: "Requests this rule applies to; all given parts must match. An empty block matches every request.",
			Attributes: map[string]schema.Attribute{
				"target": schema.SingleNestedAttribute{
					Optional:    true,
					Description: "Matches the request target; the given conditions are OR-ed.",
					Attributes: map[string]schema.Attribute{
						"model":       inNotInSchema("Matches the model, as `<provider-account>/<model>` names."),
						"mcp_servers": inNotInSchema("Matches the MCP server."),
						"mcp_tools": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Matches MCP tools per server.",
							Attributes: map[string]schema.Attribute{
								"condition": schema.StringAttribute{
									Required:    true,
									Description: fmt.Sprintf("Match semantics. One of: `%s`.", strings.Join(inNotInValues, "`, `")),
									Validators: []validator.String{
										stringvalidator.OneOf(inNotInValues...),
									},
								},
								"values": schema.SetNestedAttribute{
									Required:    true,
									Description: "MCP servers and their covered tools.",
									Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
									NestedObject: schema.NestedAttributeObject{
										Attributes: map[string]schema.Attribute{
											"name": schema.StringAttribute{
												Required:    true,
												Description: "Name of the MCP server.",
											},
											"enable_all_tools": schema.BoolAttribute{
												Required:    true,
												Description: "Whether all tools of the server are covered.",
											},
											"enabled_tools": schema.SetAttribute{
												Optional:    true,
												ElementType: types.StringType,
												Description: "Specific tools covered when `enable_all_tools` is false. Omit rather than setting an empty set.",
												Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
											},
										},
									},
								},
							},
						},
					},
				},
				"subjects": schema.SingleNestedAttribute{
					Optional:    true,
					Description: "Matches the calling subject by FQN.",
					Attributes: map[string]schema.Attribute{
						"operator": schema.StringAttribute{
							Required:    true,
							Description: "How `in` and `not_in` combine: `and` or `or`.",
							Validators: []validator.String{
								stringvalidator.OneOf("and", "or"),
							},
						},
						"in": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Subject FQNs to match. Omit rather than setting an empty set.",
							Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
						},
						"not_in": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Subject FQNs to exclude. Omit rather than setting an empty set.",
							Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
						},
					},
				},
				"metadata": schema.MapAttribute{
					Optional:    true,
					ElementType: types.StringType,
					Description: "Request metadata key-value pairs to match. Omit rather than setting an empty map.",
					Validators:  []validator.Map{mapvalidator.SizeAtLeast(1)},
				},
			},
		},
		"llm_input_guardrails":            guardrailRefList("Guardrails run on the LLM input."),
		"llm_output_guardrails":           guardrailRefList("Guardrails run on the LLM output."),
		"mcp_tool_pre_invoke_guardrails":  guardrailRefList("Guardrails run before MCP tool invocations."),
		"mcp_tool_post_invoke_guardrails": guardrailRefList("Guardrails run after MCP tool invocations."),
		"custom_error_message": schema.StringAttribute{
			Optional:    true,
			Description: "Message returned to the caller when a guardrail blocks the request. Omit rather than setting an empty string.",
			Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
		},
	}
}

func (r *gatewayGuardrailsConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces unique rule IDs and non-empty condition blocks.
func (r *gatewayGuardrailsConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config gatewayGuardrailsConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	seen := map[string]bool{}
	for _, rule := range config.Rules {
		// rules is a set, so elements have no addressable index; errors are
		// anchored at the attribute and name the offending rule in the message.
		rulesPath := path.Root("rules")
		if !rule.ID.IsUnknown() && !rule.ID.IsNull() {
			if seen[rule.ID.ValueString()] {
				resp.Diagnostics.AddAttributeError(rulesPath,
					"Invalid guardrails config", fmt.Sprintf("Duplicate rule id %q.", rule.ID.ValueString()))
			}
			seen[rule.ID.ValueString()] = true
		}
		if rule.When == nil {
			continue
		}
		if t := rule.When.Target; t != nil && t.Model == nil && t.MCPServers == nil && t.MCPTools == nil {
			resp.Diagnostics.AddAttributeError(rulesPath, "Invalid guardrails rule",
				fmt.Sprintf("Rule %q: `target` needs at least one of `model`, `mcp_servers`, `mcp_tools`.", rule.ID.ValueString()))
		}
		if s := rule.When.Subjects; s != nil && s.In == nil && s.NotIn == nil {
			resp.Diagnostics.AddAttributeError(rulesPath, "Invalid guardrails rule",
				fmt.Sprintf("Rule %q: `subjects` needs at least one of `in`, `not_in`.", rule.ID.ValueString()))
		}
	}
}

func (r *gatewayGuardrailsConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayGuardrailsConfigModel
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

func (r *gatewayGuardrailsConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gatewayGuardrailsConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.GetGatewayGuardrailsConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry gateway guardrails config failed", err.Error())
		return
	}
	if config == nil {
		tflog.Info(ctx, "no gateway guardrails config on the control plane; removing from state")
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(config.ID)
	state.Name = stringOrNull(config.Manifest.Name)
	state.Rules = nil
	for _, rule := range config.Manifest.Rules {
		state.Rules = append(state.Rules, guardrailsRuleToModel(rule))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gatewayGuardrailsConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan gatewayGuardrailsConfigModel
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

func (r *gatewayGuardrailsConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gatewayGuardrailsConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, guardrailsConfigManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry gateway guardrails config failed", err.Error())
	}
}

// ImportState imports the tenant's gateway guardrails config; the import ID
// is ignored (the config is a singleton), by convention pass
// "gateway-guardrails-config".
func (r *gatewayGuardrailsConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// apply sends the manifest and refreshes the computed ID from the dedicated
// read endpoint (the generic apply response carries no id/fqn for gateway
// configs).
func (r *gatewayGuardrailsConfigResource) apply(ctx context.Context, plan *gatewayGuardrailsConfigModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, guardrailsConfigManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry gateway guardrails config manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied gateway guardrails config manifest", map[string]any{"action": result.Action})
	config, err := r.client.GetGatewayGuardrailsConfig(ctx)
	if err != nil {
		diags.AddError("Reading back TrueFoundry gateway guardrails config failed", err.Error())
		return
	}
	if config == nil {
		diags.AddError("Reading back TrueFoundry gateway guardrails config failed", "Config not found after apply. Control plane v0.179+ no longer reads folded guardrails configs. Migrate each rule to truefoundry_gateway_guardrails_policy before 15 November 2026.")
		return
	}
	plan.ID = types.StringValue(config.ID)
}

func guardrailsConfigManifestFromModel(m *gatewayGuardrailsConfigModel) client.GatewayGuardrailsConfigManifest {
	manifest := client.GatewayGuardrailsConfigManifest{
		Type:  "gateway-guardrails-config",
		Name:  m.Name.ValueString(),
		Rules: []client.GuardrailsRule{},
	}
	for _, rule := range m.Rules {
		manifest.Rules = append(manifest.Rules, guardrailsRuleFromModel(rule))
	}
	return manifest
}

// guardrailsRuleFromModel shares hook and condition conversion with named policies.
func guardrailsRuleFromModel(rule guardrailsRuleModel) client.GuardrailsRule {
	out := client.GuardrailsRule{
		ID: rule.ID.ValueString(),
		// The API requires the four guardrail lists; send empty lists
		// for omitted attributes.
		LLMInputGuardrails:          refsOrEmpty(rule.LLMInputGuardrails),
		LLMOutputGuardrails:         refsOrEmpty(rule.LLMOutputGuardrails),
		MCPToolPreInvokeGuardrails:  refsOrEmpty(rule.MCPToolPreInvokeGuardrails),
		MCPToolPostInvokeGuardrails: refsOrEmpty(rule.MCPToolPostInvokeGuardrails),
		CustomErrorMessage:          rule.CustomErrorMessage.ValueString(),
	}
	if w := rule.When; w != nil {
		if t := w.Target; t != nil {
			group := &client.GuardrailsTargetGroup{Operator: "or"}
			if t.Model != nil {
				group.Conditions.Model = inNotInFromModel(t.Model)
			}
			if t.MCPServers != nil {
				group.Conditions.MCPServers = inNotInFromModel(t.MCPServers)
			}
			if t.MCPTools != nil {
				tools := &client.MCPToolsCondition{Condition: t.MCPTools.Condition.ValueString()}
				for _, v := range t.MCPTools.Values {
					tools.Values = append(tools.Values, client.MCPToolTarget{
						Name:           v.Name.ValueString(),
						EnableAllTools: v.EnableAllTools.ValueBool(),
						EnabledTools:   subjectsFromModel(v.EnabledTools),
					})
				}
				group.Conditions.MCPTools = tools
			}
			out.When.Target = group
		}
		if s := w.Subjects; s != nil {
			out.When.Subjects = &client.GuardrailsSubjectGroup{
				Operator: s.Operator.ValueString(),
				Conditions: client.GuardrailsSubjectClause{
					In:    subjectsFromModel(s.In),
					NotIn: subjectsFromModel(s.NotIn),
				},
			}
		}
		if len(w.Metadata) > 0 {
			out.When.Metadata = map[string]string{}
			for k, v := range w.Metadata {
				out.When.Metadata[k] = v.ValueString()
			}
		}
	}
	return out
}

// refsOrEmpty converts a guardrail reference list, mapping a null attribute
// to the empty list the API requires.
func refsOrEmpty(refs []types.String) []string {
	out := []string{}
	for _, r := range refs {
		out = append(out, r.ValueString())
	}
	return out
}

func inNotInFromModel(m *inNotInModel) *client.InNotInCondition {
	return &client.InNotInCondition{
		Condition: m.Condition.ValueString(),
		Values:    subjectsFromModel(m.Values),
	}
}

func guardrailsRuleToModel(rule client.GuardrailsRule) guardrailsRuleModel {
	out := guardrailsRuleModel{
		ID: types.StringValue(rule.ID),
		// Empty lists map back to null: manifests always carry the four
		// lists, the schema represents unused ones as omitted.
		LLMInputGuardrails:          subjectsToModel(rule.LLMInputGuardrails),
		LLMOutputGuardrails:         subjectsToModel(rule.LLMOutputGuardrails),
		MCPToolPreInvokeGuardrails:  subjectsToModel(rule.MCPToolPreInvokeGuardrails),
		MCPToolPostInvokeGuardrails: subjectsToModel(rule.MCPToolPostInvokeGuardrails),
		CustomErrorMessage:          stringOrNull(rule.CustomErrorMessage),
		When:                        &guardrailsWhenModel{},
	}
	if t := rule.When.Target; t != nil {
		target := &guardrailsTargetModel{}
		if t.Conditions.Model != nil {
			target.Model = inNotInToModel(t.Conditions.Model)
		}
		if t.Conditions.MCPServers != nil {
			target.MCPServers = inNotInToModel(t.Conditions.MCPServers)
		}
		if t.Conditions.MCPTools != nil {
			tools := &mcpToolsModel{Condition: types.StringValue(t.Conditions.MCPTools.Condition)}
			for _, v := range t.Conditions.MCPTools.Values {
				tools.Values = append(tools.Values, mcpToolTargetModel{
					Name:           types.StringValue(v.Name),
					EnableAllTools: types.BoolValue(v.EnableAllTools),
					EnabledTools:   subjectsToModel(v.EnabledTools),
				})
			}
			target.MCPTools = tools
		}
		out.When.Target = target
	}
	if s := rule.When.Subjects; s != nil {
		out.When.Subjects = &guardrailsSubjectsModel{
			Operator: types.StringValue(s.Operator),
			In:       subjectsToModel(s.Conditions.In),
			NotIn:    subjectsToModel(s.Conditions.NotIn),
		}
	}
	if len(rule.When.Metadata) > 0 {
		out.When.Metadata = map[string]types.String{}
		for k, v := range rule.When.Metadata {
			out.When.Metadata[k] = types.StringValue(v)
		}
	}
	return out
}

func inNotInToModel(c *client.InNotInCondition) *inNotInModel {
	return &inNotInModel{
		Condition: types.StringValue(c.Condition),
		Values:    subjectsToModel(c.Values),
	}
}
