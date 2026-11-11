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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

var (
	_ resource.Resource                = (*virtualMcpServerResource)(nil)
	_ resource.ResourceWithConfigure   = (*virtualMcpServerResource)(nil)
	_ resource.ResourceWithImportState = (*virtualMcpServerResource)(nil)
)

func newVirtualMcpServerResource() resource.Resource {
	return &virtualMcpServerResource{}
}

// virtualMcpServerResource manages a virtual MCP server ("mcp-server/virtual"
// manifests): a gateway-side aggregation of existing MCP servers into one
// endpoint, optionally restricted to a subset of their tools.
type virtualMcpServerResource struct {
	client *client.Client
}

type virtualMcpServerModel struct {
	ID            types.String            `tfsdk:"id"`
	FQN           types.String            `tfsdk:"fqn"`
	Name          types.String            `tfsdk:"name"`
	Description   types.String            `tfsdk:"description"`
	Servers       []mcpSourceServerModel  `tfsdk:"servers"`
	Collaborators []collaboratorModel     `tfsdk:"collaborators"`
	Tags          map[string]types.String `tfsdk:"tags"`
}

type mcpSourceServerModel struct {
	Name         types.String            `tfsdk:"name"`
	EnabledTools []types.String          `tfsdk:"enabled_tools"`
	AuthHeaders  map[string]types.String `tfsdk:"auth_headers"`
}

func (r *virtualMcpServerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_mcp_server"
}

func (r *virtualMcpServerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a virtual MCP server on the TrueFoundry AI Gateway: an aggregation of existing MCP " +
			"servers exposed as one endpoint, optionally restricted to a subset of their tools. " +
			"Applied as an `mcp-server/virtual` manifest via `PUT /api/svc/v1/apply`; the control plane rejects " +
			"source servers that do not exist. Existing servers can be imported by their FQN, " +
			"e.g. `terraform import truefoundry_virtual_mcp_server.example my-tenant:mcp-server:my-server`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the virtual MCP server.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed:      true,
				Description:   "Fully qualified name of the virtual MCP server, e.g. my-tenant:mcp-server:my-server.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the virtual MCP server (3-73 characters; must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens). Changing it replaces the server.",
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
			"servers": schema.ListNestedAttribute{
				Required:      true,
				Description:   "MCP servers aggregated by this virtual server, referenced by name.",
				Validators:    []validator.List{listvalidator.SizeAtLeast(1)},
				PlanModifiers: []planmodifier.List{orderInsensitive()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name of an existing MCP server on the same tenant, e.g. truefoundry_mcp_server.example.name.",
						},
						"enabled_tools": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Tools of the source server exposed through this virtual server. Omitted means all tools.",
						},
						"auth_headers": schema.MapAttribute{
							Optional:    true,
							Sensitive:   true,
							ElementType: types.StringType,
							Description: "Headers overriding the source server's authentication for requests via this virtual server. " +
								"Values accept TrueFoundry secret references (recommended); the control plane returns them as stored, so drift is detected.",
						},
					},
				},
			},
			"collaborators": collaboratorsAttribute("virtual MCP server", "mcp-server-user or mcp-server-manager"),
			"tags": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Free-form key-value tags on the virtual MCP server.",
			},
		},
	}
}

