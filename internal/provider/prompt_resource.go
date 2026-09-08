package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
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
	_ resource.Resource                = (*promptResource)(nil)
	_ resource.ResourceWithConfigure   = (*promptResource)(nil)
	_ resource.ResourceWithImportState = (*promptResource)(nil)
)

// promptMessageRoleValues are the message roles supported by this resource.
// Tool messages (tool call results) belong to conversations, not templates,
// and are not modeled.
var promptMessageRoleValues = []string{"system", "developer", "user", "assistant"}

func newPromptResource() resource.Resource {
	return &promptResource{}
}

// promptResource manages a prompt ("chat_prompt" manifests) via the
// versioned PUT /api/svc/v1/prompt-versions and GET/DELETE
// /api/svc/v1/prompts endpoints.
type promptResource struct {
	client *client.Client
}

type promptResourceModel struct {
	ID                 types.String            `tfsdk:"id"`
	FQN                types.String            `tfsdk:"fqn"`
	Name               types.String            `tfsdk:"name"`
	MlRepo             types.String            `tfsdk:"ml_repo"`
	Description        types.String            `tfsdk:"description"`
	VersionAlias       types.String            `tfsdk:"version_alias"`
	Messages           []promptMessageModel    `tfsdk:"messages"`
	Variables          map[string]types.String `tfsdk:"variables"`
	ModelConfiguration *promptModelConfigModel `tfsdk:"model_configuration"`
}

type promptMessageModel struct {
	Role    types.String `tfsdk:"role"`
	Content types.String `tfsdk:"content"`
	Name    types.String `tfsdk:"name"`
}

type promptModelConfigModel struct {
	Provider        types.String           `tfsdk:"provider"`
	Model           types.String           `tfsdk:"model"`
	Parameters      *promptParametersModel `tfsdk:"parameters"`
	ExtraParameters jsontypes.Normalized   `tfsdk:"extra_parameters"`
}

type promptParametersModel struct {
	MaxTokens   types.Int64    `tfsdk:"max_tokens"`
	Temperature types.Float64  `tfsdk:"temperature"`
	TopK        types.Int64    `tfsdk:"top_k"`
	TopP        types.Float64  `tfsdk:"top_p"`
	Stop        []types.String `tfsdk:"stop"`
}

func (r *promptResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_prompt"
}

