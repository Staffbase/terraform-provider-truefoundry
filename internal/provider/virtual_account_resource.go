package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	_ resource.Resource                = (*virtualAccountResource)(nil)
	_ resource.ResourceWithConfigure   = (*virtualAccountResource)(nil)
	_ resource.ResourceWithImportState = (*virtualAccountResource)(nil)
)

var expirationDateRegexp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// virtualAccountPermissionsDeprecation is the deprecation message of the
// virtual account `permissions` attribute, which control plane v0.172.0
// replaced with role-binding manifests.
const virtualAccountPermissionsDeprecation = "TrueFoundry retires virtual account permissions on 15 Nov 2026 " +
	"(control plane v0.172.0 moved access control into role bindings); applies that still send them fail after " +
	"that date. Grant access with truefoundry_role_binding and a virtualaccount subject instead, then remove " +
	"this attribute — removing it leaves existing grants in place. See the \"Migrating to role bindings\" guide."

func newVirtualAccountResource() resource.Resource {
	return &virtualAccountResource{}
}

// virtualAccountResource manages a TrueFoundry virtual account (service
// account) via the /api/svc/v1/virtual-accounts API.
type virtualAccountResource struct {
	client *client.Client
}

type virtualAccountModel struct {
	ID                types.String            `tfsdk:"id"`
	Name              types.String            `tfsdk:"name"`
	Permissions       []permissionModel       `tfsdk:"permissions"`
	ExpirationDate    types.String            `tfsdk:"expiration_date"`
	TokenType         types.String            `tfsdk:"token_type"`
	Tags              map[string]types.String `tfsdk:"tags"`
	SecretStoreConfig *secretStoreConfigModel `tfsdk:"secret_store_config"`
	AutoRotate        *autoRotateModel        `tfsdk:"auto_rotate"`
	Token             types.String            `tfsdk:"token"`
}

type permissionModel struct {
	ResourceFQN  types.String `tfsdk:"resource_fqn"`
	ResourceType types.String `tfsdk:"resource_type"`
	RoleID       types.String `tfsdk:"role_id"`
}

type secretStoreConfigModel struct {
	IntegrationFQN types.String `tfsdk:"integration_fqn"`
	SecretPath     types.String `tfsdk:"secret_path"`
}

type autoRotateModel struct {
	IntervalDays types.Int64 `tfsdk:"interval_days"`
	GracePeriod  types.Int64 `tfsdk:"grace_period_days"`
}

func (r *virtualAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_account"
}

