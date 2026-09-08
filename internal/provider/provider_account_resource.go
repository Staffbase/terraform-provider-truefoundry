package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

var (
	_ resource.Resource                   = (*providerAccountResource)(nil)
	_ resource.ResourceWithConfigure      = (*providerAccountResource)(nil)
	_ resource.ResourceWithValidateConfig = (*providerAccountResource)(nil)
	_ resource.ResourceWithImportState    = (*providerAccountResource)(nil)
)

// providerTypeValues are the supported provider account types.
var providerTypeValues = []string{"openai", "azure-openai", "azure-foundry", "virtual-model", "aws-bedrock"}

// routingTypeValues are the supported virtual model routing strategies.
var routingTypeValues = []string{"weight-based-routing", "priority-based-routing"}

// deploymentTypeValues are the Azure OpenAI deployment types.
var deploymentTypeValues = []string{"standard", "datazone_us", "datazone_eu", "global"}

// modelTypeValues are the model types accepted by the TrueFoundry gateway.
var modelTypeValues = []string{
	"chat", "completion", "embedding", "realtime", "rerank",
	"audio_transcription", "audio_translation", "text_to_speech",
	"moderation", "image", "responses", "ocr",
}

func newProviderAccountResource() resource.Resource {
	return &providerAccountResource{}
}

// providerAccountResource manages an AI Gateway provider account and the
// models registered on it, via the manifest-based apply/delete API.
type providerAccountResource struct {
	client *client.Client
}

type providerAccountModel struct {
	ID              types.String `tfsdk:"id"`
	FQN             types.String `tfsdk:"fqn"`
	Name            types.String `tfsdk:"name"`
	ProviderType    types.String `tfsdk:"provider_type"`
	APIKey          types.String `tfsdk:"api_key"`
	BaseURL         types.String `tfsdk:"base_url"`
	AzureEndpoint   types.String `tfsdk:"azure_endpoint"`
	Region          types.String `tfsdk:"region"`
	AccessKeyID     types.String `tfsdk:"access_key_id"`
	SecretAccessKey types.String `tfsdk:"secret_access_key"`
	Models          []modelModel `tfsdk:"models"`
}

type modelModel struct {
	Name       types.String   `tfsdk:"name"`
	ModelID    types.String   `tfsdk:"model_id"`
	ModelTypes []types.String `tfsdk:"model_types"`

	Cost               *costModel     `tfsdk:"cost"`
	AuthorizedSubjects []types.String `tfsdk:"authorized_subjects"`

	// Azure OpenAI
	APIVersion      types.String `tfsdk:"api_version"`
	FoundationModel types.String `tfsdk:"foundation_model"`
	DeploymentType  types.String `tfsdk:"deployment_type"`
	Region          types.String `tfsdk:"region"`

	// Azure Foundry (api_version applies here too)
	APIKey        types.String `tfsdk:"api_key"`
	AzureEndpoint types.String `tfsdk:"azure_endpoint"`

	// Virtual models
	Slug    types.String  `tfsdk:"slug"`
	Routing *routingModel `tfsdk:"routing"`
}

type routingModel struct {
	Type    types.String         `tfsdk:"type"`
	Targets []routingTargetModel `tfsdk:"targets"`
}

type costModel struct {
	Metric     types.String  `tfsdk:"metric"`
	Value      types.Float64 `tfsdk:"value"`
	Input      types.Float64 `tfsdk:"input"`
	Output     types.Float64 `tfsdk:"output"`
	CacheRead  types.Float64 `tfsdk:"cache_read"`
	CacheWrite types.Float64 `tfsdk:"cache_write"`
}

func (r *providerAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_provider_account"
}