func (r *promptResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a prompt (`chat_prompt` manifest) via `PUT /api/svc/v1/prompt-versions` — a versioned " +
			"message template in an ML repo (`truefoundry_ml_repo`). Every update creates a new immutable prompt " +
			"version on the control plane. The advanced manifest blocks (tools, MCP servers, guardrails, response " +
			"format, routing, caching, sub-agents) are not supported and are dropped from the new version when a " +
			"prompt is updated with this resource. Existing prompts can be imported by their name, e.g. " +
			"`terraform import truefoundry_prompt.example my-prompt`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the prompt.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed: true,
				Description: "Fully qualified name of the prompt, e.g. chat_prompt:my-tenant/my-repo/my-prompt. " +
					"A specific version is referenced by appending its number, e.g. `\"${truefoundry_prompt.example.fqn}:1\"`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the prompt (must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens). Changing it replaces the prompt.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.RegexMatches(nameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ml_repo": schema.StringAttribute{
				Required:      true,
				Description:   "Name of the ML repo the prompt lives in. It must exist on the tenant. Changing it replaces the prompt.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Human-readable description of what the prompt is for.",
			},
			"version_alias": schema.StringAttribute{
				Optional:    true,
				Description: "Alias for the prompt version created by this apply, e.g. v1.0.0.",
			},
			"messages": schema.ListNestedAttribute{
				Required:    true,
				Description: "The message template, in order. Message contents may reference variables as {{name}}.",
				Validators:  []validator.List{listvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"role": schema.StringAttribute{
							Required:    true,
							Description: "Role of the message: `system`, `developer`, `user`, or `assistant`.",
							Validators:  []validator.String{stringvalidator.OneOf(promptMessageRoleValues...)},
						},
						"content": schema.StringAttribute{
							Required:    true,
							Description: "Text content of the message.",
						},
						"name": schema.StringAttribute{
							Optional:    true,
							Description: "Optional participant name to disambiguate multiple participants with the same role.",
						},
					},
				},
			},
			"variables": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Template variables usable in message contents as {{name}}, mapped to their default values.",
			},
			"model_configuration": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "The gateway model the prompt is bound to. Omitted means callers pick the model themselves.",
				Attributes: map[string]schema.Attribute{
					"provider": schema.StringAttribute{
						Required:    true,
						Description: "Gateway provider account name, e.g. my-account.",
						Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
					},
					"model": schema.StringAttribute{
						Required:    true,
						Description: "Model name within the provider account, e.g. gpt-5-mini.",
						Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
					},
					"parameters": schema.SingleNestedAttribute{
						Optional:    true,
						Description: "Inference parameters passed to the model.",
						Attributes: map[string]schema.Attribute{
							"max_tokens": schema.Int64Attribute{
								Optional:    true,
								Description: "Maximum number of tokens the model may generate.",
							},
							"temperature": schema.Float64Attribute{
								Optional:    true,
								Description: "Sampling temperature.",
							},
							"top_k": schema.Int64Attribute{
								Optional:    true,
								Description: "Top-k sampling cutoff.",
							},
							"top_p": schema.Float64Attribute{
								Optional:    true,
								Description: "Nucleus sampling probability mass.",
							},
							"stop": schema.ListAttribute{
								Optional:    true,
								ElementType: types.StringType,
								Description: "Sequences at which the model stops generating.",
								Validators:  []validator.List{listvalidator.SizeAtLeast(1)},
							},
						},
					},
					"extra_parameters": schema.StringAttribute{
						Optional:    true,
						CustomType:  jsontypes.NormalizedType{},
						Description: "Provider-specific extra parameters as a JSON object string, e.g. via jsonencode().",
					},
				},
			},
		},
	}
}

