package provider

import (
	"context"
	"fmt"

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
	_ resource.Resource                = (*roleResource)(nil)
	_ resource.ResourceWithConfigure   = (*roleResource)(nil)
	_ resource.ResourceWithImportState = (*roleResource)(nil)
)

// roleManifestType is the manifest type of a custom role.
const roleManifestType = "role"

// roleResourceTypeValues are the scopes the control plane accepts for
// user-defined roles. Every other scope the built-in roles use (workspace,
// cluster, secret-group, tracing-project, mlf-project, provider-account,
// mcp-server, agent, agent-app, virtual-account, provider-integration) is
// rejected with 400 "Resource type <x> is not supported".
//
// Note that "account" is not accepted here even though it round-trips on the
// control plane: it is stored as "tenant", so accepting it would leave the
// configuration permanently out of sync with the refreshed state.
var roleResourceTypeValues = []string{"tenant", "team"}

// rolePermissionValues is the permission vocabulary the control plane
// accepts, as <entity>:<PascalCaseAction>. Enumerated from
// GET /api/svc/v1/role on the dev control plane (2026-07-30) and the Core
// control plane (2026-09-01) — the union of both, since the two tenants
// disagreed in each direction. Keep in sync with rolePermissions in
// internal/mockcp/roles.go.
var rolePermissionValues = []string{
	"account:CreateAccount", "account:DeleteAccount", "account:ManageAccount", "account:ReadAccount",
	"agent:CreateAgent", "agent:DeleteAgent", "agent:ManageAgent", "agent:ReadAgent",
	"agent-app:CreateAgentApp", "agent-app:DeleteAgentApp", "agent-app:ManageAgentApp", "agent-app:ReadAgentApp",
	"agent-identity:DeleteAgentIdentities", "agent-identity:ManageAgentIdentities", "agent-identity:ReadAgentIdentities",
	"application:ListApplications", "application:ManageApplications",
	"cluster:CreateCluster", "cluster:DeleteCluster", "cluster:ManageClusters", "cluster:ReadCluster",
	"environment:ListEnvironments", "environment:ManageEnvironments",
	"external-identity:ListExternalIdentities", "external-identity:ManageExternalIdentities",
	"gateway-controls:ListGatewayControls", "gateway-controls:ManageGatewayControls",
	"mcp-server:ApproveMcpServer", "mcp-server:CreateMcpServer", "mcp-server:DeleteMcpServer", "mcp-server:ManageMcpServer", "mcp-server:ReadMcpServer", "mcp-server:UseMcpServer",
	"policy:ListPolicies", "policy:ManagePolicies",
	"provider-account:CreateProviderAccount", "provider-account:DeleteProviderAccount", "provider-account:ManageProviderAccount", "provider-account:ReadProviderAccount", "provider-account:UseIntegrations",
	"provider-integration:UseIntegration",
	"repository:CreateRepository", "repository:DeleteData", "repository:DeleteRepository", "repository:ManageRepository", "repository:ReadData", "repository:ReadRepository", "repository:WriteData",
	"role:ListRoles", "role:ManageRoles",
	"secret-group:CreateSecretGroup", "secret-group:DeleteSecretGroup", "secret-group:ManageSecretGroup", "secret-group:ReadData", "secret-group:ReadSecretGroup", "secret-group:WriteData",
	"settings:ListSettings", "settings:ManageSettings",
	"team:CreateTeam", "team:DeleteTeam", "team:ManageTeam", "team:ReadTeam",
	"tenant:AssignRole", "tenant:ReadAuditLogs",
	"tracing-project:CreateTracingProject", "tracing-project:DeleteTracingProject", "tracing-project:ManageTracingProject", "tracing-project:ReadData", "tracing-project:ReadTracingProject", "tracing-project:WriteData",
	"user:ListUsers", "user:ManageUsers",
	"virtual-account:CreateVirtualAccount", "virtual-account:DeleteVirtualAccount", "virtual-account:ManageVirtualAccount", "virtual-account:ReadVirtualAccount",
	"workspace:CreateWorkspace", "workspace:DeleteWorkspace", "workspace:ListWorkspaces", "workspace:ManageWorkspace", "workspace:ReadWorkspace",
}

