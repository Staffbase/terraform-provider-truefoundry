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
	"regexp"

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
	_ resource.Resource                = (*secretGroupResource)(nil)
	_ resource.ResourceWithConfigure   = (*secretGroupResource)(nil)
	_ resource.ResourceWithImportState = (*secretGroupResource)(nil)
)

// secretGroupManifestType is the manifest type of a secret group.
const secretGroupManifestType = "secret-group"

// secretStoreFQNRegexp matches a secret-store integration FQN, e.g.
// my-tenant:truefoundry:system-secret-store:secret-store:system-secret-store.
// It catches the common mistake of pointing a secret group at a blob-storage
// or notification-channel integration.
var secretStoreFQNRegexp = regexp.MustCompile(`^[^:]+:[^:]+:[^:]+:secret-store:[^:]+$`)

func newSecretGroupResource() resource.Resource {
	return &secretGroupResource{}
}

// secretGroupResource manages a secret group ("secret-group" manifests) via
// the generic manifest apply/delete endpoints and the secret groups list
// endpoint.
type secretGroupResource struct {
	client *client.Client
}

type secretGroupResourceModel struct {
	ID             types.String        `tfsdk:"id"`
	FQN            types.String        `tfsdk:"fqn"`
	Name           types.String        `tfsdk:"name"`
	IntegrationFQN types.String        `tfsdk:"integration_fqn"`
	Collaborators  []collaboratorModel `tfsdk:"collaborators"`
}

func (r *secretGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret_group"
}

func (r *secretGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry secret group (`secret-group` manifest) — the named bucket that individual " +
			"secrets live in, backed by a secret store such as a `truefoundry_system_secret_store` or a " +
			"`truefoundry_hashicorp_account`. Secrets in the group are referenced as `tfy-secret://…` from every " +
			"credential attribute this provider exposes.\n\n" +
			"**The group resource manages only the group.** Secrets in it are created either with " +
			"`truefoundry_secret` (the value is then stored in the Terraform state) or out of band, which keeps " +
			"secret material out of the state — and means Terraform cannot recreate them. **Destroying or " +
			"renaming a group deletes every secret it holds**, so put " +
			"`lifecycle { prevent_destroy = true }` on groups backing live credentials.\n\n" +
			"Import an existing group by its name: `terraform import truefoundry_secret_group.example system`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the secret group.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed:      true,
				Description:   "Fully qualified name of the group, `<tenant>:<name>`. Secrets in it are addressed as `<tenant>:<name>:<secret>`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true,
				Description: "Name of the secret group (5-50 characters; must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens, no consecutive hyphens), unique within the tenant. " +
					"**Changing it replaces the group, which deletes every secret stored in it** — the secrets are " +
					"created out of band and Terraform cannot recreate them.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(minSecretGroupNameLen, maxSecretGroupNameLen),
					stringvalidator.RegexMatches(secretGroupNameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and single hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"integration_fqn": schema.StringAttribute{
				Required: true,
				Description: "FQN of the secret store integration backing the group, e.g. of a " +
					"`truefoundry_system_secret_store`: my-tenant:truefoundry:system-secret-store:secret-store:system-secret-store. " +
					"A group cannot be moved between stores — the control plane rejects the change rather than " +
					"silently migrating the secrets.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(secretStoreFQNRegexp, "must be a secret-store integration FQN, e.g. my-tenant:truefoundry:my-account:secret-store:my-store"),
				},
			},
			"collaborators": collaboratorsAttribute("secret group", "secret-group-admin, secret-group-manager, secret-group-editor, secret-group-access, or secret-group-viewer"),
		},
	}
}

func (r *secretGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *secretGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan secretGroupResourceModel
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

func (r *secretGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state secretGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := consumeImportMark(ctx, req, resp)

	group, found, err := getSecretGroupByName(ctx, r.client, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry secret group failed", err.Error())
		return
	}
	if !found {
		tflog.Info(ctx, "secret group not found on the control plane; removing from state",
			map[string]any{"name": state.Name.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	secretGroupToModel(group, &state, imported)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *secretGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan secretGroupResourceModel
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

// Delete removes the group and, with it, every secret stored in it.
func (r *secretGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state secretGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, secretGroupManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry secret group failed", err.Error())
	}
}

// ImportState imports a secret group by its name, e.g. "system".
func (r *secretGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
	markImported(ctx, resp)
}

// apply writes the manifest and reads the group back, since the generic apply
// response carries no control plane ID for secret groups.
func (r *secretGroupResource) apply(ctx context.Context, plan *secretGroupResourceModel, diags *diag.Diagnostics) {
	if _, err := r.client.Apply(ctx, secretGroupManifestFromModel(plan), false); err != nil {
		diags.AddError("Applying TrueFoundry secret group manifest failed", err.Error())
		return
	}
	name := plan.Name.ValueString()
	group, found, err := getSecretGroupByName(ctx, r.client, name)
	if err != nil {
		diags.AddError("Reading TrueFoundry secret group after apply failed", err.Error())
		return
	}
	if !found {
		diags.AddError("Applied TrueFoundry secret group not found",
			fmt.Sprintf("secret group %q is missing from the control plane right after applying it", name))
		return
	}
	tflog.Info(ctx, "applied secret group manifest", map[string]any{"name": name, "id": group.ID})
	plan.ID = types.StringValue(group.ID)
	plan.FQN = types.StringValue(group.FQN)
}

// getSecretGroupByName fetches a single secret group by name via the list
// endpoint.
//
// The endpoint honors ?fqn= but ignores ?name= (verified against a real
// control plane: a nonexistent name still returns every group), so unlike
// getMlRepoByName this cannot push the name match server-side. Filtering by
// FQN instead is deliberately not done: the FQN is just "<tenant>:<name>", so
// it selects the same row, it is unknown on import and on the first read
// after a create, and a stale FQN in state would turn a live group into a
// "not found" — which for this resource means a plan that recreates a group
// whose secrets Terraform cannot restore. Groups are few per tenant and the
// page size is 100, so the unfiltered list is a single request either way.
func getSecretGroupByName(ctx context.Context, c *client.Client, name string) (*client.SecretGroup, bool, error) {
	groups, err := c.ListSecretGroups(ctx, "")
	if err != nil {
		return nil, false, err
	}
	for i, g := range groups {
		if g.Name == name {
			return &groups[i], true, nil
		}
	}
	return nil, false, nil
}

func secretGroupToModel(group *client.SecretGroup, m *secretGroupResourceModel, imported bool) {
	m.ID = types.StringValue(group.ID)
	m.FQN = types.StringValue(group.FQN)
	m.Name = types.StringValue(group.Manifest.Name)
	m.IntegrationFQN = types.StringValue(group.Manifest.IntegrationFQN)
	m.Collaborators = refreshCollaborators(m.Collaborators, group.Manifest.Collaborators, imported)
}

func secretGroupManifestFromModel(m *secretGroupResourceModel) client.SecretGroupManifest {
	manifest := client.SecretGroupManifest{
		Type:           secretGroupManifestType,
		Name:           m.Name.ValueString(),
		IntegrationFQN: m.IntegrationFQN.ValueString(),
		Collaborators:  collaboratorsToManifest(m.Collaborators),
	}
	return manifest
}
