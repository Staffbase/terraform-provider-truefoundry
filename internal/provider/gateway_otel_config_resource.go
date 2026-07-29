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
	_ resource.Resource                   = (*gatewayOtelConfigResource)(nil)
	_ resource.ResourceWithConfigure      = (*gatewayOtelConfigResource)(nil)
	_ resource.ResourceWithValidateConfig = (*gatewayOtelConfigResource)(nil)
	_ resource.ResourceWithImportState    = (*gatewayOtelConfigResource)(nil)
)

// otelExporterTypeValues are the supported OTLP transports.
var otelExporterTypeValues = []string{"http", "grpc"}

// otelExporterEncodingValues are the supported HTTP payload encodings.
var otelExporterEncodingValues = []string{"proto", "json"}

// The span filters of a traces exporter only support one field and one
// operator, so both are constants rather than schema attributes.
const (
	otelSpanFilterField    = "spanAttributes"
	otelSpanFilterOperator = "IN"
)

func newGatewayOtelConfigResource() resource.Resource {
	return &gatewayOtelConfigResource{}
}

// gatewayOtelConfigResource manages the tenant-wide AI gateway OTel config via
// the manifest-based apply/delete API and the dedicated llm-gateway/config
// read endpoint.
type gatewayOtelConfigResource struct {
	client *client.Client
}

type gatewayOtelConfigModel struct {
	ID                        types.String              `tfsdk:"id"`
	OtelTracesExporterConfig  *otelTracesExporterModel  `tfsdk:"otel_traces_exporter_config"`
	OtelMetricsExporterConfig *otelMetricsExporterModel `tfsdk:"otel_metrics_exporter_config"`
}

type otelTracesExporterModel struct {
	Type                         types.String            `tfsdk:"type"`
	Endpoint                     types.String            `tfsdk:"endpoint"`
	Encoding                     types.String            `tfsdk:"encoding"`
	Headers                      map[string]types.String `tfsdk:"headers"`
	AdditionalResourceAttributes map[string]types.String `tfsdk:"additional_resource_attributes"`
	ExcludeRequestData           types.Bool              `tfsdk:"exclude_request_data"`
	When                         []otelSpanFilterModel   `tfsdk:"when"`
}

type otelMetricsExporterModel struct {
	Type     types.String            `tfsdk:"type"`
	Endpoint types.String            `tfsdk:"endpoint"`
	Encoding types.String            `tfsdk:"encoding"`
	Headers  map[string]types.String `tfsdk:"headers"`
}

type otelSpanFilterModel struct {
	Key   types.String   `tfsdk:"key"`
	Value []types.String `tfsdk:"value"`
}

func (r *gatewayOtelConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_otel_config"
}

// otelExporterAttributes returns the attributes every OTLP exporter has.
func otelExporterAttributes(signal string) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"type": schema.StringAttribute{
			Required:    true,
			Description: fmt.Sprintf("OTLP transport. One of: `%s`.", strings.Join(otelExporterTypeValues, "`, `")),
			Validators:  []validator.String{stringvalidator.OneOf(otelExporterTypeValues...)},
		},
		"endpoint": schema.StringAttribute{
			Required:    true,
			Description: fmt.Sprintf("URL of the collector receiving the %s.", signal),
		},
		"encoding": schema.StringAttribute{
			Optional: true,
			Description: fmt.Sprintf("Payload encoding. One of: `%s`. Required for `http` exporters, invalid for `grpc`.",
				strings.Join(otelExporterEncodingValues, "`, `")),
			Validators: []validator.String{stringvalidator.OneOf(otelExporterEncodingValues...)},
		},
		"headers": schema.MapAttribute{
			Optional:    true,
			Sensitive:   true,
			ElementType: types.StringType,
			Description: "Headers sent to the collector, e.g. an `authorization` header. Marked sensitive as a whole; " +
				"prefer a TrueFoundry secret reference over a raw token.",
		},
	}
}

