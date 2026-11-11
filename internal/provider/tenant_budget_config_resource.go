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

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

var (
	_ resource.Resource                   = (*tenantBudgetConfigResource)(nil)
	_ resource.ResourceWithConfigure      = (*tenantBudgetConfigResource)(nil)
	_ resource.ResourceWithValidateConfig = (*tenantBudgetConfigResource)(nil)
	_ resource.ResourceWithImportState    = (*tenantBudgetConfigResource)(nil)
)

// budgetV2NameRegexp is the rule name pattern the control plane enforces —
// note that, unlike other manifest names, upper case is allowed.
var budgetV2NameRegexp = regexp.MustCompile(`^[a-zA-Z](?:[a-zA-Z0-9\-]{0,60}[a-zA-Z0-9])$`)

// tenantBudgetConfigManifestType is the manifest type of a tenant-scoped
// budget limiting v2 rule.
const tenantBudgetConfigManifestType = "tenant-budget-config"

func newTenantBudgetConfigResource() resource.Resource {
	return &tenantBudgetConfigResource{}
}

// tenantBudgetConfigResource manages one tenant-scoped budget limiting v2
// rule via the manifest apply/delete API and the budgets list endpoint.
type tenantBudgetConfigResource struct {
	client *client.Client
}

type tenantBudgetConfigModel struct {
	ID        types.String            `tfsdk:"id"`
	Name      types.String            `tfsdk:"name"`
	Mode      types.String            `tfsdk:"mode"`
	Limits    *budgetV2LimitsModel    `tfsdk:"limits"`
	AppliesTo *budgetV2AppliesToModel `tfsdk:"applies_to"`
	When      *budgetV2WhenModel      `tfsdk:"when"`
	Alerts    *budgetV2AlertsModel    `tfsdk:"alerts"`
}

func (m *tenantBudgetConfigModel) body() budgetV2Body {
	return budgetV2Body{Name: m.Name, Mode: m.Mode, Limits: m.Limits, AppliesTo: m.AppliesTo, When: m.When, Alerts: m.Alerts}
}

func (m *tenantBudgetConfigModel) setBody(body budgetV2Body) {
	m.Name, m.Mode, m.Limits, m.AppliesTo, m.When, m.Alerts = body.Name, body.Mode, body.Limits, body.AppliesTo, body.When, body.Alerts
}

func (r *tenantBudgetConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenant_budget_config"
}

func (r *tenantBudgetConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages one tenant-scoped budget rule of AI gateway **budget limiting v2**: a spend cap on matching requests, " +
			"with optional alerting. Applied as a `tenant-budget-config` manifest via `PUT /api/svc/v1/apply`. " +
			"Unlike the deprecated `truefoundry_gateway_budget_config`, each rule is its own resource and rules are evaluated " +
			"independently — a request is blocked when any matching rule in `enforce` mode is over budget. " +
			"Import an existing rule with `terraform import truefoundry_tenant_budget_config.example <name>`.",
		Attributes: budgetV2Attributes(false),
	}
}

func (r *tenantBudgetConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *tenantBudgetConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config tenantBudgetConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateBudgetV2Body(config.body(), false, &resp.Diagnostics)
}

func (r *tenantBudgetConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan tenantBudgetConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = budgetV2Apply(ctx, r.client, budgetV2ManifestFrom(tenantBudgetConfigManifestType, "", plan.body()), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *tenantBudgetConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state tenantBudgetConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	budget, err := r.client.GetBudget(ctx, tenantBudgetConfigManifestType, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry tenant budget rule failed", err.Error())
		return
	}
	if budget == nil {
		tflog.Info(ctx, "tenant budget rule not found on the control plane; removing from state", map[string]any{"name": state.Name.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(budget.ID)
	state.setBody(budgetV2BodyFromManifest(budget.Manifest, state.body()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *tenantBudgetConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan tenantBudgetConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = budgetV2Apply(ctx, r.client, budgetV2ManifestFrom(tenantBudgetConfigManifestType, "", plan.body()), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *tenantBudgetConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state tenantBudgetConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, budgetV2ManifestFrom(tenantBudgetConfigManifestType, "", state.body()))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry tenant budget rule failed", err.Error())
	}
}

// ImportState imports a tenant budget rule by its name.
func (r *tenantBudgetConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
