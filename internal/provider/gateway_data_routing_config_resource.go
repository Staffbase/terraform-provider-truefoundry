package provider

import (
	"context"
	"fmt"
	"strings"

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
	_ resource.Resource                   = (*gatewayDataRoutingConfigResource)(nil)
	_ resource.ResourceWithConfigure      = (*gatewayDataRoutingConfigResource)(nil)
	_ resource.ResourceWithValidateConfig = (*gatewayDataRoutingConfigResource)(nil)
	_ resource.ResourceWithImportState    = (*gatewayDataRoutingConfigResource)(nil)
)

// dataRoutingStorageTypeValues are the supported storage kinds.
var dataRoutingStorageTypeValues = []string{"controlplane-managed", "customer-managed"}

// dataRoutingRegionValues are the control-plane-managed storage regions.
var dataRoutingRegionValues = []string{"us", "australia", "india"}

func newGatewayDataRoutingConfigResource() resource.Resource {
	return &gatewayDataRoutingConfigResource{}
}

// gatewayDataRoutingConfigResource manages the tenant-wide AI gateway data
// routing config via the manifest-based apply/delete API and the dedicated
// llm-gateway/config read endpoint.
type gatewayDataRoutingConfigResource struct {
	client *client.Client
}

type gatewayDataRoutingConfigModel struct {
	ID           types.String                  `tfsdk:"id"`
	Name         types.String                  `tfsdk:"name"`
	Default      *dataRoutingTargetModel       `tfsdk:"default"`
	Destinations []dataRoutingDestinationModel `tfsdk:"destinations"`
}

type dataRoutingTargetModel struct {
	Storage                   dataRoutingStorageModel `tfsdk:"storage"`
	TracesRetentionPeriodDays types.Int64             `tfsdk:"traces_retention_period_days"`
}

type dataRoutingStorageModel struct {
	Type                  types.String `tfsdk:"type"`
	Region                types.String `tfsdk:"region"`
	StorageIntegrationFQN types.String `tfsdk:"storage_integration_fqn"`
}

type dataRoutingDestinationModel struct {
	Name                      types.String            `tfsdk:"name"`
	Enabled                   types.Bool              `tfsdk:"enabled"`
	Description               types.String            `tfsdk:"description"`
	When                      []dataAccessFilterModel `tfsdk:"when"`
	Storage                   dataRoutingStorageModel `tfsdk:"storage"`
	TracesRetentionPeriodDays types.Int64             `tfsdk:"traces_retention_period_days"`
}

func (r *gatewayDataRoutingConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_data_routing_config"
}

// dataRoutingStorageSchema is the storage block shared by the default route and
// destinations.
func dataRoutingStorageSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required:    true,
		Description: "Where the data is stored.",
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				Required:    true,
				Description: fmt.Sprintf("Storage kind. One of: `%s`.", strings.Join(dataRoutingStorageTypeValues, "`, `")),
				Validators: []validator.String{
					stringvalidator.OneOf(dataRoutingStorageTypeValues...),
				},
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Description: fmt.Sprintf("Region for controlplane-managed storage (not allowed otherwise). One of: `%s`.", strings.Join(dataRoutingRegionValues, "`, `")),
				Validators: []validator.String{
					stringvalidator.OneOf(dataRoutingRegionValues...),
				},
			},
			"storage_integration_fqn": schema.StringAttribute{
				Optional:    true,
				Description: "Blob storage integration FQN for customer-managed storage (required there, not allowed otherwise), e.g. my-tenant:azure:my-blobstorage:blob-storage:my-container.",
			},
		},
	}
}