func (r *promptResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *promptResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan promptResourceModel
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

func (r *promptResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state promptResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	prompt, found, err := getPromptByName(ctx, r.client, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry prompt failed", err.Error())
		return
	}
	if !found {
		tflog.Info(ctx, "prompt not found on the control plane; removing from state",
			map[string]any{"name": state.Name.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if prompt.LatestVersion == nil {
		resp.Diagnostics.AddError("Reading TrueFoundry prompt failed",
			fmt.Sprintf("prompt %s has no latest version details in the list response", prompt.FQN))
		return
	}
	manifest := prompt.LatestVersion.Manifest

	state.ID = types.StringValue(prompt.ID)
	state.FQN = types.StringValue(prompt.FQN)
	state.Name = types.StringValue(manifest.Name)
	state.MlRepo = types.StringValue(manifest.MlRepo)
	state.Description = stringOrNull(manifest.Description)
	state.VersionAlias = stringOrNull(manifest.VersionAlias)
	state.Messages = nil
	for i, message := range manifest.Messages {
		model, err := promptMessageToModel(message)
		if err != nil {
			resp.Diagnostics.AddError("Reading TrueFoundry prompt failed",
				fmt.Sprintf("message %d of %s: %s", i, prompt.FQN, err))
			return
		}
		state.Messages = append(state.Messages, model)
	}
	state.Variables = stringMapToModel(manifest.Variables)
	state.ModelConfiguration = promptModelConfigToModel(manifest.ModelConfiguration)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *promptResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan promptResourceModel
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

func (r *promptResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state promptResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeletePrompt(ctx, state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry prompt failed", err.Error())
	}
}

// ImportState imports a prompt by its name, e.g. "my-prompt".
func (r *promptResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// apply sends the manifest; the response's new prompt version carries the
// prompt ID and the versioned FQN.
func (r *promptResource) apply(ctx context.Context, plan *promptResourceModel, diags *diag.Diagnostics) {
	version, err := r.client.ApplyPrompt(ctx, promptManifestFromModel(plan))
	if err != nil {
		diags.AddError("Applying TrueFoundry prompt manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied prompt manifest", map[string]any{"fqn": version.FQN})
	plan.ID = types.StringValue(version.PromptID)
	plan.FQN = types.StringValue(agentFQNFromVersionFQN(version.FQN))
}

// getPromptByName fetches a single prompt by name via the list endpoint.
func getPromptByName(ctx context.Context, c *client.Client, name string) (*client.Prompt, bool, error) {
	prompts, err := c.ListPrompts(ctx, name)
	if err != nil {
		return nil, false, err
	}
	if len(prompts) == 0 {
		return nil, false, nil
	}
	return &prompts[0], true, nil
}

func promptManifestFromModel(m *promptResourceModel) client.PromptManifest {
	manifest := client.PromptManifest{
		Type:         "chat_prompt",
		Name:         m.Name.ValueString(),
		MlRepo:       m.MlRepo.ValueString(),
		Metadata:     map[string]any{},
		Description:  m.Description.ValueString(),
		VersionAlias: m.VersionAlias.ValueString(),
		Variables:    stringMapFromModel(m.Variables),
	}
	for _, message := range m.Messages {
		content, _ := json.Marshal(message.Content.ValueString())
		manifest.Messages = append(manifest.Messages, client.PromptMessage{
			Role:    message.Role.ValueString(),
			Content: content,
			Name:    message.Name.ValueString(),
		})
	}
	if c := m.ModelConfiguration; c != nil {
		config := &client.PromptModelConfiguration{
			Provider: c.Provider.ValueString(),
			Model:    c.Model.ValueString(),
		}
		if p := c.Parameters; p != nil {
			config.Parameters = &client.PromptParameters{
				MaxTokens:   int64OrNil(p.MaxTokens),
				Temperature: float64OrNil(p.Temperature),
				TopK:        int64OrNil(p.TopK),
				TopP:        float64OrNil(p.TopP),
				Stop:        stringListFromModel(p.Stop),
			}
		}
		if !c.ExtraParameters.IsNull() {
			config.ExtraParameters = []byte(c.ExtraParameters.ValueString())
		}
		manifest.ModelConfiguration = config
	}
	return manifest
}

// promptMessageToModel converts a wire message. Only plain-string contents
// are supported; content part lists and blob storage references fail.
func promptMessageToModel(message client.PromptMessage) (promptMessageModel, error) {
	var content string
	if err := json.Unmarshal(message.Content, &content); err != nil {
		return promptMessageModel{}, fmt.Errorf("only plain-string message contents are supported, got %s", message.Content)
	}
	return promptMessageModel{
		Role:    types.StringValue(message.Role),
		Content: types.StringValue(content),
		Name:    stringOrNull(message.Name),
	}, nil
}

func promptModelConfigToModel(config *client.PromptModelConfiguration) *promptModelConfigModel {
	if config == nil {
		return nil
	}
	m := &promptModelConfigModel{
		Provider:        types.StringValue(config.Provider),
		Model:           types.StringValue(config.Model),
		ExtraParameters: jsontypes.NewNormalizedNull(),
	}
	if p := config.Parameters; p != nil {
		m.Parameters = &promptParametersModel{
			MaxTokens:   int64OrNull(p.MaxTokens),
			Temperature: float64OrNull(p.Temperature),
			TopK:        int64OrNull(p.TopK),
			TopP:        float64OrNull(p.TopP),
			Stop:        stringListToModel(p.Stop),
		}
	}
	if len(config.ExtraParameters) > 0 && string(config.ExtraParameters) != "null" {
		m.ExtraParameters = jsontypes.NewNormalizedValue(string(config.ExtraParameters))
	}
	return m
}
