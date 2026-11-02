package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

var (
	_ resource.Resource                   = (*gatewayGuardrailsPolicyResource)(nil)
	_ resource.ResourceWithConfigure      = (*gatewayGuardrailsPolicyResource)(nil)
	_ resource.ResourceWithValidateConfig = (*gatewayGuardrailsPolicyResource)(nil)
	_ resource.ResourceWithImportState    = (*gatewayGuardrailsPolicyResource)(nil)
)

func newGatewayGuardrailsPolicyResource() resource.Resource {
	return &gatewayGuardrailsPolicyResource{}
}

type gatewayGuardrailsPolicyResource struct {
	client *client.Client
}

type gatewayGuardrailsPolicyModel struct {
	ID                          types.String         `tfsdk:"id"`
	Name                        types.String         `tfsdk:"name"`
	When                        *guardrailsWhenModel `tfsdk:"when"`
	LLMInputGuardrails          []types.String       `tfsdk:"llm_input_guardrails"`
	LLMOutputGuardrails         []types.String       `tfsdk:"llm_output_guardrails"`
	MCPToolPreInvokeGuardrails  []types.String       `tfsdk:"mcp_tool_pre_invoke_guardrails"`
	MCPToolPostInvokeGuardrails []types.String       `tfsdk:"mcp_tool_post_invoke_guardrails"`
	CustomErrorMessage          types.String         `tfsdk:"custom_error_message"`
}

func (r *gatewayGuardrailsPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_guardrails_policy"
}