func (r *gatewayOtelConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	tracesAttributes := otelExporterAttributes("traces")
	tracesAttributes["additional_resource_attributes"] = schema.MapAttribute{
		Optional:    true,
		ElementType: types.StringType,
		Description: "Extra OpenTelemetry resource attributes set on every exported span, e.g. `service.namespace`.",
	}
	tracesAttributes["exclude_request_data"] = schema.BoolAttribute{
		Optional:    true,
		Description: "Whether prompts and completions are stripped from exported spans.",
	}
	tracesAttributes["when"] = schema.ListNestedAttribute{
		Optional: true,
		Description: "Exports only spans whose attributes match. Every entry must match; omit the attribute to " +
			"export all spans.",
		Validators: []validator.List{listvalidator.SizeAtLeast(1)},
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"key": schema.StringAttribute{
					Required:    true,
					Description: "Span attribute to match on.",
					Validators:  []validator.String{stringvalidator.LengthAtLeast(1)},
				},
				"value": schema.SetAttribute{
					Required:    true,
					ElementType: types.StringType,
					Description: "Values of the span attribute that are exported.",
					Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
				},
			},
		},
	}

	resp.Schema = schema.Schema{
		Description: "Manages the AI gateway OpenTelemetry configuration: where the gateway ships its traces and " +
			"metrics. Applied as a `gateway-otel-config` manifest via `PUT /api/svc/v1/apply`. **The OTel config is " +
			"a tenant-wide singleton** — define at most one of this resource per control plane; an apply replaces the " +
			"whole manifest, so an omitted exporter is switched off. Import the existing config with " +
			"`terraform import truefoundry_gateway_otel_config.example gateway-otel-config`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Control plane ID of the gateway OTel config.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"otel_traces_exporter_config": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "OTLP exporter the gateway ships request traces to.",
				Attributes:  tracesAttributes,
			},
			"otel_metrics_exporter_config": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "OTLP exporter the gateway ships metrics to.",
				Attributes:  otelExporterAttributes("metrics"),
			},
		},
	}
}

