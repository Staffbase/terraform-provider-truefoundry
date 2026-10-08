package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
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
	_ resource.Resource                   = (*openapiMcpServerResource)(nil)
	_ resource.ResourceWithConfigure      = (*openapiMcpServerResource)(nil)
	_ resource.ResourceWithValidateConfig = (*openapiMcpServerResource)(nil)
	_ resource.ResourceWithImportState    = (*openapiMcpServerResource)(nil)
)

// openapiSpecSourceTypeValues are the supported OpenAPI spec source kinds.
var openapiSpecSourceTypeValues = []string{"remote", "upload", "inline"}

// openapiToolMethodValues are the HTTP methods an OpenAPI tool can map to.
var openapiToolMethodValues = []string{"get", "post", "put", "patch", "delete", "head"}

func newOpenapiMcpServerResource() resource.Resource {
	return &openapiMcpServerResource{}
}

// openapiMcpServerResource manages an OpenAPI-based MCP server
// ("mcp-server/openapi" manifests): the gateway turns the operations of an
// OpenAPI spec into MCP tools that call the described API.
type openapiMcpServerResource struct {
	client *client.Client
}

type openapiMcpServerModel struct {
	ID                types.String              `tfsdk:"id"`
	FQN               types.String              `tfsdk:"fqn"`
	Name              types.String              `tfsdk:"name"`
	Description       types.String              `tfsdk:"description"`
	URL               types.String              `tfsdk:"url"`
	OpenAPISpecSource *openapiSpecSourceModel   `tfsdk:"openapi_spec_source"`
	EnableAllTools    types.Bool                `tfsdk:"enable_all_tools"`
	Auth              *mcpServerAuthModel       `tfsdk:"auth"`
	ToolSettings      []openapiToolSettingModel `tfsdk:"tool_settings"`
	TLS               *endpointTLSModel         `tfsdk:"tls"`
	AdditionalHeaders map[string]types.String   `tfsdk:"additional_headers"`
	Collaborators     []collaboratorModel       `tfsdk:"collaborators"`
	Tags              map[string]types.String   `tfsdk:"tags"`
}

type openapiSpecSourceModel struct {
	Type        types.String `tfsdk:"type"`
	URL         types.String `tfsdk:"url"`
	OpenAPISpec types.String `tfsdk:"openapi_spec"`
}

type openapiToolSettingModel struct {
	Method      types.String             `tfsdk:"method"`
	Path        types.String             `tfsdk:"path"`
	Name        types.String             `tfsdk:"name"`
	Description types.String             `tfsdk:"description"`
	Annotations *mcpToolAnnotationsModel `tfsdk:"annotations"`
}

func (r *openapiMcpServerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_openapi_mcp_server"
}

func (r *openapiMcpServerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an OpenAPI-based MCP server on the TrueFoundry AI Gateway: the gateway exposes the " +
			"operations of an OpenAPI spec as MCP tools calling the described API. Applied as an " +
			"`mcp-server/openapi` manifest via `PUT /api/svc/v1/apply`. Existing servers can be imported by their " +
			"FQN, e.g. `terraform import truefoundry_openapi_mcp_server.example my-tenant:mcp-server:my-server`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the MCP server.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed:      true,
				Description:   "Fully qualified name of the MCP server, e.g. my-tenant:mcp-server:my-server.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the MCP server (3-73 characters; must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens). Changing it replaces the server.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(minNameLen, maxMcpServerNameLen),
					stringvalidator.RegexMatches(nameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{
				Required:    true,
				Description: "Human-readable description shown to gateway users and AI agents.",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"url": schema.StringAttribute{
				Required:    true,
				Description: "Base URL of the API described by the OpenAPI spec, e.g. https://api.example.com.",
			},
			"openapi_spec_source": schema.SingleNestedAttribute{
				Required: true,
				Description: "Where the OpenAPI spec comes from. `type` selects the kind: `remote` and `upload` " +
					"take a `url`, `inline` takes the spec itself in `openapi_spec`.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required: true,
						Description: "Spec source kind: `remote` (fetched from a URL), `upload` (a spec previously " +
							"uploaded to TrueFoundry-managed storage), or `inline` (the spec in `openapi_spec`).",
						Validators: []validator.String{stringvalidator.OneOf(openapiSpecSourceTypeValues...)},
					},
					"url": schema.StringAttribute{
						Optional:    true,
						Description: "URL of the OpenAPI spec (types `remote` and `upload`).",
					},
					"openapi_spec": schema.StringAttribute{
						Optional:    true,
						Description: "The OpenAPI spec as a JSON or YAML string, e.g. via jsonencode() or file() (type `inline`).",
					},
				},
			},
			"enable_all_tools": schema.BoolAttribute{
				Optional:    true,
				Description: "Expose every operation of the spec as a tool instead of only those listed in `tool_settings`.",
			},
			"auth": mcpAuthSchemaAttribute(),
			"tool_settings": schema.ListNestedAttribute{
				Optional:      true,
				Description:   "Operations of the OpenAPI spec exposed as MCP tools, addressed by HTTP method and path.",
				Validators:    []validator.List{listvalidator.SizeAtLeast(1)},
				PlanModifiers: []planmodifier.List{orderInsensitive()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"method": schema.StringAttribute{
							Required:    true,
							Description: "HTTP method of the operation: get, post, put, patch, delete, or head.",
							Validators:  []validator.String{stringvalidator.OneOf(openapiToolMethodValues...)},
						},
						"path": schema.StringAttribute{
							Required:    true,
							Description: "Path of the operation in the spec, e.g. /v1/users/{id}.",
						},
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Tool name shown to gateway users and AI agents.",
						},
						"description": schema.StringAttribute{
							Required:    true,
							Description: "Tool description shown to AI agents.",
						},
						"annotations": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Behavior hints for this tool.",
							Attributes: map[string]schema.Attribute{
								"read_only_hint": schema.BoolAttribute{
									Optional:    true,
									Description: "The tool does not modify its environment.",
								},
								"destructive_hint": schema.BoolAttribute{
									Optional:    true,
									Description: "The tool may perform destructive updates.",
								},
							},
						},
					},
				},
			},
			"tls": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "TLS verification settings for the connection to the API.",
				Attributes: map[string]schema.Attribute{
					"reject_unauthorized": schema.BoolAttribute{
						Required:    true,
						Description: "Reject connections not authorized by the configured CAs.",
					},
					"ca_cert": schema.StringAttribute{
						Optional:    true,
						Description: "Custom CA certificate(s) used for TLS verification.",
					},
				},
			},
			"additional_headers": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Non-secret custom headers sent to the API with every request.",
			},
			"collaborators": collaboratorsAttribute("MCP server", "mcp-server-user or mcp-server-manager"),
			"tags": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Free-form key-value tags on the MCP server.",
			},
		},
	}
}

