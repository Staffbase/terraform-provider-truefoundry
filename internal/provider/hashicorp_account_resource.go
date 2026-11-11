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

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

var (
	_ resource.Resource                   = (*hashicorpAccountResource)(nil)
	_ resource.ResourceWithConfigure      = (*hashicorpAccountResource)(nil)
	_ resource.ResourceWithValidateConfig = (*hashicorpAccountResource)(nil)
	_ resource.ResourceWithImportState    = (*hashicorpAccountResource)(nil)
)

// defaultAppRolePath is the mount path of Vault's AppRole auth method, and
// TrueFoundry's documented default for an integration's role path.
const defaultAppRolePath = "approle"

func newHashicorpAccountResource() resource.Resource {
	return &hashicorpAccountResource{}
}

// hashicorpAccountResource manages a HashiCorp provider account carrying
// Vault secret store integrations via the manifest-based apply/delete API.
type hashicorpAccountResource struct {
	client *client.Client
}

type hashicorpAccountModel struct {
	ID     types.String `tfsdk:"id"`
	FQN    types.String `tfsdk:"fqn"`
	Name   types.String `tfsdk:"name"`
	Vaults []vaultModel `tfsdk:"vaults"`
}

type vaultModel struct {
	Name               types.String       `tfsdk:"name"`
	VaultURL           types.String       `tfsdk:"vault_url"`
	KVMountPath        types.String       `tfsdk:"kv_mount_path"`
	RootPath           types.String       `tfsdk:"root_path"`
	Namespace          types.String       `tfsdk:"namespace"`
	Token              types.String       `tfsdk:"token"`
	AppRole            *vaultAppRoleModel `tfsdk:"approle"`
	AuthorizedSubjects []types.String     `tfsdk:"authorized_subjects"`
}

type vaultAppRoleModel struct {
	RoleID   types.String `tfsdk:"role_id"`
	SecretID types.String `tfsdk:"secret_id"`
	RolePath types.String `tfsdk:"role_path"`
}

func (r *hashicorpAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_hashicorp_account"
}