func (r *virtualMcpServerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *virtualMcpServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan virtualMcpServerModel
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

func (r *virtualMcpServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state virtualMcpServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := consumeImportMark(ctx, req, resp)

	server, found, err := getMcpServer(ctx, r.client, state.FQN.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry virtual MCP server failed", err.Error())
		return
	}
	if !found {
		tflog.Info(ctx, "virtual MCP server not found on the control plane; removing from state",
			map[string]any{"fqn": state.FQN.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if server.Manifest.Type != "mcp-server/virtual" {
		resp.Diagnostics.AddError("Unexpected MCP server type",
			fmt.Sprintf("%s is of type %q; use the matching resource (truefoundry_mcp_server for mcp-server/remote).",
				server.FQN, server.Manifest.Type))
		return
	}

	state.ID = types.StringValue(server.ID)
	state.FQN = types.StringValue(server.FQN)
	state.Name = types.StringValue(server.Manifest.Name)
	state.Description = types.StringValue(server.Manifest.Description)

	// Rebuild the list keeping configured order for entries that still exist
	// and appending unknown remote entries in name order, so pure refreshes
	// stay diff-free while real drift is surfaced.
	remote := map[string]client.McpServerSource{}
	for _, source := range server.Manifest.Servers {
		remote[source.Name] = source
	}
	var servers []mcpSourceServerModel
	for _, s := range state.Servers {
		source, ok := remote[s.Name.ValueString()]
		if !ok {
			continue
		}
		delete(remote, s.Name.ValueString())
		servers = append(servers, mcpSourceToModel(source))
	}
	extra := make([]string, 0, len(remote))
	for name := range remote {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	for _, name := range extra {
		servers = append(servers, mcpSourceToModel(remote[name]))
	}
	state.Servers = servers

	state.Collaborators = refreshCollaborators(state.Collaborators, server.Manifest.Collaborators, imported)
	state.Tags = stringMapToModel(server.Manifest.Tags)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *virtualMcpServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan virtualMcpServerModel
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

func (r *virtualMcpServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state virtualMcpServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, virtualMcpServerManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry virtual MCP server failed", err.Error())
	}
}

// ImportState imports a virtual MCP server by its FQN, e.g. "my-tenant:mcp-server:my-server".
func (r *virtualMcpServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("fqn"), req, resp)
	markImported(ctx, resp)
}

// apply sends the manifest and resolves ID and FQN. The apply response for
// MCP servers carries no ID, so it is read back via the list endpoint.
func (r *virtualMcpServerResource) apply(ctx context.Context, plan *virtualMcpServerModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, virtualMcpServerManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry virtual MCP server manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied virtual MCP server manifest", map[string]any{
		"action": result.Action,
		"fqn":    result.Data.FQN,
	})
	plan.FQN = types.StringValue(result.Data.FQN)
	server, found, err := getMcpServer(ctx, r.client, result.Data.FQN)
	if err != nil {
		diags.AddError("Reading back applied TrueFoundry virtual MCP server failed", err.Error())
		return
	}
	if !found {
		diags.AddError("Reading back applied TrueFoundry virtual MCP server failed",
			fmt.Sprintf("virtual MCP server %s not found after apply", result.Data.FQN))
		return
	}
	plan.ID = types.StringValue(server.ID)
}

func virtualMcpServerManifestFromModel(m *virtualMcpServerModel) client.McpServerManifest {
	manifest := client.McpServerManifest{
		Type:          "mcp-server/virtual",
		Name:          m.Name.ValueString(),
		Description:   m.Description.ValueString(),
		Collaborators: collaboratorsToManifest(m.Collaborators),
	}
	for _, s := range m.Servers {
		source := client.McpServerSource{Name: s.Name.ValueString()}
		for _, tool := range s.EnabledTools {
			source.EnabledTools = append(source.EnabledTools, tool.ValueString())
		}
		if headers := stringMapFromModel(s.AuthHeaders); headers != nil {
			source.AuthData = &client.McpServerHeaderOverride{Type: "header", Headers: headers}
		}
		manifest.Servers = append(manifest.Servers, source)
	}
	manifest.Tags = stringMapFromModel(m.Tags)
	return manifest
}

func mcpSourceToModel(source client.McpServerSource) mcpSourceServerModel {
	m := mcpSourceServerModel{Name: types.StringValue(source.Name)}
	for _, tool := range source.EnabledTools {
		m.EnabledTools = append(m.EnabledTools, types.StringValue(tool))
	}
	if source.AuthData != nil {
		m.AuthHeaders = stringMapToModel(source.AuthData.Headers)
	}
	return m
}
