package provider

import (
	"context"
	"fmt"

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
	_ resource.Resource                = (*mlRepoResource)(nil)
	_ resource.ResourceWithConfigure   = (*mlRepoResource)(nil)
	_ resource.ResourceWithImportState = (*mlRepoResource)(nil)
)

func newMlRepoResource() resource.Resource {
	return &mlRepoResource{}
}

// mlRepoResource manages an ML repo ("ml-repo" manifests) via the dedicated
// PUT/GET/DELETE /api/svc/v1/ml-repos endpoints. ML repos are the
// storage-backed containers that agent skills and prompts live in.
type mlRepoResource struct {
	client *client.Client
}

type mlRepoResourceModel struct {
	ID                    types.String        `tfsdk:"id"`
	Name                  types.String        `tfsdk:"name"`
	Description           types.String        `tfsdk:"description"`
	StorageIntegrationFQN types.String        `tfsdk:"storage_integration_fqn"`
	Collaborators         []collaboratorModel `tfsdk:"collaborators"`
}

func (r *mlRepoResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ml_repo"
}

func (r *mlRepoResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry ML repo (`ml-repo` manifest) via `PUT /api/svc/v1/ml-repos` — the " +
			"blob-storage-backed container that versioned artifacts such as agent skills (`truefoundry_agent_skill`) " +
			"and prompts (`truefoundry_prompt`) live in. Deleting the repo deletes the artifacts it contains. " +
			"Existing repos can be imported by their name, e.g. `terraform import truefoundry_ml_repo.example my-repo`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the ML repo.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the ML repo (3-100 characters; must start with a letter, end with a letter or digit, and contain only letters, digits and hyphens — upper case is allowed here, unlike other manifest names). Changing it replaces the repo.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(minNameLen, maxMlRepoNameLen),
					stringvalidator.RegexMatches(mlRepoNameRegexp, "must start with a letter, end with a letter or digit, and contain only letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Human-readable description of what the repo contains.",
			},
			"storage_integration_fqn": schema.StringAttribute{
				Required: true,
				Description: "FQN of the blob storage integration backing the repo, e.g. of a `truefoundry_azure_account` " +
					"integration: my-tenant:azure:my-account:blob-storage:my-storage. Artifacts already written cannot move, " +
					"so changing it replaces the repo.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"collaborators": schema.SetNestedAttribute{
				Optional:    true,
				Description: "Subjects with a role on this ML repo.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"subject": schema.StringAttribute{
							Required:    true,
							Description: "Subject FQN, e.g. user:jane@example.com or team:everyone.",
						},
						"role_id": schema.StringAttribute{
							Required:    true,
							Description: "Role to assign, e.g. mlf-project-admin, mlf-project-editor, or mlf-project-viewer.",
						},
					},
				},
			},
		},
	}
}

func (r *mlRepoResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *mlRepoResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan mlRepoResourceModel
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

func (r *mlRepoResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state mlRepoResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	repo, found, err := getMlRepoByName(ctx, r.client, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry ML repo failed", err.Error())
		return
	}
	if !found {
		tflog.Info(ctx, "ML repo not found on the control plane; removing from state",
			map[string]any{"name": state.Name.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(repo.ID)
	state.Name = types.StringValue(repo.Manifest.Name)
	state.Description = stringOrNull(repo.Manifest.Description)
	state.StorageIntegrationFQN = types.StringValue(repo.Manifest.StorageIntegrationFQN)
	state.Collaborators = nil
	for _, c := range repo.Manifest.Collaborators {
		state.Collaborators = append(state.Collaborators, collaboratorModel{
			Subject: types.StringValue(c.Subject),
			RoleID:  types.StringValue(c.RoleID),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *mlRepoResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan mlRepoResourceModel
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

func (r *mlRepoResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state mlRepoResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteMlRepo(ctx, state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry ML repo failed", err.Error())
	}
}

// ImportState imports an ML repo by its name, e.g. "my-repo".
func (r *mlRepoResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *mlRepoResource) apply(ctx context.Context, plan *mlRepoResourceModel, diags *diag.Diagnostics) {
	repo, err := r.client.ApplyMlRepo(ctx, mlRepoManifestFromModel(plan))
	if err != nil {
		diags.AddError("Applying TrueFoundry ML repo manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied ML repo manifest", map[string]any{"name": plan.Name.ValueString(), "id": repo.ID})
	plan.ID = types.StringValue(repo.ID)
}

// getMlRepoByName fetches a single ML repo by name via the list endpoint.
func getMlRepoByName(ctx context.Context, c *client.Client, name string) (*client.MlRepo, bool, error) {
	repos, err := c.ListMlRepos(ctx, name)
	if err != nil {
		return nil, false, err
	}
	if len(repos) == 0 {
		return nil, false, nil
	}
	return &repos[0], true, nil
}

func mlRepoManifestFromModel(m *mlRepoResourceModel) client.MlRepoManifest {
	manifest := client.MlRepoManifest{
		Type:                  "ml-repo",
		Name:                  m.Name.ValueString(),
		Description:           m.Description.ValueString(),
		StorageIntegrationFQN: m.StorageIntegrationFQN.ValueString(),
		Collaborators:         []client.Collaborator{},
	}
	for _, c := range m.Collaborators {
		manifest.Collaborators = append(manifest.Collaborators, client.Collaborator{
			Subject: c.Subject.ValueString(),
			RoleID:  c.RoleID.ValueString(),
		})
	}
	return manifest
}
