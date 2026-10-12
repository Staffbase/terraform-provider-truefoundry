package provider

import (
	"context"
	"fmt"
	"strings"

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
	_ resource.Resource                = (*secretResource)(nil)
	_ resource.ResourceWithConfigure   = (*secretResource)(nil)
	_ resource.ResourceWithImportState = (*secretResource)(nil)
)

func newSecretResource() resource.Resource {
	return &secretResource{}
}

// secretResource manages one secret inside a secret group. Secrets have no
// manifest: they are written through the secret group update endpoint and
// read and deleted through the secrets endpoints (see client/secrets.go).
type secretResource struct {
	client *client.Client
}

type secretResourceModel struct {
	ID            types.String `tfsdk:"id"`
	FQN           types.String `tfsdk:"fqn"`
	SecretGroupID types.String `tfsdk:"secret_group_id"`
	Key           types.String `tfsdk:"key"`
	Value         types.String `tfsdk:"value"`
}

func (r *secretResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (r *secretResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages one secret in a TrueFoundry secret group (`truefoundry_secret_group`). Reference it from " +
			"any credential attribute as `tfy-secret://${truefoundry_secret.example.fqn}`.\n\n" +
			"**The secret value is stored in the Terraform state** (marked sensitive). Use this resource for values " +
			"Terraform already holds — e.g. credentials created by another resource — and keep secrets that " +
			"should never touch the state out of band.\n\n" +
			"Other secrets in the same group, including ones created in the UI, are left untouched. The control " +
			"plane replaces a group's whole secret list on every write, so the provider serializes writes to one " +
			"group. Changing the value creates a new secret version; a value changed out of band is detected " +
			"and reverted, unless the control plane hides secret values (`DISABLE_SECRET_VALUE_VIEW`).\n\n" +
			"Import a secret by its FQN, `<tenant>:<group>:<key>`: " +
			"`terraform import truefoundry_secret.example my-tenant:system:vault-role-id`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the secret.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed:      true,
				Description:   "Fully qualified name of the secret, `<tenant>:<group>:<key>`. Prefix it with `tfy-secret://` to reference the secret.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"secret_group_id": schema.StringAttribute{
				Required:      true,
				Description:   "Control plane ID of the secret group holding the secret, e.g. `truefoundry_secret_group.example.id`. Changing it replaces the secret.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"key": schema.StringAttribute{
				Required:      true,
				Description:   "Key of the secret, unique within its group. Changing it replaces the secret.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"value": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Value of the secret. Stored in the Terraform state.",
			},
		},
	}
}

func (r *secretResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *secretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan secretResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, &plan, resp.Diagnostics.AddError) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *secretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state secretResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupID, key := state.SecretGroupID.ValueString(), state.Key.ValueString()
	secrets, err := r.client.ListSecrets(ctx, groupID)
	if client.IsNotFound(err) {
		tflog.Info(ctx, "secret group not found on the control plane; removing secret from state",
			map[string]any{"secret_group_id": groupID, "key": key})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry secret group failed", err.Error())
		return
	}
	var ref *client.SecretRef
	for i, s := range secrets {
		if s.Name == key {
			ref = &secrets[i]
			break
		}
	}
	if ref == nil {
		tflog.Info(ctx, "secret not found on the control plane; removing from state",
			map[string]any{"secret_group_id": groupID, "key": key})
		resp.State.RemoveResource(ctx)
		return
	}

	secret, err := r.client.GetSecret(ctx, ref.ID)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry secret failed", err.Error())
		return
	}

	state.ID = types.StringValue(ref.ID)
	state.FQN = types.StringValue(ref.FQN)
	// A control plane with DISABLE_SECRET_VALUE_VIEW returns no value; keep
	// the state's value then, which leaves out-of-band changes undetected.
	if secret.Value != nil {
		state.Value = types.StringValue(*secret.Value)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *secretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan secretResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.put(ctx, &plan, resp.Diagnostics.AddError) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *secretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state secretResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteSecret(ctx, state.SecretGroupID.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry secret failed", err.Error())
	}
}

// ImportState imports a secret by its FQN, <tenant>:<group>:<key>, resolving
// the group's control plane ID; Read fills in the rest.
func (r *secretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, ":", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("expected the secret FQN <tenant>:<group>:<key>, got %q", req.ID))
		return
	}
	groupFQN := parts[0] + ":" + parts[1]
	groups, err := r.client.ListSecretGroups(ctx, groupFQN)
	if err != nil {
		resp.Diagnostics.AddError("Looking up TrueFoundry secret group failed", err.Error())
		return
	}
	var groupID string
	for _, g := range groups {
		if g.FQN == groupFQN {
			groupID = g.ID
			break
		}
	}
	if groupID == "" {
		resp.Diagnostics.AddError("Secret group not found", fmt.Sprintf("no secret group with FQN %q", groupFQN))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("secret_group_id"), groupID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), parts[2])...)
}

// put writes the planned value and records the secret's ID and FQN in m.
func (r *secretResource) put(ctx context.Context, m *secretResourceModel, addError func(string, string)) bool {
	ref, err := r.client.PutSecret(ctx, m.SecretGroupID.ValueString(), m.Key.ValueString(), m.Value.ValueString())
	if err != nil {
		addError("Writing TrueFoundry secret failed", err.Error())
		return false
	}
	tflog.Info(ctx, "wrote secret", map[string]any{"fqn": ref.FQN, "id": ref.ID})
	m.ID = types.StringValue(ref.ID)
	m.FQN = types.StringValue(ref.FQN)
	return true
}