func (r *gatewayDataRoutingConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	retention := schema.Int64Attribute{
		Optional:    true,
		Description: "How many days traces are retained.",
	}
	resp.Schema = schema.Schema{
		Description: "Manages the AI gateway data routing configuration: where the gateway's observability data " +
			"is stored — control-plane-managed or customer-managed blob storage, with per-request routing rules. " +
			"Applied as a `gateway-data-routing-config` manifest via `PUT /api/svc/v1/apply`. " +
			"**The data routing config is a tenant-wide singleton** — define at most one of this resource per " +
			"control plane; an apply replaces the whole config. Import the existing config with " +
			"`terraform import truefoundry_gateway_data_routing_config.example gateway-data-routing-config`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the gateway data routing config.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the data routing config (3-32 characters, lowercase alphanumeric and hyphens).",
				Validators: []validator.String{
					stringvalidator.LengthBetween(3, 32),
					stringvalidator.RegexMatches(nameRegexp, "must be lowercase alphanumeric with hyphens"),
				},
			},
			"default": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Storage for data not matched by any destination.",
				Attributes: map[string]schema.Attribute{
					"storage":                      dataRoutingStorageSchema(),
					"traces_retention_period_days": retention,
				},
			},
			"destinations": schema.ListNestedAttribute{
				Optional:    true,
				Description: "Routes for matching requests, evaluated before the default. Omit the attribute (rather than an empty list) when everything goes to the default.",
				Validators:  []validator.List{listvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name identifying this destination, unique within the config.",
							Validators:  []validator.String{stringvalidator.LengthBetween(1, 64)},
						},
						"enabled": schema.BoolAttribute{
							Required:    true,
							Description: "Whether the destination is active.",
						},
						"description": schema.StringAttribute{
							Optional:    true,
							Description: "Human-readable description of the destination.",
						},
						"when": schema.SetNestedAttribute{
							Required:    true,
							Description: "Conditions a request must match (all of them) to be routed here.",
							Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"field": schema.StringAttribute{
										Required:    true,
										Description: fmt.Sprintf("Request property to match. One of: `%s`.", strings.Join(dataAccessFilterFieldValues, "`, `")),
										Validators: []validator.String{
											stringvalidator.OneOf(dataAccessFilterFieldValues...),
										},
									},
									"key": schema.StringAttribute{
										Optional:    true,
										Description: "Metadata key to match (required for `metadata` conditions, not allowed otherwise).",
									},
									"values": schema.SetAttribute{
										Required:    true,
										ElementType: types.StringType,
										Description: "Values to match against (IN semantics).",
										Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
									},
								},
							},
						},
						"storage":                      dataRoutingStorageSchema(),
						"traces_retention_period_days": retention,
					},
				},
			},
		},
	}
}