func newRoleResource() resource.Resource {
	return &roleResource{}
}

// roleResource manages a custom role ("role" manifests) via the generic
// manifest apply/delete endpoints and the singular role list endpoint.
type roleResource struct {
	client *client.Client
}

type roleResourceModel struct {
	ID           types.String   `tfsdk:"id"`
	Name         types.String   `tfsdk:"name"`
	DisplayName  types.String   `tfsdk:"display_name"`
	Description  types.String   `tfsdk:"description"`
	ResourceType types.String   `tfsdk:"resource_type"`
	Permissions  []types.String `tfsdk:"permissions"`
	IsEditable   types.Bool     `tfsdk:"is_editable"`
	IsDefault    types.Bool     `tfsdk:"is_default"`
	IsReserved   types.Bool     `tfsdk:"is_reserved"`
}

func (r *roleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (r *roleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a custom TrueFoundry role (`role` manifest) — a named set of permissions that " +
			"`truefoundry_role_binding` permissions resolve against via their `role`. " +
			"Only roles scoped to the whole tenant or to a team can be user-defined; the control plane reserves " +
			"every other scope.\n\n" +
			"Roles TrueFoundry provides itself come in two flavours. Reserved roles (`is_editable = false`) cannot " +
			"be managed at all and this resource refuses to adopt them. Default roles (`is_default = true`, e.g. " +
			"`member`) can be updated in place, but they exist independently of Terraform — **import them, never " +
			"create them**, and note that destroying one only removes it from state, leaving the control plane " +
			"object with whatever permissions the last apply gave it.\n\n" +
			"Import an existing role by its name: `terraform import truefoundry_role.example member`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the role.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the role (3-35 characters; must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens), unique within the tenant. Changing it replaces the role.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(minNameLen, maxRoleNameLen),
					stringvalidator.RegexMatches(nameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"display_name": schema.StringAttribute{
				Required:    true,
				Description: "Human-readable name shown in the TrueFoundry UI.",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Human-readable description of what the role grants.",
			},
			"resource_type": schema.StringAttribute{
				Required: true,
				Description: "Scope of the role: `tenant` for a role assignable across the whole tenant, `team` for one " +
					"assignable within a team. The control plane also accepts the legacy alias `account` and stores it as " +
					"`tenant` — write `tenant`. Changing the scope of an existing role is rejected by the control plane; " +
					"remove and re-add the resource instead of expecting an in-place change.",
				Validators: []validator.String{stringvalidator.OneOf(roleResourceTypeValues...)},
			},
			"permissions": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: "Permissions the role grants, as `<entity>:<Action>` (e.g. `provider-account:UseIntegrations`). " +
					"At least one is required. The accepted set is fixed by the control plane; if a control plane upgrade " +
					"adds a permission this provider rejects, open an issue.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(rolePermissionValues...)),
				},
			},
			"is_editable": schema.BoolAttribute{
				Computed:    true,
				Description: "False for roles TrueFoundry reserves; those cannot be managed or imported.",
			},
			"is_default": schema.BoolAttribute{
				Computed:    true,
				Description: "True for roles TrueFoundry creates at tenant onboarding. Destroying such a role removes it from state only.",
			},
			"is_reserved": schema.BoolAttribute{
				Computed:    true,
				Description: "True for roles reserved by the control plane.",
			},
		},
	}
}

