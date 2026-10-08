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
	_ resource.Resource                   = (*roleBindingResource)(nil)
	_ resource.ResourceWithConfigure      = (*roleBindingResource)(nil)
	_ resource.ResourceWithValidateConfig = (*roleBindingResource)(nil)
	_ resource.ResourceWithImportState    = (*roleBindingResource)(nil)
)

// roleBindingManifestType is the manifest type of a role binding.
const roleBindingManifestType = "role-binding"

// roleBindingSubjectTypeValues are the principal kinds a role binding grants
// to.
var roleBindingSubjectTypeValues = []string{"user", "team", "virtualaccount", "agent"}

func newRoleBindingResource() resource.Resource {
	return &roleBindingResource{}
}

// roleBindingResource manages a role binding ("role-binding" manifest) via
// the dedicated /api/svc/v1/role-bindings endpoints.
type roleBindingResource struct {
	client *client.Client
}

type roleBindingResourceModel struct {
	ID          types.String                 `tfsdk:"id"`
	Name        types.String                 `tfsdk:"name"`
	Subjects    []roleBindingSubjectModel    `tfsdk:"subjects"`
	Permissions []roleBindingPermissionModel `tfsdk:"permissions"`
}

type roleBindingSubjectModel struct {
	Type types.String `tfsdk:"type"`
	Name types.String `tfsdk:"name"`
}

type roleBindingPermissionModel struct {
	ResourceType types.String `tfsdk:"resource_type"`
	ResourceFQN  types.String `tfsdk:"resource_fqn"`
	Role         types.String `tfsdk:"role"`
}

func (r *roleBindingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role_binding"
}

func (r *roleBindingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry role binding (`role-binding` manifest): grants roles on resources to users, " +
			"teams, virtual accounts, or agents. Role bindings replace the per-resource `collaborators` and the virtual " +
			"account `permissions`, which TrueFoundry retires on 15 Nov 2026.\n\n" +
			"A binding has either **one subject with many permissions** or **many subjects with one permission**, never " +
			"many of both — split it into one binding per role to give several subjects several roles. The control plane " +
			"matches bindings by name, so applying a name that already exists takes that binding over.\n\n" +
			"Import an existing binding by its name: `terraform import truefoundry_role_binding.example my-binding`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the role binding.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true,
				Description: fmt.Sprintf("Name of the role binding (%d-%d characters; must start with a lowercase letter, "+
					"end with a letter or digit, and contain only lowercase letters, digits and hyphens), unique within the "+
					"tenant. Changing it replaces the binding.", minNameLen, maxRoleBindingNameLen),
				Validators: []validator.String{
					stringvalidator.LengthBetween(minNameLen, maxRoleBindingNameLen),
					stringvalidator.RegexMatches(nameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"subjects": schema.SetNestedAttribute{
				Required:    true,
				Description: "Principals the permissions are granted to. More than one subject requires exactly one permission.",
				Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Required:    true,
							Description: fmt.Sprintf("Kind of principal. One of: `%s`.", strings.Join(roleBindingSubjectTypeValues, "`, `")),
							Validators:  []validator.String{stringvalidator.OneOf(roleBindingSubjectTypeValues...)},
						},
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Email address for `user`; the team, virtual account, or agent name otherwise.",
							Validators:  []validator.String{stringvalidator.LengthBetween(1, 1024)},
						},
					},
				},
			},
			"permissions": schema.SetNestedAttribute{
				Required:    true,
				Description: "Roles granted on resources. More than one permission requires exactly one subject.",
				Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"resource_type": schema.StringAttribute{
							Required:    true,
							Description: "Kind of resource the role applies to, e.g. `provider-account`, `mcp-server`, `secret-group`, `ml-repo`, `workspace`.",
							Validators:  []validator.String{stringvalidator.LengthBetween(1, 128)},
						},
						"resource_fqn": schema.StringAttribute{
							Required: true,
							Description: "FQN of the resource, e.g. the `fqn` attribute of a `truefoundry_custom_endpoint_account` " +
								"or `truefoundry_guardrail_group`.",
							Validators: []validator.String{stringvalidator.LengthBetween(1, 1024)},
						},
						"role": schema.StringAttribute{
							Required:    true,
							Description: "Role to grant on the resource, e.g. `provider-account-access` or `mcp-server-user`.",
							Validators:  []validator.String{stringvalidator.LengthBetween(1, 255)},
						},
					},
				},
			},
		},
	}
}