func (r *gatewayDataRoutingConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the storage type field combinations, condition key
// rules, and unique destination names.
func (r *gatewayDataRoutingConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config gatewayDataRoutingConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	validateStorage := func(s dataRoutingStorageModel, storagePath path.Path) {
		if s.Type.IsUnknown() {
			return
		}
		switch s.Type.ValueString() {
		case "controlplane-managed":
			if !s.StorageIntegrationFQN.IsNull() {
				resp.Diagnostics.AddAttributeError(storagePath.AtName("storage_integration_fqn"), "Invalid storage",
					"`storage_integration_fqn` is not allowed on controlplane-managed storage.")
			}
		case "customer-managed":
			if s.StorageIntegrationFQN.IsNull() {
				resp.Diagnostics.AddAttributeError(storagePath.AtName("storage_integration_fqn"), "Invalid storage",
					"`storage_integration_fqn` is required on customer-managed storage.")
			}
			if !s.Region.IsNull() {
				resp.Diagnostics.AddAttributeError(storagePath.AtName("region"), "Invalid storage",
					"`region` is not allowed on customer-managed storage.")
			}
		}
	}

	if config.Default != nil {
		validateStorage(config.Default.Storage, path.Root("default").AtName("storage"))
	}
	seen := map[string]bool{}
	for i, dest := range config.Destinations {
		destPath := path.Root("destinations").AtListIndex(i)
		if !dest.Name.IsUnknown() && !dest.Name.IsNull() {
			if seen[dest.Name.ValueString()] {
				resp.Diagnostics.AddAttributeError(destPath.AtName("name"),
					"Invalid data routing config", fmt.Sprintf("Duplicate destination name %q.", dest.Name.ValueString()))
			}
			seen[dest.Name.ValueString()] = true
		}
		validateStorage(dest.Storage, destPath.AtName("storage"))
		// when is a set, so conditions have no addressable index; errors are
		// anchored at the attribute and name the condition in the message.
		for _, c := range dest.When {
			if c.Field.IsUnknown() {
				continue
			}
			whenPath := destPath.AtName("when")
			switch c.Field.ValueString() {
			case "metadata":
				if c.Key.IsNull() {
					resp.Diagnostics.AddAttributeError(whenPath, "Invalid routing condition",
						"`key` is required on metadata conditions.")
				}
			default:
				if !c.Key.IsNull() {
					resp.Diagnostics.AddAttributeError(whenPath, "Invalid routing condition",
						fmt.Sprintf("`key` is only valid on metadata conditions, not on %q conditions.", c.Field.ValueString()))
				}
			}
		}
	}
}

func (r *gatewayDataRoutingConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayDataRoutingConfigModel
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

func (r *gatewayDataRoutingConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gatewayDataRoutingConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.GetGatewayDataRoutingConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry gateway data routing config failed", err.Error())
		return
	}
	if config == nil {
		tflog.Info(ctx, "no gateway data routing config on the control plane; removing from state")
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(config.ID)
	state.Name = stringOrNull(config.Manifest.Name)
	state.Default = &dataRoutingTargetModel{
		Storage:                   dataRoutingStorageToModel(config.Manifest.Default.Storage),
		TracesRetentionPeriodDays: int64OrNull(config.Manifest.Default.TracesRetentionPeriodDays),
	}
	state.Destinations = nil
	for _, dest := range config.Manifest.Destinations {
		state.Destinations = append(state.Destinations, dataRoutingDestinationToModel(dest))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gatewayDataRoutingConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan gatewayDataRoutingConfigModel
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

func (r *gatewayDataRoutingConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gatewayDataRoutingConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, dataRoutingManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry gateway data routing config failed", err.Error())
	}
}

// ImportState imports the tenant's gateway data routing config; the import
// ID is ignored (the config is a singleton), by convention pass
// "gateway-data-routing-config".
func (r *gatewayDataRoutingConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// apply sends the manifest and refreshes the computed ID from the dedicated
// read endpoint (the generic apply response carries no id/fqn for gateway
// configs).
func (r *gatewayDataRoutingConfigResource) apply(ctx context.Context, plan *gatewayDataRoutingConfigModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, dataRoutingManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry gateway data routing config manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied gateway data routing config manifest", map[string]any{"action": result.Action})
	config, err := r.client.GetGatewayDataRoutingConfig(ctx)
	if err != nil {
		diags.AddError("Reading back TrueFoundry gateway data routing config failed", err.Error())
		return
	}
	if config == nil {
		diags.AddError("Reading back TrueFoundry gateway data routing config failed", "config not found after apply")
		return
	}
	plan.ID = types.StringValue(config.ID)
}

func dataRoutingManifestFromModel(m *gatewayDataRoutingConfigModel) client.GatewayDataRoutingConfigManifest {
	manifest := client.GatewayDataRoutingConfigManifest{
		Type: "gateway-data-routing-config",
		Name: m.Name.ValueString(),
	}
	if m.Default != nil {
		manifest.Default = client.DataRoutingTarget{
			Storage:                   dataRoutingStorageFromModel(m.Default.Storage),
			TracesRetentionPeriodDays: int64OrNil(m.Default.TracesRetentionPeriodDays),
		}
	}
	for _, dest := range m.Destinations {
		out := client.DataRoutingDestination{
			Name:                      dest.Name.ValueString(),
			Enabled:                   dest.Enabled.ValueBool(),
			Description:               dest.Description.ValueString(),
			Storage:                   dataRoutingStorageFromModel(dest.Storage),
			TracesRetentionPeriodDays: int64OrNil(dest.TracesRetentionPeriodDays),
			When:                      []client.DataAccessFilter{},
		}
		for _, c := range dest.When {
			out.When = append(out.When, client.DataAccessFilter{
				Field:    c.Field.ValueString(),
				Key:      c.Key.ValueString(),
				Operator: "IN",
				Value:    subjectsFromModel(c.Values),
			})
		}
		manifest.Destinations = append(manifest.Destinations, out)
	}
	return manifest
}

func dataRoutingStorageFromModel(m dataRoutingStorageModel) client.DataRoutingStorage {
	return client.DataRoutingStorage{
		Type:                  m.Type.ValueString(),
		Region:                m.Region.ValueString(),
		StorageIntegrationFQN: m.StorageIntegrationFQN.ValueString(),
	}
}

func dataRoutingStorageToModel(s client.DataRoutingStorage) dataRoutingStorageModel {
	return dataRoutingStorageModel{
		Type:                  types.StringValue(s.Type),
		Region:                stringOrNull(s.Region),
		StorageIntegrationFQN: stringOrNull(s.StorageIntegrationFQN),
	}
}

func dataRoutingDestinationToModel(dest client.DataRoutingDestination) dataRoutingDestinationModel {
	out := dataRoutingDestinationModel{
		Name:                      types.StringValue(dest.Name),
		Enabled:                   types.BoolValue(dest.Enabled),
		Description:               stringOrNull(dest.Description),
		Storage:                   dataRoutingStorageToModel(dest.Storage),
		TracesRetentionPeriodDays: int64OrNull(dest.TracesRetentionPeriodDays),
	}
	for _, c := range dest.When {
		out.When = append(out.When, dataAccessFilterModel{
			Field:  types.StringValue(c.Field),
			Key:    stringOrNull(c.Key),
			Values: subjectsToModel(c.Value),
		})
	}
	return out
}
