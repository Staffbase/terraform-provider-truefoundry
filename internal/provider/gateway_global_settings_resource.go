package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
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
	_ resource.Resource                   = (*gatewayGlobalSettingsResource)(nil)
	_ resource.ResourceWithConfigure      = (*gatewayGlobalSettingsResource)(nil)
	_ resource.ResourceWithValidateConfig = (*gatewayGlobalSettingsResource)(nil)
	_ resource.ResourceWithImportState    = (*gatewayGlobalSettingsResource)(nil)
)

// loggingSettingsTypeValues are the supported request logging modes.
var loggingSettingsTypeValues = []string{"always", "never", "header_controlled"}

func newGatewayGlobalSettingsResource() resource.Resource {
	return &gatewayGlobalSettingsResource{}
}

// gatewayGlobalSettingsResource manages the tenant-wide AI gateway global
// settings via the manifest-based apply/delete API and the dedicated
// llm-gateway/config read endpoint.
type gatewayGlobalSettingsResource struct {
	client *client.Client
}

type gatewayGlobalSettingsModel struct {
	ID                    types.String                `tfsdk:"id"`
	RetrySettings         *retrySettingsModel         `tfsdk:"retry_settings"`
	LoggingSettings       *loggingSettingsModel       `tfsdk:"logging_settings"`
	SemanticCacheSettings *semanticCacheSettingsModel `tfsdk:"semantic_cache_settings"`
	GuardrailSettings     *guardrailSettingsModel     `tfsdk:"guardrail_settings"`
	ModelConfiguration    *modelConfigurationModel    `tfsdk:"model_configuration"`
}

type retrySettingsModel struct {
	Attempts      types.Int64    `tfsdk:"attempts"`
	Delay         types.Int64    `tfsdk:"delay"`
	OnStatusCodes []types.String `tfsdk:"on_status_codes"`
}

type loggingSettingsModel struct {
	Type             types.String `tfsdk:"type"`
	LogBodyByDefault types.Bool   `tfsdk:"log_body_by_default"`
}

type semanticCacheSettingsModel struct {
	SemanticCacheEmbeddingModel types.String `tfsdk:"semantic_cache_embedding_model"`
}

type guardrailSettingsModel struct {
	TimeoutMS               types.Int64 `tfsdk:"timeout_ms"`
	EnableParallelExecution types.Bool  `tfsdk:"enable_parallel_execution"`
}

type modelConfigurationModel struct {
	AllowedModels []types.String `tfsdk:"allowed_models"`
}

func (r *gatewayGlobalSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_global_settings"
}

