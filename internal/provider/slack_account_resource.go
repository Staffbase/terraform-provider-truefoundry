package provider

import (
	"context"
	"fmt"
	"sort"

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
	_ resource.Resource                   = (*slackAccountResource)(nil)
	_ resource.ResourceWithConfigure      = (*slackAccountResource)(nil)
	_ resource.ResourceWithValidateConfig = (*slackAccountResource)(nil)
	_ resource.ResourceWithImportState    = (*slackAccountResource)(nil)
)

func newSlackAccountResource() resource.Resource {
	return &slackAccountResource{}
}

// slackAccountResource manages a Slack provider account carrying notification
// channel integrations via the manifest-based apply/delete API.
type slackAccountResource struct {
	client *client.Client
}

type slackAccountModel struct {
	ID       types.String        `tfsdk:"id"`
	FQN      types.String        `tfsdk:"fqn"`
	Name     types.String        `tfsdk:"name"`
	Webhooks []slackWebhookModel `tfsdk:"webhooks"`
	Bots     []slackBotModel     `tfsdk:"bots"`
}

type slackWebhookModel struct {
	Name               types.String   `tfsdk:"name"`
	WebhookURL         types.String   `tfsdk:"webhook_url"`
	AuthorizedSubjects []types.String `tfsdk:"authorized_subjects"`
}

type slackBotModel struct {
	Name               types.String   `tfsdk:"name"`
	BotToken           types.String   `tfsdk:"bot_token"`
	AuthorizedSubjects []types.String `tfsdk:"authorized_subjects"`
}

func (r *slackAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_slack_account"
}

func (r *slackAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	authorizedSubjects := schema.ListAttribute{
		Optional:    true,
		ElementType: types.StringType,
		Description: "Subjects allowed to use this notification channel, as `<user_type>:<username>` FQNs.",
	}
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry Slack provider account carrying notification channel integrations " +
			"(incoming webhooks and bot tokens), applied as a `provider-account/slack` manifest via `PUT /api/svc/v1/apply`. " +
			"Existing accounts can be imported by their FQN, e.g. `terraform import truefoundry_slack_account.example my-tenant:slack:my-account`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the Slack account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed:      true,
				Description:   "Fully qualified name of the Slack account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the Slack account (3-32 characters, lowercase alphanumeric and hyphens). Changing it replaces the account.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(3, 32),
					stringvalidator.RegexMatches(nameRegexp, "must be lowercase alphanumeric with hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"webhooks": schema.ListNestedAttribute{
				Optional:    true,
				Description: "Slack incoming webhook notification channels. At least one webhook or bot is required.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name identifying this notification channel.",
							Validators:  []validator.String{stringvalidator.LengthBetween(1, 64)},
						},
						"webhook_url": schema.StringAttribute{
							Required:    true,
							Sensitive:   true,
							Description: "Slack incoming webhook URL. Accepts TrueFoundry secret references (recommended); the control plane returns it as stored, so drift is detected.",
						},
						"authorized_subjects": authorizedSubjects,
					},
				},
			},
			"bots": schema.ListNestedAttribute{
				Optional:    true,
				Description: "Slack bot notification channels. At least one webhook or bot is required.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name identifying this notification channel.",
							Validators:  []validator.String{stringvalidator.LengthBetween(1, 64)},
						},
						"bot_token": schema.StringAttribute{
							Required:    true,
							Sensitive:   true,
							Description: "Slack bot token. Accepts TrueFoundry secret references (recommended); the control plane returns it as stored, so drift is detected.",
						},
						"authorized_subjects": authorizedSubjects,
					},
				},
			},
		},
	}
}

