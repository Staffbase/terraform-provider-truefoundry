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
	_ resource.Resource                = (*azureAccountResource)(nil)
	_ resource.ResourceWithConfigure   = (*azureAccountResource)(nil)
	_ resource.ResourceWithImportState = (*azureAccountResource)(nil)
)

func newAzureAccountResource() resource.Resource {
	return &azureAccountResource{}
}

// azureAccountResource manages an Azure provider account carrying blob
// storage integrations via the manifest-based apply/delete API.
type azureAccountResource struct {
	client *client.Client
}

type azureAccountModel struct {
	ID             types.String       `tfsdk:"id"`
	FQN            types.String       `tfsdk:"fqn"`
	Name           types.String       `tfsdk:"name"`
	SubscriptionID types.String       `tfsdk:"subscription_id"`
	BlobStorages   []blobStorageModel `tfsdk:"blob_storages"`
}

type blobStorageModel struct {
	Name               types.String   `tfsdk:"name"`
	StorageRoot        types.String   `tfsdk:"storage_root"`
	ConnectionString   types.String   `tfsdk:"connection_string"`
	AuthorizedSubjects []types.String `tfsdk:"authorized_subjects"`
}

func (r *azureAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_azure_account"
}

func (r *azureAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry Azure provider account carrying blob storage integrations, " +
			"applied as a `provider-account/azure` manifest via `PUT /api/svc/v1/apply`. " +
			"Azure accounts can also hold AKS, container registry, key vault, and repos integrations — " +
			"those are not modeled; do not manage a pre-existing account that uses them, an apply would strip them. " +
			"Existing accounts can be imported by their FQN, e.g. `terraform import truefoundry_azure_account.example my-tenant:azure:my-account`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the Azure account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed:      true,
				Description:   "Fully qualified name of the Azure account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the Azure account (3-36 characters; must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens). Changing it replaces the account.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(minNameLen, maxWideAccountNameLen),
					stringvalidator.RegexMatches(nameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"subscription_id": schema.StringAttribute{
				Optional:    true,
				Description: "Azure subscription ID the account belongs to.",
			},
			"blob_storages": schema.ListNestedAttribute{
				Required:      true,
				Description:   "Azure Blob Storage containers accessible through this account.",
				Validators:    []validator.List{listvalidator.SizeAtLeast(1)},
				PlanModifiers: []planmodifier.List{orderInsensitive()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name identifying this blob storage integration.",
							Validators: []validator.String{
								stringvalidator.LengthBetween(1, 64),
							},
						},
						"storage_root": schema.StringAttribute{
							Required:    true,
							Description: "Root URL of the storage, e.g. https://myaccount.blob.core.windows.net/mycontainer/some/prefix.",
						},
						"connection_string": schema.StringAttribute{
							Optional:  true,
							Sensitive: true,
							Description: "Azure storage connection string. Accepts TrueFoundry secret references (recommended); the control plane returns it as stored, so drift is detected. " +
								"The control plane verifies credentials at apply time — omitting the connection string only works if the control plane itself has an Azure identity with access to the storage.",
						},
						"authorized_subjects": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Subjects allowed to use this blob storage, as `<user_type>:<username>` FQNs.",
						},
					},
				},
			},
		},
	}
}

func (r *azureAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *azureAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan azureAccountModel
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

func (r *azureAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state azureAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	integrations, err := r.client.ListBlobStorageIntegrations(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry blob storage integrations failed", err.Error())
		return
	}

	var account *client.EmbeddedProviderAccount
	remote := map[string]client.AzureBlobStorage{}
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
		tflog.Info(ctx, "azure account has no blob storage integrations on the control plane; removing from state",
			map[string]any{"fqn": state.FQN.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	if account != nil {
		state.ID = types.StringValue(account.ID)
		state.Name = types.StringValue(account.Manifest.Name)
		state.SubscriptionID = stringOrNull(account.Manifest.SubscriptionID)
	}

	// Rebuild the list keeping configured order for entries that still exist
	// and appending unknown remote entries in name order, so pure refreshes
	// stay diff-free while real drift is surfaced.
	var storages []blobStorageModel
	for _, s := range state.BlobStorages {
		manifest, ok := remote[s.Name.ValueString()]
		if !ok {
			continue
		}
		delete(remote, s.Name.ValueString())
		storages = append(storages, blobStorageToModel(manifest))
	}
	extra := make([]string, 0, len(remote))
	for name := range remote {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	for _, name := range extra {
		storages = append(storages, blobStorageToModel(remote[name]))
	}
	state.BlobStorages = storages

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *azureAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan azureAccountModel
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

func (r *azureAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state azureAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, azureManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry Azure account failed", err.Error())
	}
}

// ImportState imports an Azure account by its FQN, e.g. "my-tenant:azure:my-account".
func (r *azureAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("fqn"), req, resp)
}

func (r *azureAccountResource) apply(ctx context.Context, plan *azureAccountModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, azureManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry Azure account manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied azure account manifest", map[string]any{
		"action": result.Action,
		"fqn":    result.Data.FQN,
	})
	plan.ID = types.StringValue(result.Data.ID)
	plan.FQN = types.StringValue(result.Data.FQN)
}

func azureManifestFromModel(m *azureAccountModel) client.AzureAccountManifest {
	manifest := client.AzureAccountManifest{
		Type:           "provider-account/azure",
		Name:           m.Name.ValueString(),
		SubscriptionID: m.SubscriptionID.ValueString(),
		Integrations:   []client.AzureBlobStorage{},
	}
	for _, s := range m.BlobStorages {
		integration := client.AzureBlobStorage{
			Type:        "integration/blob-storage/azure/blob",
			Name:        s.Name.ValueString(),
			StorageRoot: s.StorageRoot.ValueString(),
		}
		if !s.ConnectionString.IsNull() {
			integration.AuthData = &client.APIKeyAuth{Type: "connection-string", ConnectionString: s.ConnectionString.ValueString()}
		}
		for _, sub := range s.AuthorizedSubjects {
			integration.AuthorizedSubjects = append(integration.AuthorizedSubjects, sub.ValueString())
		}
		manifest.Integrations = append(manifest.Integrations, integration)
	}
	return manifest
}

func blobStorageToModel(manifest client.AzureBlobStorage) blobStorageModel {
	s := blobStorageModel{
		Name:        types.StringValue(manifest.Name),
		StorageRoot: types.StringValue(manifest.StorageRoot),
	}
	if manifest.AuthData != nil {
		s.ConnectionString = stringOrNull(manifest.AuthData.ConnectionString)
	}
	for _, sub := range manifest.AuthorizedSubjects {
		s.AuthorizedSubjects = append(s.AuthorizedSubjects, types.StringValue(sub))
	}
	return s
}
