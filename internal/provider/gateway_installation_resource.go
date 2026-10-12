package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

var (
	_ resource.Resource                = (*gatewayInstallationResource)(nil)
	_ resource.ResourceWithConfigure   = (*gatewayInstallationResource)(nil)
	_ resource.ResourceWithImportState = (*gatewayInstallationResource)(nil)
)

const gatewayInstallationManifestType = "gateway-installation"

// Name and URL patterns of the gateway installation manifest, read off a
// control plane on 2026-10-12.
var (
	gatewayInstallationNameRegexp = regexp.MustCompile(`^[a-z][a-z0-9\-]{1,33}[a-z0-9]$`)
	gatewayInstallationURLRegexp  = regexp.MustCompile(`^https?://[^\s]+$`)
)

func newGatewayInstallationResource() resource.Resource {
	return &gatewayInstallationResource{}
}

// gatewayInstallationResource manages a gateway installation: applied through
// the generic manifest endpoint, read and deleted through the installations
// endpoints (see client/gateway_installations.go).
type gatewayInstallationResource struct {
	client *client.Client
}

type gatewayInstallationResourceModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	URL       types.String `tfsdk:"url"`
	Enabled   types.Bool   `tfsdk:"enabled"`
	IsDefault types.Bool   `tfsdk:"is_default"`
	Token     types.String `tfsdk:"token"`
}

func (r *gatewayInstallationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_installation"
}

func (r *gatewayInstallationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry gateway installation (`gateway-installation` manifest) — a self-hosted " +
			"gateway plane registered on the control plane with the URL it serves (Settings → Gateway Endpoints).\n\n" +
			"On create, the provider mints the gateway's token, which the gateway plane uses as `TFY_API_KEY` to " +
			"authenticate to the control plane. **The token is stored in the Terraform state** and fetched only " +
			"once: an imported installation has no token, and replacing the installation mints a new one.\n\n" +
			"Import an existing installation by its name: " +
			"`terraform import truefoundry_gateway_installation.example my-gateway`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the gateway installation.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the gateway installation (3-35 characters; must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens). Changing it replaces the installation and mints a new token.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(gatewayInstallationNameRegexp, "must be 3-35 characters, start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"url": schema.StringAttribute{
				Required:    true,
				Description: "Base URL the gateway plane is reachable at, e.g. https://gateway.example.com. Unique within the tenant.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(gatewayInstallationURLRegexp, "must be an http(s) URL"),
				},
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the gateway endpoint is offered on the control plane, e.g. in the Playground. Defaults to true.",
			},
			"is_default": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Whether this is the tenant's default gateway endpoint. Defaults to false.",
			},
			"token": schema.StringAttribute{
				Computed:      true,
				Sensitive:     true,
				Description:   "Token the gateway plane authenticates to the control plane with (its `TFY_API_KEY`). Minted once on create; null for imported installations.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *gatewayInstallationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *gatewayInstallationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayInstallationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.apply(ctx, &plan, resp.Diagnostics.AddError) {
		return
	}
	// Persist the installation before minting its token, so a failed token
	// call leaves a tracked resource behind rather than an orphan.
	plan.Token = types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	token, err := r.client.GatewayInstallationToken(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Minting TrueFoundry gateway installation token failed",
			err.Error()+"\n\nThe installation exists without a token in the state; replace it (terraform apply -replace) to mint one.")
		return
	}
	plan.Token = types.StringValue(token)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *gatewayInstallationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gatewayInstallationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	inst, err := r.client.GetGatewayInstallation(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		tflog.Info(ctx, "gateway installation not found on the control plane; removing from state",
			map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry gateway installation failed", err.Error())
		return
	}
	gatewayInstallationToModel(inst, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gatewayInstallationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state gatewayInstallationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.apply(ctx, &plan, resp.Diagnostics.AddError) {
		return
	}
	plan.Token = state.Token
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *gatewayInstallationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gatewayInstallationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteGatewayInstallation(ctx, state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry gateway installation failed", err.Error())
	}
}

// ImportState imports an installation by its name. The list endpoint ignores
// ?name=, so the name is matched client-side.
func (r *gatewayInstallationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	installations, err := r.client.ListGatewayInstallations(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Listing TrueFoundry gateway installations failed", err.Error())
		return
	}
	for _, inst := range installations {
		if inst.Name == req.ID {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), inst.ID)...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), inst.Name)...)
			return
		}
	}
	resp.Diagnostics.AddError("Gateway installation not found", fmt.Sprintf("no gateway installation named %q", req.ID))
}

// apply writes the manifest and records the installation's ID in m.
func (r *gatewayInstallationResource) apply(ctx context.Context, m *gatewayInstallationResourceModel, addError func(string, string)) bool {
	manifest := client.GatewayInstallationManifest{
		Type:      gatewayInstallationManifestType,
		Name:      m.Name.ValueString(),
		URL:       m.URL.ValueString(),
		Enabled:   m.Enabled.ValueBool(),
		IsDefault: m.IsDefault.ValueBool(),
	}
	out, err := r.client.Apply(ctx, manifest, false)
	if err != nil {
		addError("Applying TrueFoundry gateway installation manifest failed", err.Error())
		return false
	}
	if out.Data.ID == "" {
		addError("Applying TrueFoundry gateway installation manifest failed", "the control plane returned no installation ID")
		return false
	}
	tflog.Info(ctx, "applied gateway installation manifest", map[string]any{"name": manifest.Name, "id": out.Data.ID})
	m.ID = types.StringValue(out.Data.ID)
	return true
}

func gatewayInstallationToModel(inst *client.GatewayInstallation, m *gatewayInstallationResourceModel) {
	m.ID = types.StringValue(inst.ID)
	m.Name = types.StringValue(inst.Name)
	m.URL = types.StringValue(inst.Manifest.URL)
	m.Enabled = types.BoolValue(inst.Manifest.Enabled)
	m.IsDefault = types.BoolValue(inst.IsDefault)
}