func (r *providerAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry AI Gateway provider account and the models available through it. " +
			"Applied as a `provider-account/<provider_type>` manifest via `PUT /api/svc/v1/apply`. " +
			"Existing accounts can be imported by their FQN, e.g. `terraform import truefoundry_provider_account.example my-tenant:azure-openai:my-account`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the provider account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed:      true,
				Description:   "Fully qualified name of the provider account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the provider account (3-32 characters, or 3-64 for `virtual-model`; must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens). Changing it replaces the account.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(minNameLen, maxVirtualModelNameLen),
					stringvalidator.RegexMatches(nameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"provider_type": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     defaultProviderType(),
				Description: fmt.Sprintf("Model provider behind this account. One of: `%s`.", strings.Join(providerTypeValues, "`, `")),
				Validators: []validator.String{
					stringvalidator.OneOf(providerTypeValues...),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "API key used by the gateway to authenticate against the model provider. " +
					"Required for `openai` and `azure-openai`; for `aws-bedrock` it is an alternative to access keys; " +
					"must be unset for `azure-foundry` (auth is per model there) and `virtual-model`. " +
					"Accepts a TrueFoundry secret reference (recommended — keeps secret material out of the Terraform state); " +
					"the control plane returns the value as stored, so drift is detected.",
			},
			"base_url": schema.StringAttribute{
				Optional:    true,
				Description: "Optional custom base URL for the model provider API (`openai` only).",
			},
			"azure_endpoint": schema.StringAttribute{
				Optional:    true,
				Description: "Azure OpenAI Service endpoint URL, e.g. https://{resource-name}.openai.azure.com. Required for `azure-openai`.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Description: "AWS region of the Bedrock account, e.g. eu-central-1. Required for `aws-bedrock`.",
			},
			"access_key_id": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "AWS access key ID (`aws-bedrock` only; alternative to `api_key`). " +
					"Accepts a TrueFoundry secret reference (recommended).",
			},
			"secret_access_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "AWS secret access key (`aws-bedrock` only; required together with `access_key_id`). " +
					"Accepts a TrueFoundry secret reference (recommended).",
			},
			"models": schema.SetNestedAttribute{
				Required:    true,
				Description: "Models exposed on the gateway through this provider account. Order is not significant.",
				Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name identifying this model integration.",
							Validators: []validator.String{
								stringvalidator.LengthBetween(1, 64),
							},
						},
						"model_id": schema.StringAttribute{
							Optional:    true,
							Description: "Provider model identifier or Azure deployment name, e.g. gpt-4o. Required for all provider types except `virtual-model`.",
						},
						"model_types": schema.SetAttribute{
							Required:    true,
							ElementType: types.StringType,
							Description: fmt.Sprintf("Model capabilities. One or more of: `%s`.", strings.Join(modelTypeValues, "`, `")),
							Validators: []validator.Set{
								setvalidator.SizeAtLeast(1),
								setvalidator.ValueStringsAre(stringvalidator.OneOf(modelTypeValues...)),
							},
						},
						"cost": schema.SingleNestedAttribute{
							Optional: true,
							Description: "Optional pricing of this model, used for gateway cost tracking. Token-based metrics " +
								"(e.g. per_1000_tokens) use `input`/`output` rates; scalar metrics (e.g. per_1000_embedding_tokens, " +
								"per_minute_of_audio) use `value`. Volume-based pricing tiers are not supported.",
							Attributes: map[string]schema.Attribute{
								"metric": schema.StringAttribute{
									Required:    true,
									Description: "Cost metric, e.g. per_1000_tokens or per_1000_embedding_tokens.",
								},
								"value": schema.Float64Attribute{
									Optional:    true,
									Description: "Cost per metric unit for scalar metrics. Mutually exclusive with `input`.",
								},
								"input": schema.Float64Attribute{
									Optional:    true,
									Description: "Cost per metric unit for input tokens. Mutually exclusive with `value`.",
								},
								"output": schema.Float64Attribute{
									Optional:    true,
									Description: "Cost per metric unit for output tokens.",
								},
								"cache_read": schema.Float64Attribute{
									Optional:    true,
									Description: "Cost per metric unit for cache reads.",
								},
								"cache_write": schema.Float64Attribute{
									Optional:    true,
									Description: "Cost per metric unit for cache writes.",
								},
							},
						},
						"authorized_subjects": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Subjects allowed to access this model, e.g. `user:jane@example.com` or `team:my-team`. " +
								"The control plane rejects `serviceaccount:` subjects here. Unset means account-level access.",
						},
						"api_version": schema.StringAttribute{
							Optional:    true,
							Description: "Azure API version, e.g. 2024-10-21. Required for `azure-openai` models, optional for `azure-foundry`.",
						},
						"foundation_model": schema.StringAttribute{
							Optional:    true,
							Description: "Foundation model identifier behind the deployment, e.g. gpt-4o-2024-11-20 (`azure-openai` only).",
						},
						"deployment_type": schema.StringAttribute{
							Optional:    true,
							Description: fmt.Sprintf("Azure OpenAI deployment type. One of: `%s` (`azure-openai` only).", strings.Join(deploymentTypeValues, "`, `")),
							Validators: []validator.String{
								stringvalidator.OneOf(deploymentTypeValues...),
							},
						},
						"region": schema.StringAttribute{
							Optional:    true,
							Description: "Region of the deployment, e.g. francecentral (`azure-openai`) or a per-model AWS region override (`aws-bedrock`).",
						},
						"api_key": schema.StringAttribute{
							Optional: true,
							Description: "API key of the Azure AI Foundry deployment. Required for `azure-foundry` models, where auth is per model. " +
								"Must be a TrueFoundry secret reference (`tfy-secret://...` or a `...:secret-store:...` FQN path) — " +
								"raw keys are rejected: they would end up in the Terraform state, and per-model API keys are " +
								"not treated as sensitive so that model diffs stay readable in plans.",
						},
						"azure_endpoint": schema.StringAttribute{
							Optional:    true,
							Description: "Azure AI Foundry endpoint URL, e.g. https://{deployment}.{region}.models.ai.azure.com. Required for `azure-foundry` models.",
						},
						"slug": schema.StringAttribute{
							Optional:    true,
							Description: "Unique identifier of a virtual model, usable in place of `<group>/<model>` (`virtual-model` only).",
						},
						"routing": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Routing strategy of a virtual model across target models. Required for `virtual-model` models.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Required:    true,
									Description: fmt.Sprintf("Routing strategy. One of: `%s`.", strings.Join(routingTypeValues, "`, `")),
									Validators: []validator.String{
										stringvalidator.OneOf(routingTypeValues...),
									},
								},
								"targets": schema.SetNestedAttribute{
									Required:    true,
									Description: "Target models routed to. `weight` is required for weight-based routing, `priority` for priority-based routing.",
									Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
									NestedObject: schema.NestedAttributeObject{
										Attributes: routingTargetAttributes(),
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func defaultProviderType() defaults.String {
	return stringdefault.StaticString("openai")
}

func (r *providerAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the attribute combinations required per provider
// type, mirroring the TrueFoundry manifest schemas.
func (r *providerAccountResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config providerAccountModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	providerType := config.ProviderType.ValueString()
	if config.ProviderType.IsNull() || config.ProviderType.IsUnknown() {
		providerType = "openai" // schema default
	}

	requireAccount := func(cond bool, attr, why string) {
		if !cond {
			resp.Diagnostics.AddAttributeError(path.Root(attr), "Invalid provider account configuration",
				fmt.Sprintf("`%s` %s for provider_type %q.", attr, why, providerType))
		}
	}
	// models is a set, so elements have no addressable index; errors are
	// anchored at the attribute and name the offending model in the message.
	requireModel := func(cond bool, name, attr, why string) {
		if !cond {
			resp.Diagnostics.AddAttributeError(path.Root("models"), "Invalid model configuration",
				fmt.Sprintf("`%s` of model %q %s for provider_type %q.", attr, name, why, providerType))
		}
	}

	// The control plane caps account names at 32 characters for every provider
	// type except virtual-model, which allows 64. The schema validator has to
	// permit the wider bound, so the narrow one is enforced here.
	if providerType != "virtual-model" && len(config.Name.ValueString()) > maxAccountNameLen {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid provider account configuration",
			fmt.Sprintf("`name` must be at most %d characters for provider_type %q (only %q allows up to %d).",
				maxAccountNameLen, providerType, "virtual-model", maxVirtualModelNameLen))
	}

	switch providerType {
	case "openai":
		requireAccount(!config.APIKey.IsNull(), "api_key", "is required")
		requireAccount(config.AzureEndpoint.IsNull(), "azure_endpoint", "must not be set")
		for _, m := range config.Models {
			name := m.Name.ValueString()
			requireModel(m.APIVersion.IsNull(), name, "api_version", "must not be set")
			requireModel(m.FoundationModel.IsNull(), name, "foundation_model", "must not be set")
			requireModel(m.DeploymentType.IsNull(), name, "deployment_type", "must not be set")
			requireModel(m.Region.IsNull(), name, "region", "must not be set")
			requireModel(m.APIKey.IsNull(), name, "api_key", "must not be set")
			requireModel(m.AzureEndpoint.IsNull(), name, "azure_endpoint", "must not be set")
		}
	case "azure-openai":
		requireAccount(!config.APIKey.IsNull(), "api_key", "is required")
		requireAccount(!config.AzureEndpoint.IsNull(), "azure_endpoint", "is required")
		requireAccount(config.BaseURL.IsNull(), "base_url", "must not be set")
		for _, m := range config.Models {
			name := m.Name.ValueString()
			requireModel(!m.APIVersion.IsNull(), name, "api_version", "is required")
			requireModel(m.APIKey.IsNull(), name, "api_key", "must not be set")
			requireModel(m.AzureEndpoint.IsNull(), name, "azure_endpoint", "must not be set")
		}
	case "azure-foundry":
		requireAccount(config.APIKey.IsNull(), "api_key", "must not be set (auth is configured per model)")
		requireAccount(config.AzureEndpoint.IsNull(), "azure_endpoint", "must not be set (endpoints are configured per model)")
		requireAccount(config.BaseURL.IsNull(), "base_url", "must not be set")
		for _, m := range config.Models {
			name := m.Name.ValueString()
			requireModel(!m.APIKey.IsNull(), name, "api_key", "is required")
			if !m.APIKey.IsNull() && !m.APIKey.IsUnknown() && !isSecretReference(m.APIKey.ValueString()) {
				requireModel(false, name, "api_key",
					"must be a TrueFoundry secret reference (`tfy-secret://...` or a `...:secret-store:...` FQN path); raw keys are rejected because per-model API keys are shown in plans")
			}
			requireModel(!m.AzureEndpoint.IsNull(), name, "azure_endpoint", "is required")
			requireModel(m.FoundationModel.IsNull(), name, "foundation_model", "must not be set")
			requireModel(m.DeploymentType.IsNull(), name, "deployment_type", "must not be set")
			requireModel(m.Region.IsNull(), name, "region", "must not be set")
		}
	case "virtual-model":
		requireAccount(config.APIKey.IsNull(), "api_key", "must not be set (virtual model groups have no credentials)")
		requireAccount(config.AzureEndpoint.IsNull(), "azure_endpoint", "must not be set")
		requireAccount(config.BaseURL.IsNull(), "base_url", "must not be set")
		for _, m := range config.Models {
			name := m.Name.ValueString()
			requireModel(m.Routing != nil, name, "routing", "is required")
			requireModel(m.ModelID.IsNull(), name, "model_id", "must not be set (virtual models reference targets via routing)")
			requireModel(m.APIVersion.IsNull(), name, "api_version", "must not be set")
			requireModel(m.FoundationModel.IsNull(), name, "foundation_model", "must not be set")
			requireModel(m.DeploymentType.IsNull(), name, "deployment_type", "must not be set")
			requireModel(m.Region.IsNull(), name, "region", "must not be set")
			requireModel(m.APIKey.IsNull(), name, "api_key", "must not be set")
			requireModel(m.AzureEndpoint.IsNull(), name, "azure_endpoint", "must not be set")
			requireModel(m.Cost == nil, name, "cost", "must not be set")
			requireModel(m.AuthorizedSubjects == nil, name, "authorized_subjects", "must not be set")
		}
	case "aws-bedrock":
		requireAccount(!config.Region.IsNull(), "region", "is required")
		requireAccount(config.AzureEndpoint.IsNull(), "azure_endpoint", "must not be set")
		requireAccount(config.BaseURL.IsNull(), "base_url", "must not be set")
		hasAPIKey := !config.APIKey.IsNull()
		hasAccessKey := !config.AccessKeyID.IsNull() || !config.SecretAccessKey.IsNull()
		if hasAPIKey == hasAccessKey {
			resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Invalid provider account configuration",
				"aws-bedrock accounts authenticate with exactly one of `api_key` or `access_key_id`+`secret_access_key`.")
		}
		if hasAccessKey && (config.AccessKeyID.IsNull() || config.SecretAccessKey.IsNull()) {
			resp.Diagnostics.AddAttributeError(path.Root("access_key_id"), "Invalid provider account configuration",
				"`access_key_id` and `secret_access_key` must be set together.")
		}
		for _, m := range config.Models {
			name := m.Name.ValueString()
			requireModel(m.APIVersion.IsNull(), name, "api_version", "must not be set")
			requireModel(m.FoundationModel.IsNull(), name, "foundation_model", "must not be set")
			requireModel(m.DeploymentType.IsNull(), name, "deployment_type", "must not be set")
			requireModel(m.APIKey.IsNull(), name, "api_key", "must not be set")
			requireModel(m.AzureEndpoint.IsNull(), name, "azure_endpoint", "must not be set")
		}
	}

	if providerType != "aws-bedrock" {
		requireAccount(config.Region.IsNull(), "region", "must not be set")
		requireAccount(config.AccessKeyID.IsNull(), "access_key_id", "must not be set")
		requireAccount(config.SecretAccessKey.IsNull(), "secret_access_key", "must not be set")
	}

	// Cross-type model rules: model_id, slug, routing target fields, and cost.
	for _, m := range config.Models {
		name := m.Name.ValueString()
		if providerType != "virtual-model" {
			requireModel(!m.ModelID.IsNull(), name, "model_id", "is required")
			requireModel(m.Slug.IsNull(), name, "slug", "must not be set")
			requireModel(m.Routing == nil, name, "routing", "must not be set")
		}
		if m.Cost != nil {
			costPath := path.Root("models")
			scalar := !m.Cost.Value.IsNull()
			tokenBased := !m.Cost.Input.IsNull()
			if scalar == tokenBased {
				resp.Diagnostics.AddAttributeError(costPath, "Invalid model cost",
					fmt.Sprintf("Model %q: exactly one of `value` (scalar metrics) or `input` (token-based metrics) must be set.", name))
			}
			if scalar && (!m.Cost.Output.IsNull() || !m.Cost.CacheRead.IsNull() || !m.Cost.CacheWrite.IsNull()) {
				resp.Diagnostics.AddAttributeError(costPath, "Invalid model cost",
					fmt.Sprintf("Model %q: `output`, `cache_read`, and `cache_write` require `input` and cannot be combined with `value`.", name))
			}
		}
		if m.Routing == nil {
			continue
		}
		routingType := m.Routing.Type.ValueString()
		for _, target := range m.Routing.Targets {
			tPath := path.Root("models")
			targetName := target.Target.ValueString()
			switch routingType {
			case "weight-based-routing":
				if target.Weight.IsNull() {
					resp.Diagnostics.AddAttributeError(tPath, "Invalid routing target",
						fmt.Sprintf("Model %q, target %q: `weight` is required for weight-based-routing targets.", name, targetName))
				}
				if !target.Priority.IsNull() {
					resp.Diagnostics.AddAttributeError(tPath, "Invalid routing target",
						fmt.Sprintf("Model %q, target %q: `priority` must not be set for weight-based-routing targets.", name, targetName))
				}
			case "priority-based-routing":
				if target.Priority.IsNull() {
					resp.Diagnostics.AddAttributeError(tPath, "Invalid routing target",
						fmt.Sprintf("Model %q, target %q: `priority` is required for priority-based-routing targets.", name, targetName))
				}
				if !target.Weight.IsNull() {
					resp.Diagnostics.AddAttributeError(tPath, "Invalid routing target",
						fmt.Sprintf("Model %q, target %q: `weight` must not be set for priority-based-routing targets.", name, targetName))
				}
			}
		}
	}
}

func (r *providerAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan providerAccountModel
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

func (r *providerAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state providerAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	integrations, err := r.client.ListModelIntegrations(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry model integrations failed", err.Error())
		return
	}

	// The API has no dedicated GET for provider accounts, so the account is
	// read back through its model integrations, each of which embeds the
	// account (id + manifest without the integrations list). An account
	// always has >= 1 model (enforced by schema), so zero integrations means
	// the account is gone. This also serves ImportState, where only `fqn` is
	// populated.
	var account *client.EmbeddedProviderAccount
	remote := map[string]client.ModelIntegration{}
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
		tflog.Info(ctx, "provider account has no model integrations on the control plane; removing from state",
			map[string]any{"fqn": state.FQN.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	if account != nil {
		state.ID = types.StringValue(account.ID)
		state.Name = types.StringValue(account.Manifest.Name)
		state.ProviderType = types.StringValue(strings.TrimPrefix(account.Manifest.Type, "provider-account/"))
		state.BaseURL = stringOrNull(account.Manifest.BaseURL)
		state.AzureEndpoint = stringOrNull(account.Manifest.AzureEndpoint)
		state.Region = stringOrNull(account.Manifest.Region)
		// The control plane returns auth_data as stored. With TrueFoundry
		// secret references this is not secret material and round-trips
		// cleanly; raw secrets are refreshed into state like any other value.
		if auth := account.Manifest.AuthData; auth != nil {
			state.APIKey = stringOrNull(auth.APIKey)
			state.AccessKeyID = stringOrNull(auth.AccessKeyID)
			state.SecretAccessKey = stringOrNull(auth.SecretAccessKey)
		}
	}

	// Rebuild the models set from the remote integrations. Per-model API keys
	// fall back to the value already in state (matched by model name) when the
	// control plane omits them.
	stateAPIKeys := map[string]types.String{}
	for _, m := range state.Models {
		stateAPIKeys[m.Name.ValueString()] = m.APIKey
	}
	names := make([]string, 0, len(remote))
	for name := range remote {
		names = append(names, name)
	}
	sort.Strings(names)
	models := make([]modelModel, 0, len(names))
	for _, name := range names {
		apiKey, ok := stateAPIKeys[name]
		if !ok {
			apiKey = types.StringNull()
		}
		models = append(models, integrationToModel(remote[name], apiKey))
	}
	state.Models = models

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// ImportState imports a provider account by its FQN, e.g.
// "my-tenant:azure-openai:my-account". Read reconstructs everything else
// from the integrations list and its embedded provider account manifest.
func (r *providerAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("fqn"), req, resp)
}

func (r *providerAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan providerAccountModel
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

func (r *providerAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state providerAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, manifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry provider account failed", err.Error())
	}
}

// apply sends the manifest to the control plane and fills the computed
// attributes on the model.
func (r *providerAccountResource) apply(ctx context.Context, plan *providerAccountModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, manifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry provider account manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied provider account manifest", map[string]any{
		"action": result.Action,
		"fqn":    result.Data.FQN,
	})
	plan.ID = types.StringValue(result.Data.ID)
	plan.FQN = types.StringValue(result.Data.FQN)
}

// integrationType returns the integration manifest type for a provider type,
// e.g. "integration/model/azure-openai". Virtual model groups use the
// "virtual" suffix and aws-bedrock uses "bedrock".
func integrationType(providerType string) string {
	switch providerType {
	case "virtual-model":
		return "integration/model/virtual"
	case "aws-bedrock":
		return "integration/model/bedrock"
	default:
		return "integration/model/" + providerType
	}
}

func manifestFromModel(m *providerAccountModel) client.ProviderAccountManifest {
	providerType := m.ProviderType.ValueString()
	manifest := client.ProviderAccountManifest{
		Type:          "provider-account/" + providerType,
		Name:          m.Name.ValueString(),
		BaseURL:       m.BaseURL.ValueString(),
		AzureEndpoint: m.AzureEndpoint.ValueString(),
		Region:        m.Region.ValueString(),
		Integrations:  []client.ModelIntegration{},
	}
	if !m.APIKey.IsNull() {
		manifest.AuthData = &client.APIKeyAuth{Type: "api-key", APIKey: m.APIKey.ValueString()}
	}
	if !m.AccessKeyID.IsNull() {
		manifest.AuthData = &client.APIKeyAuth{
			Type:            "access-key-based",
			AccessKeyID:     m.AccessKeyID.ValueString(),
			SecretAccessKey: m.SecretAccessKey.ValueString(),
		}
	}
	for _, model := range m.Models {
		integration := client.ModelIntegration{
			Type:            integrationType(providerType),
			Name:            model.Name.ValueString(),
			ModelID:         model.ModelID.ValueString(),
			APIVersion:      model.APIVersion.ValueString(),
			FoundationModel: model.FoundationModel.ValueString(),
			DeploymentType:  model.DeploymentType.ValueString(),
			Region:          model.Region.ValueString(),
			AzureEndpoint:   model.AzureEndpoint.ValueString(),
			Slug:            model.Slug.ValueString(),
		}
		if !model.APIKey.IsNull() {
			integration.AuthData = &client.APIKeyAuth{Type: "api-key", APIKey: model.APIKey.ValueString()}
		}
		if model.Routing != nil {
			routing := &client.RoutingConfig{
				Type:               model.Routing.Type.ValueString(),
				LoadBalanceTargets: []client.RoutingTarget{},
			}
			for _, t := range model.Routing.Targets {
				routing.LoadBalanceTargets = append(routing.LoadBalanceTargets, routingTargetFromModel(t))
			}
			integration.RoutingConfig = routing
		}
		if model.Cost != nil {
			integration.Cost = &client.ModelCostMetric{
				Metric: model.Cost.Metric.ValueString(),
				Value: client.CostValue{
					Amount:     float64OrNil(model.Cost.Value),
					Input:      float64OrNil(model.Cost.Input),
					Output:     float64OrNil(model.Cost.Output),
					CacheRead:  float64OrNil(model.Cost.CacheRead),
					CacheWrite: float64OrNil(model.Cost.CacheWrite),
				},
			}
		}
		for _, s := range model.AuthorizedSubjects {
			integration.AuthorizedSubjects = append(integration.AuthorizedSubjects, s.ValueString())
		}
		for _, t := range model.ModelTypes {
			integration.ModelTypes = append(integration.ModelTypes, t.ValueString())
		}
		manifest.Integrations = append(manifest.Integrations, integration)
	}
	return manifest
}

// integrationToModel converts a remote model integration manifest to state.
// Per-model auth (azure-foundry) is taken from the remote manifest when the
// control plane returns it (it returns auth_data as stored); stateAPIKey is
// the fallback for control planes that omit it.
func integrationToModel(manifest client.ModelIntegration, stateAPIKey types.String) modelModel {
	apiKey := stateAPIKey
	if manifest.AuthData != nil && manifest.AuthData.APIKey != "" {
		apiKey = types.StringValue(manifest.AuthData.APIKey)
	}
	m := modelModel{
		Name:            types.StringValue(manifest.Name),
		ModelID:         stringOrNull(manifest.ModelID),
		APIVersion:      stringOrNull(manifest.APIVersion),
		FoundationModel: stringOrNull(manifest.FoundationModel),
		DeploymentType:  stringOrNull(manifest.DeploymentType),
		Region:          stringOrNull(manifest.Region),
		AzureEndpoint:   stringOrNull(manifest.AzureEndpoint),
		Slug:            stringOrNull(manifest.Slug),
		APIKey:          apiKey,
	}
	if manifest.RoutingConfig != nil {
		routing := &routingModel{Type: types.StringValue(manifest.RoutingConfig.Type)}
		for _, t := range manifest.RoutingConfig.LoadBalanceTargets {
			routing.Targets = append(routing.Targets, routingTargetToModel(t))
		}
		m.Routing = routing
	}
	if manifest.Cost != nil {
		m.Cost = &costModel{
			Metric:     types.StringValue(manifest.Cost.Metric),
			Value:      float64OrNull(manifest.Cost.Value.Amount),
			Input:      float64OrNull(manifest.Cost.Value.Input),
			Output:     float64OrNull(manifest.Cost.Value.Output),
			CacheRead:  float64OrNull(manifest.Cost.Value.CacheRead),
			CacheWrite: float64OrNull(manifest.Cost.Value.CacheWrite),
		}
	}
	for _, s := range manifest.AuthorizedSubjects {
		m.AuthorizedSubjects = append(m.AuthorizedSubjects, types.StringValue(s))
	}
	for _, t := range manifest.ModelTypes {
		m.ModelTypes = append(m.ModelTypes, types.StringValue(t))
	}
	return m
}

// isSecretReference reports whether s is a TrueFoundry secret reference —
// either a system secret store URI (tfy-secret://tenant:account:name) or a
// secret store integration FQN path
// (tenant:hashicorp:account:secret-store:store::path/value).
func isSecretReference(s string) bool {
	return strings.HasPrefix(s, "tfy-secret://") || strings.Contains(s, ":secret-store:")
}

func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func float64OrNil(v types.Float64) *float64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	f := v.ValueFloat64()
	return &f
}

func float64OrNull(v *float64) types.Float64 {
	if v == nil {
		return types.Float64Null()
	}
	return types.Float64Value(*v)
}

func int64OrNil(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	i := v.ValueInt64()
	return &i
}

func int64OrNull(v *int64) types.Int64 {
	if v == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*v)
}