func (r *roleBindingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the control plane's one-subject-or-one-permission
// rule. Keep in sync with validateRoleBindingManifest in
// internal/mockcp/role_bindings.go.
func (r *roleBindingResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config roleBindingResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(config.Subjects) > 1 && len(config.Permissions) > 1 {
		resp.Diagnostics.AddAttributeError(path.Root("permissions"), "Invalid role binding",
			fmt.Sprintf("A role binding can have either one subject with many permissions or many subjects with one "+
				"permission, not both (got %d subjects and %d permissions). Split it into one binding per role.",
				len(config.Subjects), len(config.Permissions)))
	}
}

func (r *roleBindingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan roleBindingResourceModel
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

func (r *roleBindingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleBindingResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var binding *client.RoleBinding
	var err error
	if id := state.ID.ValueString(); id != "" {
		binding, err = r.client.GetRoleBinding(ctx, id)
	} else {
		// Right after import only the name is known.
		binding, err = r.client.GetRoleBindingByName(ctx, state.Name.ValueString())
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry role binding failed", err.Error())
		return
	}
	if binding == nil {
		tflog.Info(ctx, "role binding not found on the control plane; removing from state",
			map[string]any{"id": state.ID.ValueString(), "name": state.Name.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	roleBindingToModel(binding, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *roleBindingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan roleBindingResourceModel
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

func (r *roleBindingResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state roleBindingResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteRoleBinding(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry role binding failed", err.Error())
	}
}

// ImportState imports a role binding by its name; Read resolves the ID.
func (r *roleBindingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *roleBindingResource) apply(ctx context.Context, plan *roleBindingResourceModel, diags *diag.Diagnostics) {
	binding, err := r.client.ApplyRoleBinding(ctx, roleBindingManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry role binding failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied role binding", map[string]any{"name": plan.Name.ValueString(), "id": binding.ID})
	plan.ID = types.StringValue(binding.ID)
}

func roleBindingToModel(binding *client.RoleBinding, m *roleBindingResourceModel) {
	m.ID = types.StringValue(binding.ID)
	m.Name = types.StringValue(binding.Manifest.Name)
	m.Subjects = make([]roleBindingSubjectModel, 0, len(binding.Manifest.Subjects))
	for _, s := range binding.Manifest.Subjects {
		m.Subjects = append(m.Subjects, roleBindingSubjectModel{
			Type: types.StringValue(s.Type),
			Name: types.StringValue(s.Name),
		})
	}
	m.Permissions = make([]roleBindingPermissionModel, 0, len(binding.Manifest.Permissions))
	for _, p := range binding.Manifest.Permissions {
		m.Permissions = append(m.Permissions, roleBindingPermissionModel{
			ResourceType: types.StringValue(p.ResourceType),
			ResourceFQN:  types.StringValue(p.ResourceFQN),
			Role:         types.StringValue(p.Role),
		})
	}
}

func roleBindingManifestFromModel(m *roleBindingResourceModel) client.RoleBindingManifest {
	manifest := client.RoleBindingManifest{
		Type:        roleBindingManifestType,
		Name:        m.Name.ValueString(),
		Subjects:    make([]client.RoleBindingSubject, 0, len(m.Subjects)),
		Permissions: make([]client.RoleBindingPermission, 0, len(m.Permissions)),
	}
	for _, s := range m.Subjects {
		manifest.Subjects = append(manifest.Subjects, client.RoleBindingSubject{
			Type: s.Type.ValueString(),
			Name: s.Name.ValueString(),
		})
	}
	for _, p := range m.Permissions {
		manifest.Permissions = append(manifest.Permissions, client.RoleBindingPermission{
			ResourceType: p.ResourceType.ValueString(),
			ResourceFQN:  p.ResourceFQN.ValueString(),
			Role:         p.Role.ValueString(),
		})
	}
	return manifest
}