func (r *hashicorpAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry HashiCorp provider account carrying Vault KV secret store integrations, " +
			"applied as a `provider-account/hashicorp` manifest via `PUT /api/svc/v1/apply`. " +
			"Secret stores back TrueFoundry secret references like `my-tenant:hashicorp:my-account:secret-store:my-vault::path/value`. " +
			"Existing accounts can be imported by their FQN, e.g. `terraform import truefoundry_hashicorp_account.example my-tenant:hashicorp:my-account`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the HashiCorp account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed:      true,
				Description:   "Fully qualified name of the HashiCorp account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the HashiCorp account (3-36 characters; must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens). Changing it replaces the account.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(minNameLen, maxWideAccountNameLen),
					stringvalidator.RegexMatches(nameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"vaults": schema.ListNestedAttribute{
				Required:      true,
				Description:   "Vault KV secret stores accessible through this account.",
				Validators:    []validator.List{listvalidator.SizeAtLeast(1)},
				PlanModifiers: []planmodifier.List{orderInsensitive()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name identifying this secret store integration.",
							Validators:  []validator.String{stringvalidator.LengthBetween(1, 64)},
						},
						"vault_url": schema.StringAttribute{
							Required:    true,
							Description: "Base URL of the Vault server, e.g. https://vault.example.com.",
						},
						"kv_mount_path": schema.StringAttribute{
							Required:    true,
							Description: "Mount path of the KV secrets engine, e.g. secret or de1/dev.",
						},
						"root_path": schema.StringAttribute{
							Optional:    true,
							Description: "Path prefix within the KV mount under which secrets are read.",
						},
						"namespace": schema.StringAttribute{
							Optional:    true,
							Description: "Vault Enterprise namespace to use.",
						},
						"token": schema.StringAttribute{
							Optional:    true,
							Sensitive:   true,
							Description: "Vault token for token auth. Exactly one of `token` and `approle` must be set. The control plane verifies the credentials against Vault at apply time.",
						},
						"approle": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Vault AppRole auth. Exactly one of `token` and `approle` must be set. The control plane verifies the credentials against Vault at apply time.",
							Attributes: map[string]schema.Attribute{
								"role_id": schema.StringAttribute{
									Required:    true,
									Description: "AppRole role ID. Accepts TrueFoundry secret references.",
								},
								"secret_id": schema.StringAttribute{
									Required:    true,
									Sensitive:   true,
									Description: "AppRole secret ID. Accepts TrueFoundry secret references.",
								},
								"role_path": schema.StringAttribute{
									Optional: true,
									Computed: true,
									Default:  stringdefault.StaticString(defaultAppRolePath),
									Description: "Path the AppRole auth method is mounted at in Vault. Defaults to `" + defaultAppRolePath + "`. " +
										"Always sent, so a store saved without it (e.g. by an earlier provider version) is detected as drift and fixed on apply.",
								},
							},
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

func (r *hashicorpAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces exactly one auth method per vault.
func (r *hashicorpAccountResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config hashicorpAccountModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// vaults is a set, so elements have no addressable index; errors are
	// anchored at the attribute and name the offending vault in the message.
	for _, v := range config.Vaults {
		hasToken := !v.Token.IsNull()
		hasAppRole := v.AppRole != nil
		if hasToken == hasAppRole {
			resp.Diagnostics.AddAttributeError(path.Root("vaults"), "Invalid vault configuration",
				fmt.Sprintf("Vault %q: exactly one of `token` and `approle` must be set.", v.Name.ValueString()))
		}
	}
}

func (r *hashicorpAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan hashicorpAccountModel
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

func (r *hashicorpAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state hashicorpAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	integrations, err := r.client.ListSecretStoreIntegrations(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry secret store integrations failed", err.Error())
		return
	}

	var account *client.EmbeddedProviderAccount
	remote := map[string]client.VaultIntegration{}
	for _, integration := range integrations {
		if integration.ProviderAccountFQN != state.FQN.ValueString() {
			continue
		}
		remote[integration.Manifest.Name] = integration.Manifest
		if account == nil && integration.ProviderAccount != nil {
			account = integration.ProviderAccount
		}
	}
	if len(remote) == 0 {
		tflog.Info(ctx, "hashicorp account has no secret stores on the control plane; removing from state",
			map[string]any{"fqn": state.FQN.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	if account != nil {
		state.ID = types.StringValue(account.ID)
		state.Name = types.StringValue(account.Manifest.Name)
	}

	// Rebuild the list keeping configured order for entries that still exist
	// and appending unknown remote entries in name order, so pure refreshes
	// stay diff-free while real drift is surfaced.
	var vaults []vaultModel
	for _, v := range state.Vaults {
		manifest, ok := remote[v.Name.ValueString()]
		if !ok {
			continue
		}
		delete(remote, v.Name.ValueString())
		vaults = append(vaults, vaultToModel(manifest))
	}
	extra := make([]string, 0, len(remote))
	for name := range remote {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	for _, name := range extra {
		vaults = append(vaults, vaultToModel(remote[name]))
	}
	state.Vaults = vaults

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *hashicorpAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan hashicorpAccountModel
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

func (r *hashicorpAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state hashicorpAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, hashicorpManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry HashiCorp account failed", err.Error())
	}
}

// ImportState imports a HashiCorp account by its FQN, e.g. "my-tenant:hashicorp:my-account".
func (r *hashicorpAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("fqn"), req, resp)
}

func (r *hashicorpAccountResource) apply(ctx context.Context, plan *hashicorpAccountModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, hashicorpManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry HashiCorp account manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied hashicorp account manifest", map[string]any{
		"action": result.Action,
		"fqn":    result.Data.FQN,
	})
	plan.ID = types.StringValue(result.Data.ID)
	plan.FQN = types.StringValue(result.Data.FQN)
}

func hashicorpManifestFromModel(m *hashicorpAccountModel) client.HashicorpAccountManifest {
	manifest := client.HashicorpAccountManifest{
		Type:         "provider-account/hashicorp",
		Name:         m.Name.ValueString(),
		Integrations: []client.VaultIntegration{},
	}
	for _, v := range m.Vaults {
		integration := client.VaultIntegration{
			Type:               "integration/secret-store/hashicorp/vault",
			Name:               v.Name.ValueString(),
			VaultURL:           v.VaultURL.ValueString(),
			KVMountPath:        v.KVMountPath.ValueString(),
			RootPath:           v.RootPath.ValueString(),
			Namespace:          v.Namespace.ValueString(),
			AuthorizedSubjects: subjectsFromModel(v.AuthorizedSubjects),
		}
		if v.AppRole != nil {
			integration.AuthData = &client.APIKeyAuth{
				Type:     "approle",
				RoleID:   v.AppRole.RoleID.ValueString(),
				SecretID: v.AppRole.SecretID.ValueString(),
				RolePath: v.AppRole.RolePath.ValueString(),
			}
		} else {
			integration.AuthData = &client.APIKeyAuth{Type: "token", Token: v.Token.ValueString()}
		}
		manifest.Integrations = append(manifest.Integrations, integration)
	}
	return manifest
}

func vaultToModel(manifest client.VaultIntegration) vaultModel {
	v := vaultModel{
		Name:               types.StringValue(manifest.Name),
		VaultURL:           types.StringValue(manifest.VaultURL),
		KVMountPath:        types.StringValue(manifest.KVMountPath),
		RootPath:           stringOrNull(manifest.RootPath),
		Namespace:          stringOrNull(manifest.Namespace),
		AuthorizedSubjects: subjectsToModel(manifest.AuthorizedSubjects),
	}
	if auth := manifest.AuthData; auth != nil {
		switch auth.Type {
		case "approle":
			v.AppRole = &vaultAppRoleModel{
				RoleID:   types.StringValue(auth.RoleID),
				SecretID: types.StringValue(auth.SecretID),
				// Left null when the control plane has none stored, so the
				// planned default shows up as drift and gets written.
				RolePath: stringOrNull(auth.RolePath),
			}
		default:
			v.Token = stringOrNull(auth.Token)
		}
	}
	return v
}