func (r *gatewayOtelConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces that at least one exporter is configured and that
// `encoding` matches the transport.
func (r *gatewayOtelConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// With for_each/count, ValidateConfig first runs on the unexpanded
	// resource where each.*/count.* references are unknown; Go-native model
	// fields cannot hold unknown collections. Skip — validation re-runs at
	// plan time with known values.
	if !req.Config.Raw.IsFullyKnown() {
		return
	}
	var config gatewayOtelConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.OtelTracesExporterConfig == nil && config.OtelMetricsExporterConfig == nil {
		resp.Diagnostics.AddError("Empty OTel config",
			"Set `otel_traces_exporter_config`, `otel_metrics_exporter_config`, or both — an OTel config without an exporter does nothing.")
		return
	}
	if t := config.OtelTracesExporterConfig; t != nil {
		validateOtelEncoding(path.Root("otel_traces_exporter_config"), t.Type, t.Encoding, &resp.Diagnostics)
	}
	if m := config.OtelMetricsExporterConfig; m != nil {
		validateOtelEncoding(path.Root("otel_metrics_exporter_config"), m.Type, m.Encoding, &resp.Diagnostics)
	}
}

// validateOtelEncoding reports an encoding that does not match the transport:
// the control plane requires it on HTTP exporters and rejects it on gRPC ones.
func validateOtelEncoding(p path.Path, exporterType, encoding types.String, diags *diag.Diagnostics) {
	if exporterType.IsUnknown() || encoding.IsUnknown() {
		return
	}
	switch exporterType.ValueString() {
	case "http":
		if encoding.IsNull() {
			diags.AddAttributeError(p.AtName("encoding"), "Missing encoding",
				"`encoding` is required for `http` exporters.")
		}
	case "grpc":
		if !encoding.IsNull() {
			diags.AddAttributeError(p.AtName("encoding"), "Unexpected encoding",
				"`encoding` is only valid for `http` exporters; gRPC always uses protobuf.")
		}
	}
}

func (r *gatewayOtelConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayOtelConfigModel
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

func (r *gatewayOtelConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gatewayOtelConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.GetGatewayOtelConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading TrueFoundry gateway OTel config failed", err.Error())
		return
	}
	if config == nil {
		tflog.Info(ctx, "no gateway OTel config on the control plane; removing from state")
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(config.ID)
	state.OtelTracesExporterConfig = otelTracesExporterToModel(config.Manifest.OtelTracesExporterConfig)
	state.OtelMetricsExporterConfig = otelMetricsExporterToModel(config.Manifest.OtelMetricsExporterConfig)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gatewayOtelConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan gatewayOtelConfigModel
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

func (r *gatewayOtelConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gatewayOtelConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Delete(ctx, otelConfigManifestFromModel(&state))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting TrueFoundry gateway OTel config failed", err.Error())
	}
}

// ImportState imports the tenant's gateway OTel config; the import ID is
// ignored (the config is a singleton), by convention pass
// "gateway-otel-config".
func (r *gatewayOtelConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// apply sends the manifest and refreshes the computed ID from the dedicated
// read endpoint (the generic apply response carries no id/fqn for gateway
// configs).
func (r *gatewayOtelConfigResource) apply(ctx context.Context, plan *gatewayOtelConfigModel, diags *diag.Diagnostics) {
	result, err := r.client.Apply(ctx, otelConfigManifestFromModel(plan), false)
	if err != nil {
		diags.AddError("Applying TrueFoundry gateway OTel config manifest failed", err.Error())
		return
	}
	tflog.Info(ctx, "applied gateway OTel config manifest", map[string]any{"action": result.Action})
	config, err := r.client.GetGatewayOtelConfig(ctx)
	if err != nil {
		diags.AddError("Reading back TrueFoundry gateway OTel config failed", err.Error())
		return
	}
	if config == nil {
		diags.AddError("Reading back TrueFoundry gateway OTel config failed", "config not found after apply")
		return
	}
	plan.ID = types.StringValue(config.ID)
}

func otelConfigManifestFromModel(m *gatewayOtelConfigModel) client.GatewayOtelConfigManifest {
	manifest := client.GatewayOtelConfigManifest{Type: "gateway-otel-config"}
	if t := m.OtelTracesExporterConfig; t != nil {
		exporter := &client.OtelExporterConfig{
			Type:                         t.Type.ValueString(),
			Endpoint:                     t.Endpoint.ValueString(),
			Encoding:                     t.Encoding.ValueString(),
			Headers:                      stringMapFromModel(t.Headers),
			AdditionalResourceAttributes: stringMapFromModel(t.AdditionalResourceAttributes),
		}
		if !t.ExcludeRequestData.IsNull() {
			v := t.ExcludeRequestData.ValueBool()
			exporter.ExcludeRequestData = &v
		}
		for _, f := range t.When {
			exporter.When = append(exporter.When, client.OtelSpanAttributeFilter{
				Field:    otelSpanFilterField,
				Key:      f.Key.ValueString(),
				Operator: otelSpanFilterOperator,
				Value:    subjectsFromModel(f.Value),
			})
		}
		manifest.OtelTracesExporterConfig = exporter
	}
	if metrics := m.OtelMetricsExporterConfig; metrics != nil {
		manifest.OtelMetricsExporterConfig = &client.OtelExporterConfig{
			Type:     metrics.Type.ValueString(),
			Endpoint: metrics.Endpoint.ValueString(),
			Encoding: metrics.Encoding.ValueString(),
			Headers:  stringMapFromModel(metrics.Headers),
		}
	}
	return manifest
}

func otelTracesExporterToModel(e *client.OtelExporterConfig) *otelTracesExporterModel {
	if e == nil {
		return nil
	}
	out := &otelTracesExporterModel{
		Type:                         types.StringValue(e.Type),
		Endpoint:                     types.StringValue(e.Endpoint),
		Encoding:                     stringOrNull(e.Encoding),
		Headers:                      stringMapToModel(e.Headers),
		AdditionalResourceAttributes: stringMapToModel(e.AdditionalResourceAttributes),
		ExcludeRequestData:           types.BoolNull(),
	}
	if e.ExcludeRequestData != nil {
		out.ExcludeRequestData = types.BoolValue(*e.ExcludeRequestData)
	}
	for _, f := range e.When {
		out.When = append(out.When, otelSpanFilterModel{
			Key:   types.StringValue(f.Key),
			Value: subjectsToModel(f.Value),
		})
	}
	return out
}

func otelMetricsExporterToModel(e *client.OtelExporterConfig) *otelMetricsExporterModel {
	if e == nil {
		return nil
	}
	return &otelMetricsExporterModel{
		Type:     types.StringValue(e.Type),
		Endpoint: types.StringValue(e.Endpoint),
		Encoding: stringOrNull(e.Encoding),
		Headers:  stringMapToModel(e.Headers),
	}
}