func (r *slackAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig requires at least one notification channel: the account is
// read back through its integrations, so an empty account is
// indistinguishable from a deleted one.
func (r *slackAccountResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config slackAccountModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(config.Webhooks)+len(config.Bots) == 0 {
		resp.Diagnostics.AddAttributeError(path.Root("webhooks"), "Invalid Slack account configuration",
			"At least one webhook or bot notification channel is required.")
	}
}

func (r *slackAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan slackAccountModel
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

func (r *slackAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state slackAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	integrations, err := r.client.ListNotificationChannelIntegrations(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry notification channel integrations failed", err.Error())
		return
	}

	var account *client.EmbeddedProviderAccount
	remote := map[string]client.SlackNotificationChannel{}
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
		tflog.Info(ctx, "slack account has no notification channels on the control plane; removing from state",
			map[string]any{"fqn": state.FQN.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	if account != nil {
		state.ID = types.StringValue(account.ID)
		state.Name = types.StringValue(account.Manifest.Name)
	}

	// Keep the configured order, append unknown channels sorted by name.
	known := map[string]bool{}
	for _, w := range state.Webhooks {
		known[w.Name.ValueString()] = true
	}
	for _, b := range state.Bots {
		known[b.Name.ValueString()] = true
	}
	var webhooks []slackWebhookModel
	var bots []slackBotModel
	appendChannel := func(manifest client.SlackNotificationChannel) {
		switch manifest.Type {
		case "integration/notification-channel/slack-bot":
			bots = append(bots, slackBotToModel(manifest))
		default:
			webhooks = append(webhooks, slackWebhookToModel(manifest))
		}
	}
	for _, w := range state.Webhooks {
		if manifest, ok := remote[w.Name.ValueString()]; ok {
			delete(remote, w.Name.ValueString())
			appendChannel(manifest)
		}
	}
	for _, b := range state.Bots {
		if manifest, ok := remote[b.Name.ValueString()]; ok {
			delete(remote, b.Name.ValueString())
			appendChannel(manifest)
		}
	}
	extra := make([]string, 0, len(remote))
	for name := range remote {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	for _, name := range extra {
		appendChannel(remote[name])
	}
	state.Webhooks = webhooks
	state.Bots = bots

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *slackAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan slackAccountModel
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

func (r *slackAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state slackAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, slackManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry Slack account failed", err.Error())
	}
}

// ImportState imports a Slack account by its FQN, e.g. "my-tenant:slack:my-account".
func (r *slackAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("fqn"), req, resp)
}

func (r *slackAccountResource) apply(ctx context.Context, plan *slackAccountModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, slackManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry Slack account manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied slack account manifest", map[string]any{
		"action": result.Action,
		"fqn":    result.Data.FQN,
	})
	plan.ID = types.StringValue(result.Data.ID)
	plan.FQN = types.StringValue(result.Data.FQN)
}

func slackManifestFromModel(m *slackAccountModel) client.SlackAccountManifest {
	manifest := client.SlackAccountManifest{
		Type:         "provider-account/slack",
		Name:         m.Name.ValueString(),
		Integrations: []client.SlackNotificationChannel{},
	}
	for _, w := range m.Webhooks {
		manifest.Integrations = append(manifest.Integrations, client.SlackNotificationChannel{
			Type:               "integration/notification-channel/slack-webhook",
			Name:               w.Name.ValueString(),
			AuthData:           &client.APIKeyAuth{Type: "slack-webhook", WebhookURL: w.WebhookURL.ValueString()},
			AuthorizedSubjects: subjectsFromModel(w.AuthorizedSubjects),
		})
	}
	for _, b := range m.Bots {
		manifest.Integrations = append(manifest.Integrations, client.SlackNotificationChannel{
			Type:               "integration/notification-channel/slack-bot",
			Name:               b.Name.ValueString(),
			AuthData:           &client.APIKeyAuth{Type: "slack-bot", BotToken: b.BotToken.ValueString()},
			AuthorizedSubjects: subjectsFromModel(b.AuthorizedSubjects),
		})
	}
	return manifest
}

func slackWebhookToModel(manifest client.SlackNotificationChannel) slackWebhookModel {
	w := slackWebhookModel{
		Name:               types.StringValue(manifest.Name),
		AuthorizedSubjects: subjectsToModel(manifest.AuthorizedSubjects),
	}
	if manifest.AuthData != nil {
		w.WebhookURL = types.StringValue(manifest.AuthData.WebhookURL)
	}
	return w
}

func slackBotToModel(manifest client.SlackNotificationChannel) slackBotModel {
	b := slackBotModel{
		Name:               types.StringValue(manifest.Name),
		AuthorizedSubjects: subjectsToModel(manifest.AuthorizedSubjects),
	}
	if manifest.AuthData != nil {
		b.BotToken = types.StringValue(manifest.AuthData.BotToken)
	}
	return b
}

func subjectsFromModel(subjects []types.String) []string {
	var out []string
	for _, s := range subjects {
		out = append(out, s.ValueString())
	}
	return out
}

func subjectsToModel(subjects []string) []types.String {
	var out []types.String
	for _, s := range subjects {
		out = append(out, types.StringValue(s))
	}
	return out
}
