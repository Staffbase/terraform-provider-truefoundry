package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
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
	_ resource.Resource                   = (*mcpServerResource)(nil)
	_ resource.ResourceWithConfigure      = (*mcpServerResource)(nil)
	_ resource.ResourceWithValidateConfig = (*mcpServerResource)(nil)
	_ resource.ResourceWithImportState    = (*mcpServerResource)(nil)
)

// mcpAuthTypeValues are the supported MCP server authentication modes.
var mcpAuthTypeValues = []string{"header", "oauth2", "passthrough"}

func newMcpServerResource() resource.Resource {
	return &mcpServerResource{}
}

// mcpServerResource manages a remote MCP server registered on the AI Gateway
// ("mcp-server/remote" manifests) via the manifest-based apply/delete API and
// the dedicated GET /api/svc/v1/mcp-servers read endpoint.
type mcpServerResource struct {
	client *client.Client
}

type mcpServerModel struct {
	ID                types.String            `tfsdk:"id"`
	FQN               types.String            `tfsdk:"fqn"`
	Name              types.String            `tfsdk:"name"`
	Description       types.String            `tfsdk:"description"`
	URL               types.String            `tfsdk:"url"`
	Auth              *mcpServerAuthModel     `tfsdk:"auth"`
	ToolSettings      []mcpToolSettingModel   `tfsdk:"tool_settings"`
	TLS               *endpointTLSModel       `tfsdk:"tls"`
	AdditionalHeaders map[string]types.String `tfsdk:"additional_headers"`
	Collaborators     []collaboratorModel     `tfsdk:"collaborators"`
	Tags              map[string]types.String `tfsdk:"tags"`
}

type mcpServerAuthModel struct {
	Type types.String `tfsdk:"type"`

	Headers   map[string]types.String `tfsdk:"headers"`
	AuthLevel types.String            `tfsdk:"auth_level"`

	GrantType             types.String            `tfsdk:"grant_type"`
	AuthorizationURL      types.String            `tfsdk:"authorization_url"`
	TokenURL              types.String            `tfsdk:"token_url"`
	ClientID              types.String            `tfsdk:"client_id"`
	ClientSecret          types.String            `tfsdk:"client_secret"`
	RegistrationURL       types.String            `tfsdk:"registration_url"`
	IntrospectionURL      types.String            `tfsdk:"introspection_url"`
	Provider              types.String            `tfsdk:"provider"`
	JWTSource             types.String            `tfsdk:"jwt_source"`
	Scopes                []types.String          `tfsdk:"scopes"`
	AdditionalTokenParams map[string]types.String `tfsdk:"additional_token_params"`
	PKCEMethods           []types.String          `tfsdk:"code_challenge_methods_supported"`
}

type mcpToolSettingModel struct {
	Name        types.String             `tfsdk:"name"`
	Description types.String             `tfsdk:"description"`
	Disabled    types.Bool               `tfsdk:"disabled"`
	Annotations *mcpToolAnnotationsModel `tfsdk:"annotations"`
}

type mcpToolAnnotationsModel struct {
	ReadOnlyHint    types.Bool `tfsdk:"read_only_hint"`
	DestructiveHint types.Bool `tfsdk:"destructive_hint"`
}

func (r *mcpServerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_server"
}

