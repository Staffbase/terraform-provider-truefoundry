package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
)

// orderInsensitive returns a list plan modifier that suppresses diffs that
// only reorder elements: when the planned list is a permutation of the list
// in state, the plan keeps the state value. Element comparison uses
// attr.Value.Equal, so nested set attributes already compare
// order-insensitively.
//
// Collections whose elements carry sensitive attributes cannot be sets —
// Terraform cannot mark values inside sets, so the whole collection would
// render as one opaque "(sensitive value)" in plans. This modifier gives
// those lists the same reorder-tolerant behavior sets have natively.
func orderInsensitive() planmodifier.List { return orderInsensitiveModifier{} }

type orderInsensitiveModifier struct{}

func (orderInsensitiveModifier) Description(context.Context) string {
	return "Ignores changes that only reorder elements."
}

func (m orderInsensitiveModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (orderInsensitiveModifier) PlanModifyList(_ context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	if req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	state, plan := req.StateValue.Elements(), req.PlanValue.Elements()
	if len(state) != len(plan) {
		return
	}
	used := make([]bool, len(state))
outer:
	for _, p := range plan {
		if p.IsUnknown() {
			return
		}
		for i, s := range state {
			if !used[i] && p.Equal(s) {
				used[i] = true
				continue outer
			}
		}
		return
	}
	resp.PlanValue = req.StateValue
}