func (r *gatewayGlobalSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the AI gateway global settings: request retries, logging, semantic caching, " +
			"guardrail execution, and the served-models allowlist. Applied as a `gateway-global-settings` manifest " +
			"via `PUT /api/svc/v1/apply`. **The global settings are a tenant-wide singleton** — define at most one " +
			"of this resource per control plane; an apply replaces the whole settings manifest, so omitted settings " +
			"are reset to platform defaults. Import the existing settings with " +
			"`terraform import truefoundry_gateway_global_settings.example gateway-global-settings`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the gateway global settings.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"retry_settings": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Automatic retries of failed requests.",
				Attributes: map[string]schema.Attribute{
					"attempts": schema.Int64Attribute{
						Required:    true,
						Description: "Maximum number of retry attempts.",
						Validators:  []validator.Int64{int64validator.AtLeast(1)},
					},
					"delay": schema.Int64Attribute{
						Optional:    true,
						Description: "Delay between retry attempts in milliseconds.",
					},
					"on_status_codes": schema.SetAttribute{
						Optional:    true,
						ElementType: types.StringType,
						Description: "HTTP status codes that trigger a retry, e.g. [\"429\", \"503\"]. Omit the attribute (rather than an empty set) to retry on the platform's default codes.",
						Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
					},
				},
			},
			"logging_settings": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Request/response logging behavior of the gateway.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:    true,
						Description: fmt.Sprintf("Logging mode. One of: `%s`.", strings.Join(loggingSettingsTypeValues, "`, `")),
						Validators: []validator.String{
							stringvalidator.OneOf(loggingSettingsTypeValues...),
						},
					},
					"log_body_by_default": schema.BoolAttribute{
						Optional:    true,
						Description: "Whether request/response bodies are logged when no header overrides it (header_controlled mode only).",
					},
				},
			},
			"semantic_cache_settings": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Semantic response caching.",
				Attributes: map[string]schema.Attribute{
					"semantic_cache_embedding_model": schema.StringAttribute{
						Required:    true,
						Description: "Model the semantic cache uses to embed prompts, e.g. generic/text-embedding-3-small.",
					},
				},
			},
			"guardrail_settings": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Guardrail execution behavior. Set at least one of the attributes.",
				Attributes: map[string]schema.Attribute{
					"timeout_ms": schema.Int64Attribute{
						Optional:    true,
						Description: "Timeout for guardrail evaluation in milliseconds.",
						Validators:  []validator.Int64{int64validator.AtLeast(1)},
					},
					"enable_parallel_execution": schema.BoolAttribute{
						Optional:    true,
						Description: "Run guardrails in parallel instead of sequentially.",
					},
				},
			},
			"model_configuration": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Restrictions on the models the gateway serves.",
				Attributes: map[string]schema.Attribute{
					"allowed_models": schema.SetAttribute{
						Required:    true,
						ElementType: types.StringType,
						Description: "Models the gateway is allowed to serve. An empty set allows no models; omit the whole `model_configuration` block to allow all.",
					},
				},
			},
		},
	}
}

func (r *gatewayGlobalSettingsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the logging mode field combinations and a non-empty
// guardrail settings block.
func (r *gatewayGlobalSettingsResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config gatewayGlobalSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if l := config.LoggingSettings; l != nil && !l.Type.IsNull() {
		logBodyPath := path.Root("logging_settings").AtName("log_body_by_default")
		switch l.Type.ValueString() {
		case "header_controlled":
			if l.LogBodyByDefault.IsNull() {
				resp.Diagnostics.AddAttributeError(logBodyPath, "Invalid logging settings",
					"`log_body_by_default` is required with logging type \"header_controlled\".")
			}
		default:
			if !l.LogBodyByDefault.IsNull() {
				resp.Diagnostics.AddAttributeError(logBodyPath, "Invalid logging settings",
					"`log_body_by_default` is only valid with logging type \"header_controlled\".")
			}
		}
	}
	if g := config.GuardrailSettings; g != nil && g.TimeoutMS.IsNull() && g.EnableParallelExecution.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("guardrail_settings"), "Invalid guardrail settings",
			"`guardrail_settings` needs at least one of `timeout_ms` and `enable_parallel_execution`.")
	}
}

func (r *gatewayGlobalSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayGlobalSettingsModel
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

func (r *gatewayGlobalSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gatewayGlobalSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	settings, err := r.client.GetGatewayGlobalSettings(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry gateway global settings failed", err.Error())
		return
	}
	if settings == nil {
		tflog.Info(ctx, "no gateway global settings on the control plane; removing from state")
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, globalSettingsToModel(settings))...)
}

func (r *gatewayGlobalSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan gatewayGlobalSettingsModel
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

func (r *gatewayGlobalSettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gatewayGlobalSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, globalSettingsManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry gateway global settings failed", err.Error())
	}
}

