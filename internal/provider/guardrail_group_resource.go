package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
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
	_ resource.Resource                   = (*guardrailGroupResource)(nil)
	_ resource.ResourceWithConfigure      = (*guardrailGroupResource)(nil)
	_ resource.ResourceWithValidateConfig = (*guardrailGroupResource)(nil)
	_ resource.ResourceWithImportState    = (*guardrailGroupResource)(nil)
)

// guardrailTypeValues are the supported guardrail config types.
var guardrailTypeValues = []string{"azure-prompt-shield", "azure-content-safety"}

// enforcingStrategyValues are the guardrail enforcement strategies.
var enforcingStrategyValues = []string{"enforce", "enforce_but_ignore_on_error", "audit"}

// contentSafetyCategories are the Azure Content Safety harm categories.
var contentSafetyCategories = []string{"Hate", "SelfHarm", "Sexual", "Violence"}

func newGuardrailGroupResource() resource.Resource {
	return &guardrailGroupResource{}
}

// guardrailGroupResource manages an AI Gateway guardrail config group and its
// guardrail configs via the manifest-based apply/delete API.
type guardrailGroupResource struct {
	client *client.Client
}

type guardrailGroupModel struct {
	ID            types.String        `tfsdk:"id"`
	FQN           types.String        `tfsdk:"fqn"`
	Name          types.String        `tfsdk:"name"`
	Collaborators []collaboratorModel `tfsdk:"collaborators"`
	Guardrails    []guardrailModel    `tfsdk:"guardrails"`
}

type collaboratorModel struct {
	Subject types.String `tfsdk:"subject"`
	RoleID  types.String `tfsdk:"role_id"`
}

