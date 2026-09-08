package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

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
	_ resource.Resource                   = (*customEndpointAccountResource)(nil)
	_ resource.ResourceWithConfigure      = (*customEndpointAccountResource)(nil)
	_ resource.ResourceWithValidateConfig = (*customEndpointAccountResource)(nil)
	_ resource.ResourceWithImportState    = (*customEndpointAccountResource)(nil)
)

// endpointTypeValues are the supported custom endpoint service types.
var endpointTypeValues = []string{"azure-speech-service", "other"}

// endpointRoutingTypeValues are the custom endpoint routing strategies.
var endpointRoutingTypeValues = []string{"weight", "priority"}

func newCustomEndpointAccountResource() resource.Resource {
	return &customEndpointAccountResource{}
}

// customEndpointAccountResource manages an AI Gateway custom endpoint
// provider account (arbitrary HTTP services proxied by the gateway) via the
// manifest-based apply/delete API.
type customEndpointAccountResource struct {
	client *client.Client
}

type customEndpointAccountModel struct {
	ID            types.String        `tfsdk:"id"`
	FQN           types.String        `tfsdk:"fqn"`
	Name          types.String        `tfsdk:"name"`
	EndpointType  types.String        `tfsdk:"endpoint_type"`
	RoutingType   types.String        `tfsdk:"routing_type"`
	Slug          types.String        `tfsdk:"slug"`
	Collaborators []collaboratorModel `tfsdk:"collaborators"`
	Endpoints     []endpointModel     `tfsdk:"endpoints"`
}

type endpointModel struct {
	Name               types.String            `tfsdk:"name"`
	BaseURL            types.String            `tfsdk:"base_url"`
	AuthHeaders        map[string]types.String `tfsdk:"auth_headers"`
	Headers            map[string]types.String `tfsdk:"headers"`
	AuthorizedSubjects []types.String          `tfsdk:"authorized_subjects"`
	TLS                *endpointTLSModel       `tfsdk:"tls"`
	Loadbalancing      *loadbalancingModel     `tfsdk:"loadbalancing"`
}

type endpointTLSModel struct {
	RejectUnauthorized types.Bool   `tfsdk:"reject_unauthorized"`
	CACert             types.String `tfsdk:"ca_cert"`
}

type loadbalancingModel struct {
	Weight              types.Float64           `tfsdk:"weight"`
	Priority            types.Int64             `tfsdk:"priority"`
	FallbackStatusCodes []types.String          `tfsdk:"fallback_status_codes"`
	FallbackCandidate   types.Bool              `tfsdk:"fallback_candidate"`
	MetadataMatch       map[string]types.String `tfsdk:"metadata_match"`
}

func (r *customEndpointAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_endpoint_account"
}