func (r *roleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan roleResourceModel
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

func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	role, err := r.client.GetRole(ctx, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry role failed", err.Error())
		return
	}
	if role == nil {
		tflog.Info(ctx, "role not found on the control plane; removing from state", map[string]any{"name": state.Name.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if !role.IsEditable {
		resp.Diagnostics.AddError("Role is not editable", roleNotEditableDetail(role.Name))
		return
	}

	roleToModel(role, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan roleResourceModel
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

// Delete removes the role, unless the control plane provides it. Deleting a
// TrueFoundry-provided default role is a tenant-wide permission change that a
// `terraform destroy` should never make on its own, so the resource leaves it
// in place and says so.
func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state roleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := state.Name.ValueString()
	role, err := r.client.GetRole(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry role before delete failed", err.Error())
		return
	}
	if role == nil {
		return
	}
	if role.IsDefault || role.IsReserved {
		resp.Diagnostics.AddWarning("Built-in role left on the control plane",
			fmt.Sprintf("Role %q is provided by TrueFoundry. Terraform removed it from state but did not delete it, "+
				"because doing so would change the permissions of every subject that holds it. It keeps the "+
				"permissions of the last apply — reset them in the TrueFoundry UI if that is not what you want.", name))
		return
	}

	if err := r.client.Delete(ctx, roleManifestFromModel(&state)); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry role failed", err.Error())
	}
}

// ImportState imports a role by its name, e.g. "member".
func (r *roleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// apply writes the manifest and reads the role back, since the generic apply
// response carries no control plane ID for roles.
func (r *roleResource) apply(ctx context.Context, plan *roleResourceModel, diags *diag.Diagnostics) {
	if _, err := r.client.Apply(ctx, roleManifestFromModel(plan), false); err != nil {
		diags.AddError("Applying TrueFoundry role manifest failed", err.Error())
		return
	}
	name := plan.Name.ValueString()
	role, err := r.client.GetRole(ctx, name)
	if err != nil {
		diags.AddError("Reading TrueFoundry role after apply failed", err.Error())
		return
	}
	if role == nil {
		diags.AddError("Applied TrueFoundry role not found", fmt.Sprintf("role %q is missing from the control plane right after applying it", name))
		return
	}
	tflog.Info(ctx, "applied role manifest", map[string]any{"name": name, "id": role.ID})
	plan.ID = types.StringValue(role.ID)
	plan.IsEditable = types.BoolValue(role.IsEditable)
	plan.IsDefault = types.BoolValue(role.IsDefault)
	plan.IsReserved = types.BoolValue(role.IsReserved)
}

// roleNotEditableDetail is shared by Read and the acceptance tests.
func roleNotEditableDetail(name string) string {
	return fmt.Sprintf("Role %q is reserved by TrueFoundry and cannot be managed by Terraform (isEditable=false). "+
		"Remove it from the configuration; it is not importable.", name)
}

func roleToModel(role *client.Role, m *roleResourceModel) {
	m.ID = types.StringValue(role.ID)
	m.Name = types.StringValue(role.Manifest.Name)
	m.DisplayName = types.StringValue(role.Manifest.DisplayName)
	m.Description = stringOrNull(role.Manifest.Description)
	// The control plane normalizes the scope (e.g. "account" to "tenant"), so
	// take the stored value rather than echoing the configuration.
	m.ResourceType = types.StringValue(role.ResourceType)
	m.Permissions = nil
	for _, p := range role.Manifest.Permissions {
		m.Permissions = append(m.Permissions, types.StringValue(p))
	}
	m.IsEditable = types.BoolValue(role.IsEditable)
	m.IsDefault = types.BoolValue(role.IsDefault)
	m.IsReserved = types.BoolValue(role.IsReserved)
}

func roleManifestFromModel(m *roleResourceModel) client.RoleManifest {
	manifest := client.RoleManifest{
		Type:         roleManifestType,
		Name:         m.Name.ValueString(),
		DisplayName:  m.DisplayName.ValueString(),
		Description:  m.Description.ValueString(),
		ResourceType: m.ResourceType.ValueString(),
		Permissions:  []string{},
	}
	for _, p := range m.Permissions {
		manifest.Permissions = append(manifest.Permissions, p.ValueString())
	}
	return manifest
}