func (r *virtualAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry virtual account (service account). Grant it access with " +
			"`truefoundry_role_binding` and a `virtualaccount` subject. " +
			"The account's API token is exposed as the sensitive `token` attribute; alternatively, " +
			"`secret_store_config` syncs it into a secret store. The token is only refreshed on read " +
			"when `auto_rotate` is set or the token isn't in state yet (e.g. right after import), so a " +
			"plan over many virtual accounts doesn't double its request count. " +
			"Existing accounts can be imported by their ID.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the virtual account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the virtual account (3-36 characters; must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens). Changing it replaces the account (and its token).",
				Validators: []validator.String{
					stringvalidator.LengthBetween(minNameLen, maxWideAccountNameLen),
					stringvalidator.RegexMatches(nameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"permissions": schema.SetNestedAttribute{
				Optional:           true,
				DeprecationMessage: virtualAccountPermissionsDeprecation,
				Description: "Roles granted to this virtual account on specific resources; use `truefoundry_role_binding` " +
					"with a `virtualaccount` subject instead. Unset leaves the account's grants on the " +
					"control plane untouched (and they are not tracked). Must not be empty: the control plane rejects " +
					"an empty list.",
				Validators: []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"resource_fqn": schema.StringAttribute{
							Required:    true,
							Description: "FQN of the resource the role applies to, e.g. a provider account FQN.",
						},
						"resource_type": schema.StringAttribute{
							Required:    true,
							Description: "Type of the resource, e.g. provider-account, cluster, workspace.",
						},
						"role_id": schema.StringAttribute{
							Required:    true,
							Description: "Role to assign, e.g. provider-account-access.",
						},
					},
				},
			},
			"expiration_date": schema.StringAttribute{
				Optional:    true,
				Description: "Expiration date of the account in yyyy-mm-dd format. Unset means no expiration.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(expirationDateRegexp, "must be a date in yyyy-mm-dd format"),
				},
			},
			"token_type": schema.StringAttribute{
				Optional:    true,
				Description: "Format of the issued token: `jwt` or `opaque`. Unset uses the platform default.",
				Validators: []validator.String{
					stringvalidator.OneOf("jwt", "opaque"),
				},
			},
			"tags": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Key-value pairs to categorize this virtual account.",
			},
			"secret_store_config": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Sync the account token into a secret store, e.g. Vault.",
				Attributes: map[string]schema.Attribute{
					"integration_fqn": schema.StringAttribute{
						Required:    true,
						Description: "FQN of the secret store integration.",
					},
					"secret_path": schema.StringAttribute{
						Required:    true,
						Description: "Path under which the token is stored.",
					},
				},
			},
			"auto_rotate": schema.SingleNestedAttribute{
				Optional: true,
				Description: "Turn on control-plane-side periodic token rotation. Presence of this block is what " +
					"activates rotation; there is no separate enabled flag. Setting it makes every read refetch the " +
					"token, since the whole point of rotation is for Terraform to pick up the new value — leave it " +
					"unset (the common case) and the token is set once on create and left as-is on refresh, so a " +
					"plan over many virtual accounts doesn't double its request count. Unverified against a real " +
					"control plane — Staffbase does not use this feature yet.",
				Attributes: map[string]schema.Attribute{
					"interval_days": schema.Int64Attribute{
						Required:    true,
						Description: "Days between rotations. Minimum 30.",
						Validators:  []validator.Int64{int64validator.AtLeast(30)},
					},
					"grace_period_days": schema.Int64Attribute{
						Required:    true,
						Description: "Days the pre-rotation token stays valid after a rotation. Minimum 1.",
						Validators:  []validator.Int64{int64validator.AtLeast(1)},
					},
				},
			},
			"token": schema.StringAttribute{
				Computed:  true,
				Sensitive: true,
				// Update never changes the token (it copies the state value),
				// so without this every in-place update would plan a
				// spurious "~ token = (sensitive value)".
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Description: "Current API token of the virtual account. Set once on create; only refreshed on " +
					"read when `auto_rotate` is set (or the token isn't in state yet, e.g. right after import).",
			},
		},
	}
}