func (r *mcpServerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a remote MCP server registered on the TrueFoundry AI Gateway, proxied at " +
			"{gateway}/{tenant}/mcp/{name}/server. Applied as an `mcp-server/remote` manifest via `PUT /api/svc/v1/apply`. " +
			"For OpenAPI-based MCP servers use `truefoundry_openapi_mcp_server`; TrueFoundry-managed and stdio servers are not supported. " +
			"Existing servers can be imported by their FQN, e.g. `terraform import truefoundry_mcp_server.example my-tenant:mcp-server:my-server`.",
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
				Description: "URL of the MCP server endpoint, e.g. https://mcp.example.com/mcp.",
			},
			"auth": mcpAuthSchemaAttribute(),
			"tool_settings": schema.ListNestedAttribute{
				Optional:      true,
				Description:   "Per-tool overrides for the tools the MCP server exposes.",
				Validators:    []validator.List{listvalidator.SizeAtLeast(1)},
				PlanModifiers: []planmodifier.List{orderInsensitive()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name of the tool as exposed by the MCP server.",
						},
						"description": schema.StringAttribute{
							Optional:    true,
							Description: "Replacement description shown to AI agents instead of the tool's own.",
						},
						"disabled": schema.BoolAttribute{
							Optional:    true,
							Description: "Hide this tool from gateway users.",
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
				Description: "TLS verification settings for the connection to the MCP server.",
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
				Description: "Non-secret custom headers sent to the MCP server with every request.",
			},
			"collaborators": schema.SetNestedAttribute{
				Optional:    true,
				Description: "Subjects with a role on this MCP server.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"subject": schema.StringAttribute{
							Required:    true,
							Description: "Subject FQN, e.g. user:jane@example.com or team:everyone.",
						},
						"role_id": schema.StringAttribute{
							Required:    true,
							Description: "Role to assign, e.g. mcp-server-user or mcp-server-manager.",
						},
					},
				},
			},
			"tags": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Free-form key-value tags on the MCP server.",
			},
		},
	}
}

func (r *mcpServerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the per-auth-type field combinations.
func (r *mcpServerResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config mcpServerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateMcpAuthConfig(config.Auth, &resp.Diagnostics)
}

// mcpAuthSchemaAttribute is the `auth` attribute shared by the remote and
// OpenAPI MCP server resources.
func mcpAuthSchemaAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional: true,
		Description: "How the gateway authenticates against the MCP server. Omitted means no authentication. " +
			"`type` selects the mode; the other fields belong to one mode each.",
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				Required:    true,
				Description: "Authentication mode: `header` (static headers), `oauth2`, or `passthrough` (forward the caller's Authorization header).",
				Validators:  []validator.String{stringvalidator.OneOf(mcpAuthTypeValues...)},
			},
			"headers": schema.MapAttribute{
				Optional:    true,
				Sensitive:   true,
				ElementType: types.StringType,
				Description: "Headers sent to the MCP server, e.g. {\"Authorization\" = \"Bearer ...\"} (type `header`). " +
					"Values accept TrueFoundry secret references (recommended); the control plane returns them as stored, so drift is detected.",
			},
			"auth_level": schema.StringAttribute{
				Optional:    true,
				Description: "Whether the headers apply for all users (`global`) or each user supplies their own (`per_user`) (type `header`).",
				Validators:  []validator.String{stringvalidator.OneOf("global", "per_user")},
			},
			"grant_type": schema.StringAttribute{
				Optional:    true,
				Description: "OAuth2 grant: `authorization_code` or `client_credentials` (type `oauth2`).",
				Validators:  []validator.String{stringvalidator.OneOf("authorization_code", "client_credentials")},
			},
			"authorization_url": schema.StringAttribute{
				Optional:    true,
				Description: "OAuth2 authorization endpoint (type `oauth2`, authorization_code grant).",
			},
			"token_url": schema.StringAttribute{
				Optional:    true,
				Description: "OAuth2 token endpoint (type `oauth2`).",
			},
			"client_id": schema.StringAttribute{
				Optional:    true,
				Description: "OAuth2 client ID; accepts a TrueFoundry secret reference (type `oauth2`).",
			},
			"client_secret": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "OAuth2 client secret. A TrueFoundry secret reference is recommended; the control plane returns it as stored, so drift is detected (type `oauth2`).",
			},
			"registration_url": schema.StringAttribute{
				Optional:    true,
				Description: "OAuth2 dynamic client registration endpoint (type `oauth2`).",
			},
			"introspection_url": schema.StringAttribute{
				Optional:    true,
				Description: "OAuth2 token introspection endpoint (type `oauth2`).",
			},
			"provider": schema.StringAttribute{
				Optional:    true,
				Description: "OAuth2 provider: `custom`, `auth0`, or `okta` (type `oauth2`).",
				Validators:  []validator.String{stringvalidator.OneOf("custom", "auth0", "okta")},
			},
			"jwt_source": schema.StringAttribute{
				Optional:    true,
				Description: "Which token the gateway forwards: `access_token` or `id_token` (type `oauth2`).",
				Validators:  []validator.String{stringvalidator.OneOf("access_token", "id_token")},
			},
			"scopes": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "OAuth2 scopes to request (type `oauth2`).",
			},
			"additional_token_params": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Extra parameters sent with the token request (type `oauth2`).",
			},
			"code_challenge_methods_supported": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "PKCE code challenge methods the authorization server supports; only `S256` is allowed (type `oauth2`).",
				Validators:  []validator.Set{setvalidator.ValueStringsAre(stringvalidator.OneOf("S256"))},
			},
		},
	}
}

