package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

// Load balancing across target models appears in two places with the same
// wire shape: the `routing_config` of a virtual model
// (truefoundry_provider_account) and the rules of the gateway load balancing
// config (truefoundry_gateway_load_balancing_config). The target schema,
// model, and conversions are shared here so the two cannot drift.
//
// The advanced target options `retry_config`, `sla_cutoff`, and
// `override_params` are not modeled — see README "Known limitations".

type routingTargetModel struct {
	Target              types.String            `tfsdk:"target"`
	Weight              types.Float64           `tfsdk:"weight"`
	Priority            types.Int64             `tfsdk:"priority"`
	FallbackStatusCodes []types.String          `tfsdk:"fallback_status_codes"`
	FallbackCandidate   types.Bool              `tfsdk:"fallback_candidate"`
	MetadataMatch       map[string]types.String `tfsdk:"metadata_match"`
	HeadersOverride     *headersOverrideModel   `tfsdk:"headers_override"`
}

type headersOverrideModel struct {
	Remove []types.String          `tfsdk:"remove"`
	Set    map[string]types.String `tfsdk:"set"`
}

// routingTargetAttributes returns the schema of one load balancing target.
func routingTargetAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"target": schema.StringAttribute{
			Required:    true,
			Description: "Target model as `<provider-account>/<model-name>`.",
		},
		"weight": schema.Float64Attribute{
			Optional:    true,
			Description: "Relative routing weight; higher values receive more traffic (weight-based routing).",
		},
		"priority": schema.Int64Attribute{
			Optional:    true,
			Description: "Priority of the target; 0 is highest (priority-based routing).",
		},
		"fallback_status_codes": schema.SetAttribute{
			Optional:    true,
			ElementType: types.StringType,
			Description: "Status codes for which the request falls back to other targets.",
		},
		"fallback_candidate": schema.BoolAttribute{
			Optional:    true,
			Description: "Whether this target may serve as a fallback for other targets.",
		},
		"metadata_match": schema.MapAttribute{
			Optional:    true,
			ElementType: types.StringType,
			Description: "Metadata key-value pairs that must match incoming request metadata headers for this target to be considered.",
		},
		"headers_override": schema.SingleNestedAttribute{
			Optional:    true,
			Description: "Headers to remove from or set on the upstream request for this target.",
			Attributes: map[string]schema.Attribute{
				"remove": schema.SetAttribute{
					Optional:    true,
					ElementType: types.StringType,
					Description: "Headers to remove from the upstream request.",
				},
				"set": schema.MapAttribute{
					Optional:    true,
					ElementType: types.StringType,
					Description: "Headers to add or overwrite on the upstream request.",
				},
			},
		},
	}
}

func routingTargetFromModel(t routingTargetModel) client.RoutingTarget {
	target := client.RoutingTarget{
		Target:   t.Target.ValueString(),
		Weight:   float64OrNil(t.Weight),
		Priority: int64OrNil(t.Priority),
	}
	if !t.FallbackCandidate.IsNull() {
		v := t.FallbackCandidate.ValueBool()
		target.FallbackCandidate = &v
	}
	for _, c := range t.FallbackStatusCodes {
		target.FallbackStatusCodes = append(target.FallbackStatusCodes, c.ValueString())
	}
	if len(t.MetadataMatch) > 0 {
		target.MetadataMatch = map[string]string{}
		for k, v := range t.MetadataMatch {
			target.MetadataMatch[k] = v.ValueString()
		}
	}
	if t.HeadersOverride != nil {
		override := &client.HeadersOverride{}
		for _, h := range t.HeadersOverride.Remove {
			override.Remove = append(override.Remove, h.ValueString())
		}
		if len(t.HeadersOverride.Set) > 0 {
			override.Set = map[string]string{}
			for k, v := range t.HeadersOverride.Set {
				override.Set[k] = v.ValueString()
			}
		}
		target.HeadersOverride = override
	}
	return target
}

func routingTargetToModel(t client.RoutingTarget) routingTargetModel {
	target := routingTargetModel{
		Target:   types.StringValue(t.Target),
		Weight:   float64OrNull(t.Weight),
		Priority: int64OrNull(t.Priority),
	}
	if t.FallbackCandidate != nil {
		target.FallbackCandidate = types.BoolValue(*t.FallbackCandidate)
	} else {
		target.FallbackCandidate = types.BoolNull()
	}
	for _, c := range t.FallbackStatusCodes {
		target.FallbackStatusCodes = append(target.FallbackStatusCodes, types.StringValue(c))
	}
	if len(t.MetadataMatch) > 0 {
		target.MetadataMatch = map[string]types.String{}
		for k, v := range t.MetadataMatch {
			target.MetadataMatch[k] = types.StringValue(v)
		}
	}
	if t.HeadersOverride != nil {
		override := &headersOverrideModel{}
		for _, h := range t.HeadersOverride.Remove {
			override.Remove = append(override.Remove, types.StringValue(h))
		}
		if len(t.HeadersOverride.Set) > 0 {
			override.Set = map[string]types.String{}
			for k, v := range t.HeadersOverride.Set {
				override.Set[k] = types.StringValue(v)
			}
		}
		target.HeadersOverride = override
	}
	return target
}