func (r *customEndpointAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a TrueFoundry AI Gateway custom endpoint provider account: arbitrary HTTP services " +
			"proxied by the gateway at /proxy-api/{account-name}/{endpoint-name}, optionally load balanced. " +
			"Applied as a `provider-account/custom-endpoint` manifest via `PUT /api/svc/v1/apply`. " +
			"Existing accounts can be imported by their FQN, e.g. `terraform import truefoundry_custom_endpoint_account.example my-tenant:custom-endpoint:my-account`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the custom endpoint account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fqn": schema.StringAttribute{
				Computed:      true,
				Description:   "Fully qualified name of the custom endpoint account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the custom endpoint account (3-32 characters; must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens). Changing it replaces the account.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(minNameLen, maxAccountNameLen),
					stringvalidator.RegexMatches(nameRegexp, "must start with a lowercase letter, end with a letter or digit, and contain only lowercase letters, digits and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"endpoint_type": schema.StringAttribute{
				Optional:    true,
				Description: fmt.Sprintf("Type of service behind the endpoints, used for tracking. One of: `%s`.", strings.Join(endpointTypeValues, "`, `")),
				Validators: []validator.String{
					stringvalidator.OneOf(endpointTypeValues...),
				},
			},
			"routing_type": schema.StringAttribute{
				Optional: true,
				Description: fmt.Sprintf("Load balancing across the endpoints: `%s`. Unset means each endpoint is only reachable "+
					"individually at /proxy-api/{account-name}/{endpoint-name}.", strings.Join(endpointRoutingTypeValues, "` or `")),
				Validators: []validator.String{
					stringvalidator.OneOf(endpointRoutingTypeValues...),
				},
			},
			"slug": schema.StringAttribute{
				Optional:    true,
				Description: "Identifier of the load balancer, reachable at /proxy-api/{account-name}/{slug}/{upstream-path}. Required when `routing_type` is set.",
			},
			"collaborators": schema.SetNestedAttribute{
				Optional:    true,
				Description: "Subjects with a role on this custom endpoint account.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"subject": schema.StringAttribute{
							Required:    true,
							Description: "Subject FQN, e.g. user:jane@example.com or team:everyone.",
						},
						"role_id": schema.StringAttribute{
							Required:    true,
							Description: "Role to assign, e.g. provider-account-access.",
						},
					},
				},
			},
			"endpoints": schema.ListNestedAttribute{
				Required:      true,
				Description:   "Upstream endpoints proxied through this account.",
				Validators:    []validator.List{listvalidator.SizeAtLeast(1)},
				PlanModifiers: []planmodifier.List{orderInsensitive()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Name identifying this endpoint.",
							Validators: []validator.String{
								stringvalidator.LengthBetween(1, 64),
							},
						},
						"base_url": schema.StringAttribute{
							Required:    true,
							Description: "Target base URL requests are proxied to, e.g. https://my-service.example.com/v1.",
						},
						"auth_headers": schema.MapAttribute{
							Optional:    true,
							Sensitive:   true,
							ElementType: types.StringType,
							Description: "Authentication headers sent to the upstream on every request, e.g. {\"Ocp-Apim-Subscription-Key\" = \"...\"}. " +
								"Values accept TrueFoundry secret references (recommended); the control plane returns them as stored, so drift is detected.",
						},
						"headers": schema.MapAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Non-secret custom headers forwarded to the upstream with every request.",
						},
						"authorized_subjects": schema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
							Description: "Subjects allowed to access this endpoint, as `<user_type>:<username>` FQNs.",
						},
						"tls": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "TLS verification settings for the upstream connection.",
							Attributes: map[string]schema.Attribute{
								"reject_unauthorized": schema.BoolAttribute{
									Required:    true,
									Description: "Reject connections not authorized by the configured CAs.",
								},
								"ca_cert": schema.StringAttribute{
									Optional:    true,
									Description: "Custom CA certificate(s) used for TLS verification.",
								},
							},
						},
						"loadbalancing": schema.SingleNestedAttribute{
							Optional:    true,
							Description: "Per-endpoint load balancing settings. Requires `routing_type` on the account; `weight` for weight routing (weights must sum to 100), `priority` for priority routing.",
							Attributes: map[string]schema.Attribute{
								"weight": schema.Float64Attribute{
									Optional:    true,
									Description: "Relative routing weight; weights across all endpoints must sum to 100 (weight routing).",
								},
								"priority": schema.Int64Attribute{
									Optional:    true,
									Description: "Priority of the endpoint; 0 is highest (priority routing).",
								},
								"fallback_status_codes": schema.SetAttribute{
									Optional:    true,
									ElementType: types.StringType,
									Description: "Status codes for which the request falls back to other endpoints.",
								},
								"fallback_candidate": schema.BoolAttribute{
									Optional:    true,
									Description: "Whether this endpoint may serve as a fallback for other endpoints.",
								},
								"metadata_match": schema.MapAttribute{
									Optional:    true,
									ElementType: types.StringType,
									Description: "Metadata key-value pairs that must match incoming request metadata headers for this endpoint to be considered.",
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *customEndpointAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the routing/loadbalancing combinations.
func (r *customEndpointAccountResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config customEndpointAccountModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	routingType := config.RoutingType.ValueString()
	if routingType != "" && config.Slug.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("slug"), "Invalid custom endpoint account configuration",
			"`slug` is required when `routing_type` is set: the load balancer is addressed as /proxy-api/{account-name}/{slug}/.")
	}

	// endpoints is a set, so elements have no addressable index; errors are
	// anchored at the attribute and name the offending endpoint in the message.
	for _, e := range config.Endpoints {
		endpointsPath := path.Root("endpoints")
		name := e.Name.ValueString()
		if routingType == "" {
			if e.Loadbalancing != nil {
				resp.Diagnostics.AddAttributeError(endpointsPath, "Invalid endpoint configuration",
					fmt.Sprintf("Endpoint %q: `loadbalancing` requires `routing_type` to be set on the account.", name))
			}
			continue
		}
		if e.Loadbalancing == nil {
			resp.Diagnostics.AddAttributeError(endpointsPath, "Invalid endpoint configuration",
				fmt.Sprintf("Endpoint %q: `loadbalancing` is required on every endpoint when routing_type is %q.", name, routingType))
			continue
		}
		switch routingType {
		case "weight":
			if e.Loadbalancing.Weight.IsNull() {
				resp.Diagnostics.AddAttributeError(endpointsPath, "Invalid endpoint configuration",
					fmt.Sprintf("Endpoint %q: `weight` is required for weight routing.", name))
			}
			if !e.Loadbalancing.Priority.IsNull() {
				resp.Diagnostics.AddAttributeError(endpointsPath, "Invalid endpoint configuration",
					fmt.Sprintf("Endpoint %q: `priority` must not be set for weight routing.", name))
			}
		case "priority":
			if e.Loadbalancing.Priority.IsNull() {
				resp.Diagnostics.AddAttributeError(endpointsPath, "Invalid endpoint configuration",
					fmt.Sprintf("Endpoint %q: `priority` is required for priority routing.", name))
			}
			if !e.Loadbalancing.Weight.IsNull() {
				resp.Diagnostics.AddAttributeError(endpointsPath, "Invalid endpoint configuration",
					fmt.Sprintf("Endpoint %q: `weight` must not be set for priority routing.", name))
			}
		}
	}
}

func (r *customEndpointAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan customEndpointAccountModel
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

func (r *customEndpointAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state customEndpointAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Custom endpoint integrations are listed as "model" integrations.
	integrations, err := r.client.ListModelIntegrations(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry custom endpoint integrations failed", err.Error())
		return
	}

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
		tflog.Info(ctx, "custom endpoint account has no endpoints on the control plane; removing from state",
			map[string]any{"fqn": state.FQN.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	if account != nil {
		state.ID = types.StringValue(account.ID)
		state.Name = types.StringValue(account.Manifest.Name)
		state.EndpointType = stringOrNull(account.Manifest.EndpointType)
		state.RoutingType = stringOrNull(account.Manifest.RoutingType)
		state.Slug = stringOrNull(account.Manifest.Slug)
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
	var endpoints []endpointModel
	for _, e := range state.Endpoints {
		manifest, ok := remote[e.Name.ValueString()]
		if !ok {
			continue
		}
		delete(remote, e.Name.ValueString())
		endpoints = append(endpoints, endpointToModel(manifest))
	}
	extra := make([]string, 0, len(remote))
	for name := range remote {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	for _, name := range extra {
		endpoints = append(endpoints, endpointToModel(remote[name]))
	}
	state.Endpoints = endpoints

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *customEndpointAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan customEndpointAccountModel
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

func (r *customEndpointAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state customEndpointAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, customEndpointManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry custom endpoint account failed", err.Error())
	}
}

// ImportState imports a custom endpoint account by its FQN, e.g.
// "my-tenant:custom-endpoint:my-account".
func (r *customEndpointAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("fqn"), req, resp)
}

func (r *customEndpointAccountResource) apply(ctx context.Context, plan *customEndpointAccountModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, customEndpointManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry custom endpoint account manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied custom endpoint account manifest", map[string]any{
		"action": result.Action,
		"fqn":    result.Data.FQN,
	})
	plan.ID = types.StringValue(result.Data.ID)
	plan.FQN = types.StringValue(result.Data.FQN)
}

func customEndpointManifestFromModel(m *customEndpointAccountModel) client.ProviderAccountManifest {
	manifest := client.ProviderAccountManifest{
		Type:         "provider-account/custom-endpoint",
		Name:         m.Name.ValueString(),
		EndpointType: m.EndpointType.ValueString(),
		RoutingType:  m.RoutingType.ValueString(),
		Slug:         m.Slug.ValueString(),
		Integrations: []client.ModelIntegration{},
	}
	for _, c := range m.Collaborators {
		manifest.Collaborators = append(manifest.Collaborators, client.Collaborator{
			Subject: c.Subject.ValueString(),
			RoleID:  c.RoleID.ValueString(),
		})
	}
	for _, e := range m.Endpoints {
		integration := client.ModelIntegration{
			Type:    "integration/model/custom-endpoint",
			Name:    e.Name.ValueString(),
			BaseURL: e.BaseURL.ValueString(),
		}
		if len(e.AuthHeaders) > 0 {
			integration.AuthData = &client.APIKeyAuth{Type: "header", Headers: map[string]string{}}
			for k, v := range e.AuthHeaders {
				integration.AuthData.Headers[k] = v.ValueString()
			}
		}
		if len(e.Headers) > 0 {
			integration.Headers = map[string]string{}
			for k, v := range e.Headers {
				integration.Headers[k] = v.ValueString()
			}
		}
		for _, s := range e.AuthorizedSubjects {
			integration.AuthorizedSubjects = append(integration.AuthorizedSubjects, s.ValueString())
		}
		if e.TLS != nil {
			integration.TLSSettings = &client.TLSSettings{
				RejectUnauthorized: e.TLS.RejectUnauthorized.ValueBool(),
				CACert:             e.TLS.CACert.ValueString(),
			}
		}
		if e.Loadbalancing != nil {
			lb := &client.EndpointTargetConfig{
				Weight:   float64OrNil(e.Loadbalancing.Weight),
				Priority: int64OrNil(e.Loadbalancing.Priority),
			}
			if !e.Loadbalancing.FallbackCandidate.IsNull() {
				v := e.Loadbalancing.FallbackCandidate.ValueBool()
				lb.FallbackCandidate = &v
			}
			for _, c := range e.Loadbalancing.FallbackStatusCodes {
				lb.FallbackStatusCodes = append(lb.FallbackStatusCodes, c.ValueString())
			}
			if len(e.Loadbalancing.MetadataMatch) > 0 {
				lb.MetadataMatch = map[string]string{}
				for k, v := range e.Loadbalancing.MetadataMatch {
					lb.MetadataMatch[k] = v.ValueString()
				}
			}
			integration.LoadbalancingConfig = lb
		}
		manifest.Integrations = append(manifest.Integrations, integration)
	}
	return manifest
}

func endpointToModel(manifest client.ModelIntegration) endpointModel {
	e := endpointModel{
		Name:    types.StringValue(manifest.Name),
		BaseURL: types.StringValue(manifest.BaseURL),
	}
	if manifest.AuthData != nil && len(manifest.AuthData.Headers) > 0 {
		e.AuthHeaders = map[string]types.String{}
		for k, v := range manifest.AuthData.Headers {
			e.AuthHeaders[k] = types.StringValue(v)
		}
	}
	if len(manifest.Headers) > 0 {
		e.Headers = map[string]types.String{}
		for k, v := range manifest.Headers {
			e.Headers[k] = types.StringValue(v)
		}
	}
	for _, s := range manifest.AuthorizedSubjects {
		e.AuthorizedSubjects = append(e.AuthorizedSubjects, types.StringValue(s))
	}
	if manifest.TLSSettings != nil {
		e.TLS = &endpointTLSModel{
			RejectUnauthorized: types.BoolValue(manifest.TLSSettings.RejectUnauthorized),
			CACert:             stringOrNull(manifest.TLSSettings.CACert),
		}
	}
	if lb := manifest.LoadbalancingConfig; lb != nil {
		model := &loadbalancingModel{
			Weight:   float64OrNull(lb.Weight),
			Priority: int64OrNull(lb.Priority),
		}
		if lb.FallbackCandidate != nil {
			model.FallbackCandidate = types.BoolValue(*lb.FallbackCandidate)
		} else {
			model.FallbackCandidate = types.BoolNull()
		}
		for _, c := range lb.FallbackStatusCodes {
			model.FallbackStatusCodes = append(model.FallbackStatusCodes, types.StringValue(c))
		}
		if len(lb.MetadataMatch) > 0 {
			model.MetadataMatch = map[string]types.String{}
			for k, v := range lb.MetadataMatch {
				model.MetadataMatch[k] = types.StringValue(v)
			}
		}
		e.Loadbalancing = model
	}
	return e
}