// validateMcpAuthConfig enforces the per-auth-type field combinations of the
// `auth` block shared by the remote and OpenAPI MCP server resources.
func validateMcpAuthConfig(auth *mcpServerAuthModel, diags *diag.Diagnostics) {
	if auth == nil {
		return
	}

	authPath := path.Root("auth")
	headerFields := map[string]bool{"headers": len(auth.Headers) > 0, "auth_level": !auth.AuthLevel.IsNull()}
	oauthFields := map[string]bool{
		"grant_type": !auth.GrantType.IsNull(), "authorization_url": !auth.AuthorizationURL.IsNull(),
		"token_url": !auth.TokenURL.IsNull(), "client_id": !auth.ClientID.IsNull(),
		"client_secret": !auth.ClientSecret.IsNull(), "registration_url": !auth.RegistrationURL.IsNull(),
		"introspection_url": !auth.IntrospectionURL.IsNull(), "provider": !auth.Provider.IsNull(),
		"jwt_source": !auth.JWTSource.IsNull(), "scopes": len(auth.Scopes) > 0,
		"additional_token_params": len(auth.AdditionalTokenParams) > 0, "code_challenge_methods_supported": len(auth.PKCEMethods) > 0,
	}
	forbid := func(fields map[string]bool, authType string) {
		names := make([]string, 0, len(fields))
		for name, set := range fields {
			if set {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			diags.AddAttributeError(authPath.AtName(name), "Invalid MCP server auth configuration",
				fmt.Sprintf("`%s` must not be set when auth type is %q.", name, authType))
		}
	}

	switch auth.Type.ValueString() {
	case "header":
		if len(auth.Headers) == 0 {
			diags.AddAttributeError(authPath.AtName("headers"), "Invalid MCP server auth configuration",
				"`headers` is required for auth type \"header\".")
		}
		if auth.AuthLevel.IsNull() {
			diags.AddAttributeError(authPath.AtName("auth_level"), "Invalid MCP server auth configuration",
				"`auth_level` is required for auth type \"header\".")
		}
		forbid(oauthFields, "header")
	case "oauth2":
		for _, required := range []struct {
			name string
			set  bool
		}{
			{"token_url", !auth.TokenURL.IsNull()},
			{"grant_type", !auth.GrantType.IsNull()},
			{"provider", !auth.Provider.IsNull()},
			{"jwt_source", !auth.JWTSource.IsNull()},
		} {
			if !required.set {
				diags.AddAttributeError(authPath.AtName(required.name), "Invalid MCP server auth configuration",
					fmt.Sprintf("`%s` is required for auth type \"oauth2\".", required.name))
			}
		}
		forbid(headerFields, "oauth2")
	case "passthrough":
		forbid(headerFields, "passthrough")
		forbid(oauthFields, "passthrough")
	}
}

func (r *mcpServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan mcpServerModel
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

func (r *mcpServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state mcpServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	server, found, err := getMcpServer(ctx, r.client, state.FQN.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry MCP server failed", err.Error())
		return
	}
	if !found {
		tflog.Info(ctx, "MCP server not found on the control plane; removing from state",
			map[string]any{"fqn": state.FQN.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if server.Manifest.Type != "mcp-server/remote" {
		resp.Diagnostics.AddError("Unexpected MCP server type",
			fmt.Sprintf("%s is of type %q; use the matching resource (truefoundry_openapi_mcp_server for mcp-server/openapi, truefoundry_virtual_mcp_server for mcp-server/virtual).",
				server.FQN, server.Manifest.Type))
		return
	}

	state.ID = types.StringValue(server.ID)
	state.FQN = types.StringValue(server.FQN)
	state.Name = types.StringValue(server.Manifest.Name)
	state.Description = types.StringValue(server.Manifest.Description)
	state.URL = types.StringValue(server.Manifest.URL)
	state.Auth = mcpAuthToModel(server.Manifest.AuthData)
	state.ToolSettings = nil
	for _, setting := range server.Manifest.ToolSettings {
		state.ToolSettings = append(state.ToolSettings, mcpToolSettingToModel(setting))
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
	state.Collaborators = nil
	for _, c := range server.Manifest.Collaborators {
		state.Collaborators = append(state.Collaborators, collaboratorModel{
			Subject: types.StringValue(c.Subject),
			RoleID:  types.StringValue(c.RoleID),
		})
	}
	state.Tags = stringMapToModel(server.Manifest.Tags)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *mcpServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan mcpServerModel
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

func (r *mcpServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state mcpServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, mcpServerManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry MCP server failed", err.Error())
	}
}

// ImportState imports an MCP server by its FQN, e.g. "my-tenant:mcp-server:my-server".
func (r *mcpServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("fqn"), req, resp)
}

// apply sends the manifest and resolves ID and FQN. The apply response for
// MCP servers carries no ID, so it is read back via the list endpoint.
func (r *mcpServerResource) apply(ctx context.Context, plan *mcpServerModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, mcpServerManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry MCP server manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied MCP server manifest", map[string]any{
		"action": result.Action,
		"fqn":    result.Data.FQN,
	})
	plan.FQN = types.StringValue(result.Data.FQN)
	server, found, err := getMcpServer(ctx, r.client, result.Data.FQN)
	if err != nil {
		diags.AddError("Reading back applied TrueFoundry MCP server failed", err.Error())
		return
	}
	if !found {
		diags.AddError("Reading back applied TrueFoundry MCP server failed",
			fmt.Sprintf("MCP server %s not found after apply", result.Data.FQN))
		return
	}
	plan.ID = types.StringValue(server.ID)
}

// getMcpServer fetches a single MCP server by FQN via the list endpoint.
func getMcpServer(ctx context.Context, c *client.Client, fqn string) (*client.McpServer, bool, error) {
	servers, err := c.ListMcpServers(ctx, fqn)
	if err != nil {
		return nil, false, err
	}
	if len(servers) == 0 {
		return nil, false, nil
	}
	return &servers[0], true, nil
}

func mcpServerManifestFromModel(m *mcpServerModel) client.McpServerManifest {
	manifest := client.McpServerManifest{
		Type:          "mcp-server/remote",
		Name:          m.Name.ValueString(),
		Description:   m.Description.ValueString(),
		URL:           m.URL.ValueString(),
		AuthData:      mcpAuthFromModel(m.Auth),
		Collaborators: []client.Collaborator{},
	}
	for _, setting := range m.ToolSettings {
		manifest.ToolSettings = append(manifest.ToolSettings, mcpToolSettingFromModel(setting))
	}
	if m.TLS != nil {
		manifest.TLSSettings = &client.TLSSettings{
			RejectUnauthorized: m.TLS.RejectUnauthorized.ValueBool(),
			CACert:             m.TLS.CACert.ValueString(),
		}
	}
	manifest.AdditionalHeaders = stringMapFromModel(m.AdditionalHeaders)
	for _, c := range m.Collaborators {
		manifest.Collaborators = append(manifest.Collaborators, client.Collaborator{
			Subject: c.Subject.ValueString(),
			RoleID:  c.RoleID.ValueString(),
		})
	}
	manifest.Tags = stringMapFromModel(m.Tags)
	return manifest
}

func mcpAuthFromModel(m *mcpServerAuthModel) *client.McpServerAuth {
	if m == nil {
		return nil
	}
	auth := &client.McpServerAuth{
		Type:             m.Type.ValueString(),
		AuthLevel:        m.AuthLevel.ValueString(),
		GrantType:        m.GrantType.ValueString(),
		AuthorizationURL: m.AuthorizationURL.ValueString(),
		TokenURL:         m.TokenURL.ValueString(),
		ClientID:         m.ClientID.ValueString(),
		ClientSecret:     m.ClientSecret.ValueString(),
		RegistrationURL:  m.RegistrationURL.ValueString(),
		IntrospectionURL: m.IntrospectionURL.ValueString(),
		Provider:         m.Provider.ValueString(),
		JWTSource:        m.JWTSource.ValueString(),
	}
	auth.Headers = stringMapFromModel(m.Headers)
	for _, s := range m.Scopes {
		auth.Scopes = append(auth.Scopes, s.ValueString())
	}
	auth.AdditionalTokenParams = stringMapFromModel(m.AdditionalTokenParams)
	for _, s := range m.PKCEMethods {
		auth.CodeChallengeMethodsSupported = append(auth.CodeChallengeMethodsSupported, s.ValueString())
	}
	return auth
}

func mcpAuthToModel(auth *client.McpServerAuth) *mcpServerAuthModel {
	if auth == nil {
		return nil
	}
	m := &mcpServerAuthModel{
		Type:             types.StringValue(auth.Type),
		AuthLevel:        stringOrNull(auth.AuthLevel),
		GrantType:        stringOrNull(auth.GrantType),
		AuthorizationURL: stringOrNull(auth.AuthorizationURL),
		TokenURL:         stringOrNull(auth.TokenURL),
		ClientID:         stringOrNull(auth.ClientID),
		ClientSecret:     stringOrNull(auth.ClientSecret),
		RegistrationURL:  stringOrNull(auth.RegistrationURL),
		IntrospectionURL: stringOrNull(auth.IntrospectionURL),
		Provider:         stringOrNull(auth.Provider),
		JWTSource:        stringOrNull(auth.JWTSource),
	}
	m.Headers = stringMapToModel(auth.Headers)
	for _, s := range auth.Scopes {
		m.Scopes = append(m.Scopes, types.StringValue(s))
	}
	m.AdditionalTokenParams = stringMapToModel(auth.AdditionalTokenParams)
	for _, s := range auth.CodeChallengeMethodsSupported {
		m.PKCEMethods = append(m.PKCEMethods, types.StringValue(s))
	}
	return m
}

func mcpToolSettingFromModel(m mcpToolSettingModel) client.McpToolSetting {
	setting := client.McpToolSetting{
		Name:        m.Name.ValueString(),
		Description: m.Description.ValueString(),
	}
	if !m.Disabled.IsNull() {
		v := m.Disabled.ValueBool()
		setting.Disabled = &v
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

func mcpToolSettingToModel(setting client.McpToolSetting) mcpToolSettingModel {
	m := mcpToolSettingModel{
		Name:        types.StringValue(setting.Name),
		Description: stringOrNull(setting.Description),
	}
	if setting.Disabled != nil {
		m.Disabled = types.BoolValue(*setting.Disabled)
	} else {
		m.Disabled = types.BoolNull()
	}
	if setting.Annotations != nil {
		m.Annotations = &mcpToolAnnotationsModel{
			ReadOnlyHint:    boolOrNull(setting.Annotations.ReadOnlyHint),
			DestructiveHint: boolOrNull(setting.Annotations.DestructiveHint),
		}
	}
	return m
}

// stringMapFromModel converts a model map to a wire map, nil when empty.
func stringMapFromModel(m map[string]types.String) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range m {
		out[k] = v.ValueString()
	}
	return out
}

// stringMapToModel converts a wire map to a model map, nil when empty.
func stringMapToModel(m map[string]string) map[string]types.String {
	if len(m) == 0 {
		return nil
	}
	out := map[string]types.String{}
	for k, v := range m {
		out[k] = types.StringValue(v)
	}
	return out
}

// boolOrNull maps a nil pointer to a null bool.
func boolOrNull(v *bool) types.Bool {
	if v == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*v)
}