func (r *openapiMcpServerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the per-spec-source-type and per-auth-type field
// combinations.
func (r *openapiMcpServerResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config openapiMcpServerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if source := config.OpenAPISpecSource; source != nil {
		sourcePath := path.Root("openapi_spec_source")
		switch source.Type.ValueString() {
		case "remote", "upload":
			if source.URL.IsNull() {
				resp.Diagnostics.AddAttributeError(sourcePath.AtName("url"), "Invalid OpenAPI spec source",
					fmt.Sprintf("`url` is required for spec source type %q.", source.Type.ValueString()))
			}
			if !source.OpenAPISpec.IsNull() {
				resp.Diagnostics.AddAttributeError(sourcePath.AtName("openapi_spec"), "Invalid OpenAPI spec source",
					fmt.Sprintf("`openapi_spec` must not be set when spec source type is %q.", source.Type.ValueString()))
			}
		case "inline":
			if source.OpenAPISpec.IsNull() {
				resp.Diagnostics.AddAttributeError(sourcePath.AtName("openapi_spec"), "Invalid OpenAPI spec source",
					"`openapi_spec` is required for spec source type \"inline\".")
			}
			if !source.URL.IsNull() {
				resp.Diagnostics.AddAttributeError(sourcePath.AtName("url"), "Invalid OpenAPI spec source",
					"`url` must not be set when spec source type is \"inline\".")
			}
		}
	}

	validateMcpAuthConfig(config.Auth, &resp.Diagnostics)
}

func (r *openapiMcpServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan openapiMcpServerModel
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

func (r *openapiMcpServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state openapiMcpServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := consumeImportMark(ctx, req, resp)

	server, found, err := getMcpServer(ctx, r.client, state.FQN.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry OpenAPI MCP server failed", err.Error())
		return
	}
	if !found {
		tflog.Info(ctx, "OpenAPI MCP server not found on the control plane; removing from state",
			map[string]any{"fqn": state.FQN.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if server.Manifest.Type != "mcp-server/openapi" {
		resp.Diagnostics.AddError("Unexpected MCP server type",
			fmt.Sprintf("%s is of type %q; use the matching resource (truefoundry_mcp_server for mcp-server/remote, truefoundry_virtual_mcp_server for mcp-server/virtual).",
				server.FQN, server.Manifest.Type))
		return
	}

	state.ID = types.StringValue(server.ID)
	state.FQN = types.StringValue(server.FQN)
	state.Name = types.StringValue(server.Manifest.Name)
	state.Description = types.StringValue(server.Manifest.Description)
	state.URL = types.StringValue(server.Manifest.URL)
	if source := server.Manifest.OpenAPISpecSource; source != nil {
		state.OpenAPISpecSource = &openapiSpecSourceModel{
			Type:        types.StringValue(source.Type),
			URL:         stringOrNull(source.URL),
			OpenAPISpec: stringOrNull(source.OpenAPISpec),
		}
	} else {
		state.OpenAPISpecSource = nil
	}
	state.EnableAllTools = boolOrNull(server.Manifest.EnableAllTools)
	state.Auth = mcpAuthToModel(server.Manifest.AuthData)
	state.ToolSettings = nil
	for _, setting := range server.Manifest.ToolSettings {
		state.ToolSettings = append(state.ToolSettings, openapiToolSettingToModel(setting))
	}
	if server.Manifest.TLSSettings != nil {
		state.TLS = &endpointTLSModel{
			RejectUnauthorized: types.BoolValue(server.Manifest.TLSSettings.RejectUnauthorized),
			CACert:             stringOrNull(server.Manifest.TLSSettings.CACert),
		}
	} else {
		state.TLS = nil
	}
	state.AdditionalHeaders = stringMapToModel(server.Manifest.AdditionalHeaders)
	state.Collaborators = refreshCollaborators(state.Collaborators, server.Manifest.Collaborators, imported)
	state.Tags = stringMapToModel(server.Manifest.Tags)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *openapiMcpServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan openapiMcpServerModel
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

func (r *openapiMcpServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state openapiMcpServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, openapiMcpServerManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry OpenAPI MCP server failed", err.Error())
	}
}

// ImportState imports an OpenAPI MCP server by its FQN, e.g. "my-tenant:mcp-server:my-server".
func (r *openapiMcpServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("fqn"), req, resp)
	markImported(ctx, resp)
}

// apply sends the manifest and resolves ID and FQN. The apply response for
// MCP servers carries no ID, so it is read back via the list endpoint.
func (r *openapiMcpServerResource) apply(ctx context.Context, plan *openapiMcpServerModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, openapiMcpServerManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry OpenAPI MCP server manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied OpenAPI MCP server manifest", map[string]any{
		"action": result.Action,
		"fqn":    result.Data.FQN,
	})
	plan.FQN = types.StringValue(result.Data.FQN)
	server, found, err := getMcpServer(ctx, r.client, result.Data.FQN)
	if err != nil {
		diags.AddError("Reading back applied TrueFoundry OpenAPI MCP server failed", err.Error())
		return
	}
	if !found {
		diags.AddError("Reading back applied TrueFoundry OpenAPI MCP server failed",
			fmt.Sprintf("OpenAPI MCP server %s not found after apply", result.Data.FQN))
		return
	}
	plan.ID = types.StringValue(server.ID)
}

func openapiMcpServerManifestFromModel(m *openapiMcpServerModel) client.McpServerManifest {
	manifest := client.McpServerManifest{
		Type:          "mcp-server/openapi",
		Name:          m.Name.ValueString(),
		Description:   m.Description.ValueString(),
		URL:           m.URL.ValueString(),
		AuthData:      mcpAuthFromModel(m.Auth),
		Collaborators: collaboratorsToManifest(m.Collaborators),
	}
	if m.OpenAPISpecSource != nil {
		manifest.OpenAPISpecSource = &client.OpenApiSpecSource{
			Type:        m.OpenAPISpecSource.Type.ValueString(),
			URL:         m.OpenAPISpecSource.URL.ValueString(),
			OpenAPISpec: m.OpenAPISpecSource.OpenAPISpec.ValueString(),
		}
	}
	if !m.EnableAllTools.IsNull() {
		v := m.EnableAllTools.ValueBool()
		manifest.EnableAllTools = &v
	}
	for _, setting := range m.ToolSettings {
		manifest.ToolSettings = append(manifest.ToolSettings, openapiToolSettingFromModel(setting))
	}
	if m.TLS != nil {
		manifest.TLSSettings = &client.TLSSettings{
			RejectUnauthorized: m.TLS.RejectUnauthorized.ValueBool(),
			CACert:             m.TLS.CACert.ValueString(),
		}
	}
	manifest.AdditionalHeaders = stringMapFromModel(m.AdditionalHeaders)
	manifest.Tags = stringMapFromModel(m.Tags)
	return manifest
}

func openapiToolSettingFromModel(m openapiToolSettingModel) client.McpToolSetting {
	setting := client.McpToolSetting{
		Method:      m.Method.ValueString(),
		Path:        m.Path.ValueString(),
		Name:        m.Name.ValueString(),
		Description: m.Description.ValueString(),
	}
	if m.Annotations != nil {
		setting.Annotations = &client.McpToolAnnotations{}
		if !m.Annotations.ReadOnlyHint.IsNull() {
			v := m.Annotations.ReadOnlyHint.ValueBool()
			setting.Annotations.ReadOnlyHint = &v
		}
		if !m.Annotations.DestructiveHint.IsNull() {
			v := m.Annotations.DestructiveHint.ValueBool()
			setting.Annotations.DestructiveHint = &v
		}
	}
	return setting
}

func openapiToolSettingToModel(setting client.McpToolSetting) openapiToolSettingModel {
	m := openapiToolSettingModel{
		Method:      types.StringValue(setting.Method),
		Path:        types.StringValue(setting.Path),
		Name:        types.StringValue(setting.Name),
		Description: types.StringValue(setting.Description),
	}
	if setting.Annotations != nil {
		m.Annotations = &mcpToolAnnotationsModel{
			ReadOnlyHint:    boolOrNull(setting.Annotations.ReadOnlyHint),
			DestructiveHint: boolOrNull(setting.Annotations.DestructiveHint),
		}
	}
	return m
}