// ImportState imports the tenant's gateway global settings; the import ID is
// ignored (the settings are a singleton), by convention pass
// "gateway-global-settings".
func (r *gatewayGlobalSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// apply sends the manifest and refreshes the computed ID from the dedicated
// read endpoint (the generic apply response is not relied on for gateway
// configs).
func (r *gatewayGlobalSettingsResource) apply(ctx context.Context, plan *gatewayGlobalSettingsModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, globalSettingsManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry gateway global settings manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied gateway global settings manifest", map[string]any{"action": result.Action})
	settings, err := r.client.GetGatewayGlobalSettings(ctx)
	if err != nil {
		diags.AddError("Reading back TrueFoundry gateway global settings failed", err.Error())
		return
	}
	if settings == nil {
		diags.AddError("Reading back TrueFoundry gateway global settings failed", "settings not found after apply")
		return
	}
	plan.ID = types.StringValue(settings.ID)
}

func globalSettingsManifestFromModel(m *gatewayGlobalSettingsModel) client.GatewayGlobalSettingsManifest {
	manifest := client.GatewayGlobalSettingsManifest{Type: "gateway-global-settings"}
	if r := m.RetrySettings; r != nil {
		manifest.RetrySettings = &client.GatewayRetrySettings{
			Attempts:      r.Attempts.ValueInt64(),
			Delay:         int64OrNil(r.Delay),
			OnStatusCodes: subjectsFromModel(r.OnStatusCodes),
		}
	}
	if l := m.LoggingSettings; l != nil {
		manifest.LoggingSettings = &client.GatewayLoggingSettings{Type: l.Type.ValueString()}
		if !l.LogBodyByDefault.IsNull() {
			v := l.LogBodyByDefault.ValueBool()
			manifest.LoggingSettings.LogBodyByDefault = &v
		}
	}
	if s := m.SemanticCacheSettings; s != nil {
		manifest.SemanticCacheSettings = &client.GatewaySemanticCacheSettings{
			SemanticCacheEmbeddingModel: s.SemanticCacheEmbeddingModel.ValueString(),
		}
	}
	if g := m.GuardrailSettings; g != nil {
		manifest.GuardrailSettings = &client.GatewayGuardrailSettings{TimeoutMS: int64OrNil(g.TimeoutMS)}
		if !g.EnableParallelExecution.IsNull() {
			v := g.EnableParallelExecution.ValueBool()
			manifest.GuardrailSettings.EnableParallelExecution = &v
		}
	}
	if mc := m.ModelConfiguration; mc != nil {
		manifest.ModelConfiguration = &client.GatewayModelConfiguration{
			AllowedModels: refsOrEmpty(mc.AllowedModels),
		}
	}
	return manifest
}

func globalSettingsToModel(settings *client.GatewayGlobalSettings) *gatewayGlobalSettingsModel {
	out := &gatewayGlobalSettingsModel{ID: types.StringValue(settings.ID)}
	manifest := settings.Manifest
	if r := manifest.RetrySettings; r != nil {
		out.RetrySettings = &retrySettingsModel{
			Attempts:      types.Int64Value(r.Attempts),
			Delay:         int64OrNull(r.Delay),
			OnStatusCodes: subjectsToModel(r.OnStatusCodes),
		}
	}
	if l := manifest.LoggingSettings; l != nil {
		out.LoggingSettings = &loggingSettingsModel{
			Type:             types.StringValue(l.Type),
			LogBodyByDefault: types.BoolNull(),
		}
		if l.LogBodyByDefault != nil {
			out.LoggingSettings.LogBodyByDefault = types.BoolValue(*l.LogBodyByDefault)
		}
	}
	if s := manifest.SemanticCacheSettings; s != nil {
		out.SemanticCacheSettings = &semanticCacheSettingsModel{
			SemanticCacheEmbeddingModel: types.StringValue(s.SemanticCacheEmbeddingModel),
		}
	}
	if g := manifest.GuardrailSettings; g != nil {
		out.GuardrailSettings = &guardrailSettingsModel{
			TimeoutMS:               int64OrNull(g.TimeoutMS),
			EnableParallelExecution: types.BoolNull(),
		}
		if g.EnableParallelExecution != nil {
			out.GuardrailSettings.EnableParallelExecution = types.BoolValue(*g.EnableParallelExecution)
		}
	}
	if mc := manifest.ModelConfiguration; mc != nil {
		// Not subjectsToModel: a stored empty allowlist must round-trip as an
		// empty (non-null) list.
		models := make([]types.String, 0, len(mc.AllowedModels))
		for _, m := range mc.AllowedModels {
			models = append(models, types.StringValue(m))
		}
		out.ModelConfiguration = &modelConfigurationModel{AllowedModels: models}
	}
	return out
}
