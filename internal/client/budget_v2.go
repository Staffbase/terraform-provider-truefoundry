package client

import (
	"context"
	"net/http"
	"net/url"
)

// Budget limiting v2 ("Budget Limiting V2" in the TrueFoundry docs) replaces
// the single gateway-budget-config singleton with one manifest per rule:
// "tenant-budget-config" or "team-budget-config". Rules are applied and
// deleted through the generic manifest endpoints and listed via
// GET /api/svc/v1/llm-gateway/budgets — the llm-gateway/config/<type>
// endpoint returns nothing for these types.
const budgetsPath = "/api/svc/v1/llm-gateway/budgets"

// BudgetV2Filter matches a value against an allow list, a deny list, or both.
type BudgetV2Filter struct {
	In    []string `json:"in,omitempty"`
	NotIn []string `json:"not_in,omitempty"`
}

// BudgetV2Subjects filters a budget rule by the calling subject. Teams is
// only valid on tenant-scoped rules.
type BudgetV2Subjects struct {
	Users           *BudgetV2Filter `json:"users,omitempty"`
	Teams           *BudgetV2Filter `json:"teams,omitempty"`
	VirtualAccounts *BudgetV2Filter `json:"virtual_accounts,omitempty"`
}

// BudgetV2When scopes a budget rule to matching requests. All given filters
// must match; an empty value matches every request within the rule's scope.
type BudgetV2When struct {
	Subjects         *BudgetV2Subjects         `json:"subjects,omitempty"`
	Models           *BudgetV2Filter           `json:"models,omitempty"`
	ProviderAccounts *BudgetV2Filter           `json:"provider_accounts,omitempty"`
	Metadata         map[string]BudgetV2Filter `json:"metadata,omitempty"`
}

// BudgetV2Limits are the spend caps of a budget rule, in USD. Several
// periods can be set at once; each is enforced independently.
type BudgetV2Limits struct {
	CostPerDay      *float64 `json:"cost_per_day,omitempty"`
	CostPerWeek     *float64 `json:"cost_per_week,omitempty"`
	CostPerMonth    *float64 `json:"cost_per_month,omitempty"`
	CostPerQuarter  *float64 `json:"cost_per_quarter,omitempty"`
	CostPerLifetime *float64 `json:"cost_per_lifetime,omitempty"`
}

// BudgetV2Override raises or lowers the limits for specific entities of a
// per-entity budget. Exactly one of the entity lists is set, matching the
// owning BudgetV2AppliesTo.Type.
type BudgetV2Override struct {
	Users           []string       `json:"users,omitempty"`
	Models          []string       `json:"models,omitempty"`
	VirtualAccounts []string       `json:"virtual_accounts,omitempty"`
	MetadataValues  []string       `json:"metadata_values,omitempty"`
	Limits          BudgetV2Limits `json:"limits"`
}

// BudgetV2AppliesTo selects whether the rule tracks one shared budget
// ("aggregate") or a separate budget per entity. Metadata holds the metadata
// key to bucket by, and is only set for type "metadata".
type BudgetV2AppliesTo struct {
	Type      string             `json:"type"`
	Metadata  string             `json:"metadata,omitempty"`
	Overrides []BudgetV2Override `json:"overrides,omitempty"`
}

// BudgetV2Alerts notifies targets when usage crosses the given percentage
// thresholds of the limit.
type BudgetV2Alerts struct {
	Thresholds          []float64            `json:"thresholds"`
	NotifyBreachingUser *bool                `json:"notify_breaching_user,omitempty"`
	NotificationTarget  []NotificationTarget `json:"notification_target,omitempty"`
}

// BudgetV2Manifest is a "tenant-budget-config" or "team-budget-config"
// manifest — one budget rule. TeamName is required for team-scoped rules and
// empty otherwise.
type BudgetV2Manifest struct {
	Type      string            `json:"type"`
	Name      string            `json:"name"`
	TeamName  string            `json:"team_name,omitempty"`
	Mode      string            `json:"mode"`
	When      BudgetV2When      `json:"when"`
	Limits    BudgetV2Limits    `json:"limits"`
	AppliesTo BudgetV2AppliesTo `json:"applies_to"`
	Alerts    *BudgetV2Alerts   `json:"alerts,omitempty"`
}

// BudgetV2 is a stored budget rule as returned by
// GET /api/svc/v1/llm-gateway/budgets.
type BudgetV2 struct {
	ID         string           `json:"id"`
	TenantName string           `json:"tenantName"`
	Name       string           `json:"name"`
	Type       string           `json:"type"`
	Manifest   BudgetV2Manifest `json:"manifest"`
}

// ListBudgets returns the tenant's v2 budget rules of the given manifest type
// ("tenant-budget-config" or "team-budget-config"). The endpoint wraps the
// array in a {"data": [...]} envelope (verified against the dev control plane
// 2026-09-15; no "pagination" key is present, so this does not page).
func (c *Client) ListBudgets(ctx context.Context, manifestType string) ([]BudgetV2, error) {
	var out listIntegrationsResponse[BudgetV2]
	q := url.Values{"type": {manifestType}}
	if err := c.do(ctx, http.MethodGet, budgetsPath, q, nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// GetBudget returns the budget rule with the given manifest type and name, or
// nil if it does not exist. The list endpoint ignores a name filter, so the
// match happens client-side.
func (c *Client) GetBudget(ctx context.Context, manifestType, name string) (*BudgetV2, error) {
	budgets, err := c.ListBudgets(ctx, manifestType)
	if err != nil {
		return nil, err
	}
	for i, b := range budgets {
		if b.Name == name {
			return &budgets[i], nil
		}
	}
	return nil, nil
}