func (r *virtualAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *virtualAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan virtualAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	account, token, err := r.client.ApplyVirtualAccount(ctx, virtualAccountManifestFromModel(&plan), false)
	if err != nil {
		resp.Diagnostics.AddError("Creating TrueFoundry virtual account failed", err.Error())
		return
	}
	plan.ID = types.StringValue(account.ID)
	if token == "" {
		// The token is only included in the apply response on creation;
		// fall back to the token endpoint just in case.
		token, err = r.client.GetVirtualAccountToken(ctx, account.ID)
		if err != nil {
			resp.Diagnostics.AddError("Fetching TrueFoundry virtual account token failed", err.Error())
			return
		}
	}
	plan.Token = types.StringValue(token)
	tflog.Info(ctx, "created virtual account", map[string]any{"id": account.ID})
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *virtualAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state virtualAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := consumeImportMark(ctx, req, resp)

	account, err := r.client.GetVirtualAccount(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		tflog.Info(ctx, "virtual account gone from control plane; removing from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry virtual account failed", err.Error())
		return
	}
	virtualAccountManifestToModel(account, &state, imported)

	// The token is set once on create and otherwise left as-is: an
	// already-known token is only refetched when auto_rotate is on (the
	// control plane may have rotated it since the last read), or when it's
	// never been populated yet (a fresh import).
	if state.AutoRotate != nil || state.Token.IsNull() {
		token, err := r.client.GetVirtualAccountToken(ctx, state.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Reading TrueFoundry virtual account token failed", err.Error())
			return
		}
		state.Token = types.StringValue(token)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *virtualAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan virtualAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state virtualAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	account, _, err := r.client.ApplyVirtualAccount(ctx, virtualAccountManifestFromModel(&plan), false)
	if err != nil {
		resp.Diagnostics.AddError("Updating TrueFoundry virtual account failed", err.Error())
		return
	}
	plan.ID = types.StringValue(account.ID)
	// Updates do not rotate the token.
	plan.Token = state.Token
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *virtualAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state virtualAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteVirtualAccount(ctx, state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry virtual account failed", err.Error())
	}
}

// ImportState imports a virtual account by its control plane ID. Read fills
// the manifest attributes and fetches the current token.
func (r *virtualAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	markImported(ctx, resp)
}

func virtualAccountManifestFromModel(m *virtualAccountModel) client.VirtualAccountManifest {
	manifest := client.VirtualAccountManifest{
		Name:           m.Name.ValueString(),
		Type:           "virtual-account",
		ExpirationDate: m.ExpirationDate.ValueString(),
		TokenType:      m.TokenType.ValueString(),
	}
	// Unset permissions are omitted, which leaves the account's existing
	// grants untouched; an empty set is rejected at plan time.
	for _, p := range m.Permissions {
		manifest.Permissions = append(manifest.Permissions, client.Permission{
			ResourceFQN:  p.ResourceFQN.ValueString(),
			ResourceType: p.ResourceType.ValueString(),
			RoleID:       p.RoleID.ValueString(),
		})
	}
	if len(m.Tags) > 0 {
		manifest.Tags = map[string]string{}
		for k, v := range m.Tags {
			manifest.Tags[k] = v.ValueString()
		}
	}
	if m.SecretStoreConfig != nil {
		manifest.SecretStoreConfig = &client.SecretStoreConfig{
			IntegrationFQN: m.SecretStoreConfig.IntegrationFQN.ValueString(),
			SecretPath:     m.SecretStoreConfig.SecretPath.ValueString(),
		}
	}
	if m.AutoRotate != nil {
		manifest.AutoRotate = &client.AutoRotate{
			IntervalDays: m.AutoRotate.IntervalDays.ValueInt64(),
			GracePeriod:  m.AutoRotate.GracePeriod.ValueInt64(),
		}
	}
	return manifest
}

func virtualAccountManifestToModel(account *client.VirtualAccount, state *virtualAccountModel, imported bool) {
	manifest := account.Manifest
	state.ID = types.StringValue(account.ID)
	state.Name = types.StringValue(manifest.Name)
	state.ExpirationDate = stringOrNull(manifest.ExpirationDate)
	state.TokenType = stringOrNull(manifest.TokenType)
	// Like collaborators (see refreshCollaborators): an unset attribute stays
	// unset unless the account was just imported, and a response without
	// permissions keeps the prior value.
	if (state.Permissions != nil || imported) && len(manifest.Permissions) > 0 {
		state.Permissions = make([]permissionModel, 0, len(manifest.Permissions))
		for _, p := range manifest.Permissions {
			state.Permissions = append(state.Permissions, permissionModel{
				ResourceFQN:  types.StringValue(p.ResourceFQN),
				ResourceType: types.StringValue(p.ResourceType),
				RoleID:       types.StringValue(p.RoleID),
			})
		}
	}
	state.Tags = nil
	if len(manifest.Tags) > 0 {
		state.Tags = map[string]types.String{}
		for k, v := range manifest.Tags {
			state.Tags[k] = types.StringValue(v)
		}
	}
	state.SecretStoreConfig = nil
	if manifest.SecretStoreConfig != nil {
		state.SecretStoreConfig = &secretStoreConfigModel{
			IntegrationFQN: types.StringValue(manifest.SecretStoreConfig.IntegrationFQN),
			SecretPath:     types.StringValue(manifest.SecretStoreConfig.SecretPath),
		}
	}
	state.AutoRotate = nil
	if manifest.AutoRotate != nil {
		state.AutoRotate = &autoRotateModel{
			IntervalDays: types.Int64Value(manifest.AutoRotate.IntervalDays),
			GracePeriod:  types.Int64Value(manifest.AutoRotate.GracePeriod),
		}
	}
}
