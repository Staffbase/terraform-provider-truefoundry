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
	"sort"

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
	_ resource.Resource                = (*systemSecretStoreResource)(nil)
	_ resource.ResourceWithConfigure   = (*systemSecretStoreResource)(nil)
	_ resource.ResourceWithImportState = (*systemSecretStoreResource)(nil)
)

func newSystemSecretStoreResource() resource.Resource {
	return &systemSecretStoreResource{}
}

// systemSecretStoreResource manages a TrueFoundry provider account carrying
// DB-backed system secret stores via the manifest-based apply/delete API.
type systemSecretStoreResource struct {
	client *client.Client
}

type systemSecretStoreModel struct {
	ID           types.String             `tfsdk:"id"`
	FQN          types.String             `tfsdk:"fqn"`
	Name         types.String             `tfsdk:"name"`
	SecretStores []systemSecretStoreEntry `tfsdk:"secret_stores"`
}

type systemSecretStoreEntry struct {
	Name               types.String   `tfsdk:"name"`
	AuthorizedSubjects []types.String `tfsdk:"authorized_subjects"`
}

func (r *systemSecretStoreResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system_secret_store"
}

func (r *systemSecretStoreResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry provider account carrying DB-backed system secret stores, " +
			"applied as a `provider-account/truefoundry` manifest via `PUT /api/svc/v1/apply`. " +
			"System secret stores keep secrets in the control plane's own database — no external " +
			"configuration or credentials — and back secret references like " +
			"`tfy-secret://my-tenant:my-account:secret-name`. " +
			"Existing accounts can be imported by their FQN, e.g. `terraform import truefoundry_system_secret_store.example my-tenant:truefoundry:my-account`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the TrueFoundry account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed:      true,
				Description:   "Fully qualified name of the TrueFoundry account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the TrueFoundry account (3-32 characters; must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens). Changing it replaces the account.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(minNameLen, maxAccountNameLen),
					stringvalidator.RegexMatches(nameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"secret_stores": schema.SetNestedAttribute{
				Required:    true,
				Description: "DB-backed secret stores in this account.",
				Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name identifying this secret store.",
							Validators:  []validator.String{stringvalidator.LengthBetween(1, 64)},
						},
						"authorized_subjects": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Subjects allowed to use this secret store, as `<user_type>:<username>` FQNs.",
						},
					},
				},
			},
		},
	}
}

func (r *systemSecretStoreResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *systemSecretStoreResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan systemSecretStoreModel
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

func (r *systemSecretStoreResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state systemSecretStoreModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	integrations, err := r.client.ListTrueFoundrySecretStoreIntegrations(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry secret store integrations failed", err.Error())
		return
	}

	var account *client.EmbeddedProviderAccount
	remote := map[string]client.TrueFoundrySecretStore{}
	for _, integration := range integrations {
		if integration.ProviderAccountFQN != state.FQN.ValueString() ||
			integration.Manifest.Type != "integration/secret-store/truefoundry/db" {
			continue
		}
		remote[integration.Manifest.Name] = integration.Manifest
		if account == nil && integration.ProviderAccount != nil {
			account = integration.ProviderAccount
		}
	}
	if len(remote) == 0 {
		tflog.Info(ctx, "truefoundry account has no secret stores on the control plane; removing from state",
			map[string]any{"fqn": state.FQN.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	if account != nil {
		state.ID = types.StringValue(account.ID)
		state.Name = types.StringValue(account.Manifest.Name)
	}

	names := make([]string, 0, len(remote))
	for name := range remote {
		names = append(names, name)
	}
	sort.Strings(names)
	stores := make([]systemSecretStoreEntry, 0, len(names))
	for _, name := range names {
		stores = append(stores, systemSecretStoreToModel(remote[name]))
	}
	state.SecretStores = stores

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *systemSecretStoreResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan systemSecretStoreModel
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

func (r *systemSecretStoreResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state systemSecretStoreModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, systemSecretStoreManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry system secret store account failed", err.Error())
	}
}

// ImportState imports a TrueFoundry account by its FQN, e.g.
// "my-tenant:truefoundry:my-account".
func (r *systemSecretStoreResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("fqn"), req, resp)
}

func (r *systemSecretStoreResource) apply(ctx context.Context, plan *systemSecretStoreModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, systemSecretStoreManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry system secret store manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied truefoundry account manifest", map[string]any{
		"action": result.Action,
		"fqn":    result.Data.FQN,
	})
	plan.ID = types.StringValue(result.Data.ID)
	plan.FQN = types.StringValue(result.Data.FQN)
}

func systemSecretStoreManifestFromModel(m *systemSecretStoreModel) client.TrueFoundryAccountManifest {
	manifest := client.TrueFoundryAccountManifest{
		Type:         "provider-account/truefoundry",
		Name:         m.Name.ValueString(),
		Integrations: []client.TrueFoundrySecretStore{},
	}
	for _, s := range m.SecretStores {
		manifest.Integrations = append(manifest.Integrations, client.TrueFoundrySecretStore{
			Type:               "integration/secret-store/truefoundry/db",
			Name:               s.Name.ValueString(),
			AuthorizedSubjects: subjectsFromModel(s.AuthorizedSubjects),
		})
	}
	return manifest
}

func systemSecretStoreToModel(manifest client.TrueFoundrySecretStore) systemSecretStoreEntry {
	return systemSecretStoreEntry{
		Name:               types.StringValue(manifest.Name),
		AuthorizedSubjects: subjectsToModel(manifest.AuthorizedSubjects),
	}
}