type guardrailModel struct {
	Type              types.String `tfsdk:"type"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	APIKey            types.String `tfsdk:"api_key"`
	EnforcingStrategy types.String `tfsdk:"enforcing_strategy"`
	ResourceName      types.String `tfsdk:"resource_name"`
	APIVersion        types.String `tfsdk:"api_version"`
	CustomHost        types.String `tfsdk:"custom_host"`

	// azure-content-safety only
	SeverityThreshold types.Int64    `tfsdk:"severity_threshold"`
	Categories        []types.String `tfsdk:"categories"`
	BlocklistNames    []types.String `tfsdk:"blocklist_names"`
}

func (r *guardrailGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_guardrail_group"
}

func (r *guardrailGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry AI Gateway guardrail config group and its guardrails. " +
			"Applied as a `provider-account/guardrail-config-group` manifest via `PUT /api/svc/v1/apply`. " +
			"Existing groups can be imported by their FQN, e.g. `terraform import truefoundry_guardrail_group.example my-tenant:guardrail-config-group:my-group`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the guardrail config group.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed:      true,
				Description:   "Fully qualified name of the guardrail config group.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the guardrail config group (3-36 characters; must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens). Changing it replaces the group.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(minNameLen, maxWideAccountNameLen),
					stringvalidator.RegexMatches(nameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"collaborators": schema.SetNestedAttribute{
				Optional:    true,
				Description: "Subjects with a role on this guardrail config group.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"subject": schema.StringAttribute{
							Required:    true,
							Description: "Subject FQN, e.g. user:jane@example.com or team:my-team.",
						},
						"role_id": schema.StringAttribute{
							Required:    true,
							Description: "Role to assign, e.g. provider-account-manager.",
						},
					},
				},
			},
			"guardrails": schema.ListNestedAttribute{
				Required:      true,
				Description:   "Guardrail configs in this group.",
				Validators:    []validator.List{listvalidator.SizeAtLeast(1)},
				PlanModifiers: []planmodifier.List{orderInsensitive()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Required:    true,
							Description: fmt.Sprintf("Guardrail type. One of: `%s`.", strings.Join(guardrailTypeValues, "`, `")),
							Validators: []validator.String{
								stringvalidator.OneOf(guardrailTypeValues...),
							},
						},
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name identifying this guardrail config.",
							Validators: []validator.String{
								stringvalidator.LengthBetween(1, 64),
							},
						},
						"description": schema.StringAttribute{
							Optional:    true,
							Description: "Optional description of this guardrail config.",
						},
						"api_key": schema.StringAttribute{
							Required:  true,
							Sensitive: true,
							Description: "API key of the Azure Content Safety resource. " +
								"Accepts a TrueFoundry secret reference (recommended — keeps secret material out of the Terraform state); " +
								"the control plane returns the value as stored, so drift is detected.",
						},
						"enforcing_strategy": schema.StringAttribute{
							Required: true,
							Description: fmt.Sprintf("How violations are handled. One of: `%s`. "+
								"`enforce` blocks on failure and on guardrail errors, `enforce_but_ignore_on_error` blocks on failure but continues on guardrail errors, `audit` only records violations.",
								strings.Join(enforcingStrategyValues, "`, `")),
							Validators: []validator.String{
								stringvalidator.OneOf(enforcingStrategyValues...),
							},
						},
						"resource_name": schema.StringAttribute{
							Required:    true,
							Description: "Name of the Azure Content Safety resource the service is deployed on, e.g. my-content-safety.",
						},
						"api_version": schema.StringAttribute{
							Required:    true,
							Description: "Azure API version, e.g. 2024-09-01.",
						},
						"custom_host": schema.StringAttribute{
							Optional:    true,
							Description: "Custom endpoint URL; unset uses the default Azure endpoint.",
						},
						"severity_threshold": schema.Int64Attribute{
							Optional: true,
							Description: "Minimum severity (0-6) to flag content; lower blocks more. " +
								"Required for `azure-content-safety`, must be unset for `azure-prompt-shield`.",
							Validators: []validator.Int64{
								int64validator.Between(0, 6),
							},
						},
						"categories": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: fmt.Sprintf("Harm categories to detect. One or more of: `%s`. "+
								"Required for `azure-content-safety`, must be unset for `azure-prompt-shield`.",
								strings.Join(contentSafetyCategories, "`, `")),
							Validators: []validator.Set{
								setvalidator.SizeAtLeast(1),
								setvalidator.ValueStringsAre(stringvalidator.OneOf(contentSafetyCategories...)),
							},
						},
						"blocklist_names": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Custom Azure Content Safety blocklists to check text against (`azure-content-safety` only).",
						},
					},
				},
			},
		},
	}
}

func (r *guardrailGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the per-guardrail-type field combinations.
func (r *guardrailGroupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config guardrailGroupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// guardrails is a set, so elements have no addressable index; errors are
	// anchored at the attribute and name the offending guardrail in the message.
	for _, g := range config.Guardrails {
		fail := func(attr, why string) {
			resp.Diagnostics.AddAttributeError(path.Root("guardrails"), "Invalid guardrail configuration",
				fmt.Sprintf("`%s` of guardrail %q %s for type %q.", attr, g.Name.ValueString(), why, g.Type.ValueString()))
		}
		switch g.Type.ValueString() {
		case "azure-content-safety":
			if g.SeverityThreshold.IsNull() {
				fail("severity_threshold", "is required")
			}
			if g.Categories == nil {
				fail("categories", "is required")
			}
		case "azure-prompt-shield":
			if !g.SeverityThreshold.IsNull() {
				fail("severity_threshold", "must not be set")
			}
			if g.Categories != nil {
				fail("categories", "must not be set")
			}
			if g.BlocklistNames != nil {
				fail("blocklist_names", "must not be set")
			}
		}
	}
}

func (r *guardrailGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan guardrailGroupModel
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

func (r *guardrailGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state guardrailGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	integrations, err := r.client.ListGuardrailIntegrations(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry guardrail integrations failed", err.Error())
		return
	}

	// Like provider accounts, guardrail groups have no dedicated GET: the
	// group is reconstructed from its guardrail configs and the embedded
	// account manifest. Zero integrations means the group is gone.
	var account *client.EmbeddedProviderAccount
	remote := map[string]client.GuardrailConfig{}
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
		tflog.Info(ctx, "guardrail group has no configs on the control plane; removing from state",
			map[string]any{"fqn": state.FQN.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	if account != nil {
		state.ID = types.StringValue(account.ID)
		state.Name = types.StringValue(account.Manifest.Name)
		state.Collaborators = nil
		for _, c := range account.Manifest.Collaborators {
			state.Collaborators = append(state.Collaborators, collaboratorModel{
				Subject: types.StringValue(c.Subject),
				RoleID:  types.StringValue(c.RoleID),
			})
		}
	}

	// Rebuild the list keeping configured order for entries that still exist
	// and appending unknown remote entries in name order, so pure refreshes
	// stay diff-free while real drift is surfaced.
	var guardrails []guardrailModel
	for _, g := range state.Guardrails {
		manifest, ok := remote[g.Name.ValueString()]
		if !ok {
			continue
		}
		delete(remote, g.Name.ValueString())
		guardrails = append(guardrails, guardrailToModel(manifest))
	}
	extra := make([]string, 0, len(remote))
	for name := range remote {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	for _, name := range extra {
		guardrails = append(guardrails, guardrailToModel(remote[name]))
	}
	state.Guardrails = guardrails

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *guardrailGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan guardrailGroupModel
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

func (r *guardrailGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state guardrailGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, guardrailGroupManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry guardrail group failed", err.Error())
	}
}

// ImportState imports a guardrail group by its FQN, e.g.
// "my-tenant:guardrail-config-group:my-group".
func (r *guardrailGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("fqn"), req, resp)
}

func (r *guardrailGroupResource) apply(ctx context.Context, plan *guardrailGroupModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, guardrailGroupManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry guardrail group manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied guardrail group manifest", map[string]any{
		"action": result.Action,
		"fqn":    result.Data.FQN,
	})
	plan.ID = types.StringValue(result.Data.ID)
	plan.FQN = types.StringValue(result.Data.FQN)
}

func guardrailGroupManifestFromModel(m *guardrailGroupModel) client.GuardrailGroupManifest {
	manifest := client.GuardrailGroupManifest{
		Type:         "provider-account/guardrail-config-group",
		Name:         m.Name.ValueString(),
		Integrations: []client.GuardrailConfig{},
	}
	for _, c := range m.Collaborators {
		manifest.Collaborators = append(manifest.Collaborators, client.Collaborator{
			Subject: c.Subject.ValueString(),
			RoleID:  c.RoleID.ValueString(),
		})
	}
	for _, g := range m.Guardrails {
		config := client.GuardrailConfig{
			Type:              "integration/guardrail-config/" + g.Type.ValueString(),
			Name:              g.Name.ValueString(),
			Description:       g.Description.ValueString(),
			AuthData:          &client.APIKeyAuth{Type: "api-key", APIKey: g.APIKey.ValueString()},
			Operation:         "validate",
			EnforcingStrategy: g.EnforcingStrategy.ValueString(),
			Config: client.GuardrailConfigSettings{
				ResourceName:      g.ResourceName.ValueString(),
				APIVersion:        g.APIVersion.ValueString(),
				CustomHost:        g.CustomHost.ValueString(),
				SeverityThreshold: int64OrNil(g.SeverityThreshold),
			},
		}
		for _, cat := range g.Categories {
			config.Config.Categories = append(config.Config.Categories, cat.ValueString())
		}
		for _, b := range g.BlocklistNames {
			config.Config.BlocklistNames = append(config.Config.BlocklistNames, b.ValueString())
		}
		manifest.Integrations = append(manifest.Integrations, config)
	}
	return manifest
}

func guardrailToModel(manifest client.GuardrailConfig) guardrailModel {
	m := guardrailModel{
		Type:              types.StringValue(strings.TrimPrefix(manifest.Type, "integration/guardrail-config/")),
		Name:              types.StringValue(manifest.Name),
		Description:       stringOrNull(manifest.Description),
		EnforcingStrategy: stringOrNull(manifest.EnforcingStrategy),
		ResourceName:      stringOrNull(manifest.Config.ResourceName),
		APIVersion:        stringOrNull(manifest.Config.APIVersion),
		CustomHost:        stringOrNull(manifest.Config.CustomHost),
		SeverityThreshold: int64OrNull(manifest.Config.SeverityThreshold),
	}
	if manifest.AuthData != nil {
		m.APIKey = stringOrNull(manifest.AuthData.APIKey)
	}
	for _, cat := range manifest.Config.Categories {
		m.Categories = append(m.Categories, types.StringValue(cat))
	}
	for _, b := range manifest.Config.BlocklistNames {
		m.BlocklistNames = append(m.BlocklistNames, types.StringValue(b))
	}
	return m
}