func (r *gatewayGuardrailsPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := guardrailsRuleSchema()
	attrs["id"] = schema.StringAttribute{
		Computed:      true,
		Description:   "Control plane ID of the policy.",
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attrs["name"] = schema.StringAttribute{
		Required:      true,
		Description:   "Unique policy name: letters, digits, and hyphens. Changing it replaces the policy.",
		Validators:    []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9-]+$`), "must contain only letters, digits, and hyphens")},
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
	when := attrs["when"].(schema.SingleNestedAttribute)
	when.Description = "Requests this policy applies to; all given parts must match. A target with at least one model, MCP server, or MCP tool condition is required. Use `*` to match all targets."
	attrs["when"] = when
	resp.Schema = schema.Schema{
		Description: "Manages one named AI gateway guardrail policy on control plane v0.179+. Multiple policies coexist; every matching policy runs. Replaces one rule of the deprecated `truefoundry_gateway_guardrails_config`: map the rule's `id` to `name` and keep its other fields. Model targets require an LLM input or output guardrail; MCP targets require a pre- or post-invoke guardrail. Import by policy name.",
		Attributes:  attrs,
	}
}

func (r *gatewayGuardrailsPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *gatewayGuardrailsPolicyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// Unexpanded for_each/count configs can contain unknown collections.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config gatewayGuardrailsPolicyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || config.When == nil {
		return
	}
	t := config.When.Target
	if t == nil || (t.Model == nil && t.MCPServers == nil && t.MCPTools == nil) {
		resp.Diagnostics.AddAttributeError(path.Root("when").AtName("target"), "Invalid guardrails policy", "`target` is required and needs at least one of `model`, `mcp_servers`, `mcp_tools`.")
	} else {
		if t.Model != nil && len(config.LLMInputGuardrails) == 0 && len(config.LLMOutputGuardrails) == 0 {
			resp.Diagnostics.AddAttributeError(path.Root("when"), "Invalid guardrails policy", "Model targets require at least one LLM input or output guardrail.")
		}
		if (t.MCPServers != nil || t.MCPTools != nil) && len(config.MCPToolPreInvokeGuardrails) == 0 && len(config.MCPToolPostInvokeGuardrails) == 0 {
			resp.Diagnostics.AddAttributeError(path.Root("when"), "Invalid guardrails policy", "MCP targets require at least one pre- or post-invoke guardrail.")
		}
	}
	if s := config.When.Subjects; s != nil && len(s.In) == 0 && len(s.NotIn) == 0 {
		resp.Diagnostics.AddAttributeError(path.Root("when").AtName("subjects"), "Invalid guardrails policy", "`subjects` needs at least one of `in`, `not_in`.")
	}
}

func (r *gatewayGuardrailsPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayGuardrailsPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *gatewayGuardrailsPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan gatewayGuardrailsPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *gatewayGuardrailsPolicyResource) apply(ctx context.Context, plan *gatewayGuardrailsPolicyModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, guardrailsPolicyManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry guardrails policy failed", err.Error())
		return
	}
	id := result.Data.ID
	if id == "" {
		policy, err := r.client.GetGuardrailsPolicy(ctx, plan.Name.ValueString())
		if err != nil {
			diags.AddError("Reading back TrueFoundry guardrails policy failed", err.Error())
			return
		}
		if policy != nil {
			id = policy.ID
		}
	}
	if id == "" {
		diags.AddError("Reading back TrueFoundry guardrails policy failed", "Policy ID not found after apply.")
		return
	}
	plan.ID = types.StringValue(id)
}

func (r *gatewayGuardrailsPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gatewayGuardrailsPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.client.GetGuardrailsPolicy(ctx, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry guardrails policy failed", err.Error())
		return
	}
	if policy == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	m := policy.Manifest
	rule := guardrailsRuleToModel(client.GuardrailsRule{
		When:                        m.When,
		LLMInputGuardrails:          m.LLMInputGuardrails,
		LLMOutputGuardrails:         m.LLMOutputGuardrails,
		MCPToolPreInvokeGuardrails:  m.MCPToolPreInvokeGuardrails,
		MCPToolPostInvokeGuardrails: m.MCPToolPostInvokeGuardrails,
		CustomErrorMessage:          m.CustomErrorMessage,
	})
	state = gatewayGuardrailsPolicyModel{
		ID:                          types.StringValue(policy.ID),
		Name:                        types.StringValue(m.Name),
		When:                        rule.When,
		LLMInputGuardrails:          rule.LLMInputGuardrails,
		LLMOutputGuardrails:         rule.LLMOutputGuardrails,
		MCPToolPreInvokeGuardrails:  rule.MCPToolPreInvokeGuardrails,
		MCPToolPostInvokeGuardrails: rule.MCPToolPostInvokeGuardrails,
		CustomErrorMessage:          rule.CustomErrorMessage,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gatewayGuardrailsPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gatewayGuardrailsPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteGuardrailsPolicy(ctx, state.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Deleting TrueFoundry guardrails policy failed", err.Error())
	}
}

func (r *gatewayGuardrailsPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func guardrailsPolicyManifestFromModel(m *gatewayGuardrailsPolicyModel) client.GuardrailsPolicyManifest {
	rule := guardrailsRuleFromModel(guardrailsRuleModel{
		When:                        m.When,
		LLMInputGuardrails:          m.LLMInputGuardrails,
		LLMOutputGuardrails:         m.LLMOutputGuardrails,
		MCPToolPreInvokeGuardrails:  m.MCPToolPreInvokeGuardrails,
		MCPToolPostInvokeGuardrails: m.MCPToolPostInvokeGuardrails,
		CustomErrorMessage:          m.CustomErrorMessage,
	})
	return client.GuardrailsPolicyManifest{
		Type:                        "gateway-guardrails-config",
		Name:                        m.Name.ValueString(),
		When:                        rule.When,
		LLMInputGuardrails:          rule.LLMInputGuardrails,
		LLMOutputGuardrails:         rule.LLMOutputGuardrails,
		MCPToolPreInvokeGuardrails:  rule.MCPToolPreInvokeGuardrails,
		MCPToolPostInvokeGuardrails: rule.MCPToolPostInvokeGuardrails,
		CustomErrorMessage:          rule.CustomErrorMessage,
	}
}
