package mockcp

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
)

// budgetV2 is a stored v2 budget rule ("tenant-budget-config" or
// "team-budget-config"). Unlike the v1 gateway-budget-config singleton, these
// are individually named rules.
type budgetV2 struct {
	id       string
	manifest map[string]any
}

// budgetV2NameRe is the rule name pattern the real control plane enforces —
// note it allows upper case, unlike most other manifest names.
var budgetV2NameRe = regexp.MustCompile(`^[a-zA-Z](?:[a-zA-Z0-9\-]{0,60}[a-zA-Z0-9])$`)

// mockTeams are the team names the mock accepts on team-budget-config rules;
// the real control plane rejects unknown teams with "Invalid team id".
var mockTeams = map[string]bool{"mock-team": true, "platform": true}

// isBudgetV2Type reports whether typ is a v2 budget rule manifest type.
func isBudgetV2Type(typ string) bool {
	return typ == "tenant-budget-config" || typ == "team-budget-config"
}

// BudgetCount returns the number of stored v2 budget rules.
func (s *Server) BudgetCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.budgets)
}

// DeleteBudgetByName removes a v2 budget rule out-of-band (simulates drift).
func (s *Server) DeleteBudgetByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.budgets, name)
}

// applyBudgetV2 stores a v2 budget rule keyed by name.
func (s *Server) applyBudgetV2(w http.ResponseWriter, typ, name string, manifest map[string]any, dryRun bool) {
	if !budgetV2NameRe.MatchString(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Validation Error: GatewayConfig /name: must match pattern %q", budgetV2NameRe.String())})
		return
	}
	if msg := validateBudgetV2Manifest(typ, manifest); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": msg})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	action := "CREATE"
	var existing map[string]any
	b, ok := s.budgets[name]
	if ok {
		action = "UPDATE"
		existing = b.manifest
	} else {
		b = &budgetV2{id: "bgt-" + strconv.Itoa(s.nextID)}
		s.nextID++
	}
	if !dryRun {
		b.manifest = manifest
		s.budgets[name] = b
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"existingManifest": existing,
		"action":           action,
		"data": map[string]any{
			"id":         b.id,
			"tenantName": Tenant,
			"name":       name,
			"type":       typ,
			"manifest":   manifest,
		},
	})
}

// handleListBudgets implements GET /api/svc/v1/llm-gateway/budgets. Only the
// `type` query parameter filters; the real endpoint ignores everything else.
func (s *Server) handleListBudgets(w http.ResponseWriter, r *http.Request) {
	typeFilter := r.URL.Query().Get("type")
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []map[string]any{}
	for name, b := range s.budgets {
		typ, _ := b.manifest["type"].(string)
		if typeFilter != "" && typ != typeFilter {
			continue
		}
		items = append(items, map[string]any{
			"id":         b.id,
			"tenantName": Tenant,
			"name":       name,
			"type":       typ,
			"manifest":   b.manifest,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
	writeJSON(w, http.StatusOK, items)
}

// validateBudgetV2Manifest enforces the v2 budget rule shape, derived from
// the TrueFoundry SDK's TenantBudgetConfig/TeamBudgetConfig schemas and from
// dry-run probes against a real control plane.
func validateBudgetV2Manifest(typ string, manifest map[string]any) string {
	switch mode, _ := manifest["mode"].(string); mode {
	case "enforce", "audit":
	default:
		return "/mode: must be equal to one of the allowed values"
	}

	if typ == "team-budget-config" {
		team, _ := manifest["team_name"].(string)
		if team == "" {
			return "must have required property 'team_name'"
		}
		if !mockTeams[team] {
			return "Invalid team id"
		}
	} else if _, ok := manifest["team_name"]; ok {
		return "team_name is only valid on team-budget-config"
	}

	limits, ok := manifest["limits"].(map[string]any)
	if !ok {
		return "must have required property 'limits'"
	}
	if msg := validateBudgetV2Limits(limits); msg != "" {
		return msg
	}

	appliesTo, ok := manifest["applies_to"].(map[string]any)
	if !ok {
		return "must have required property 'applies_to'"
	}
	appliesToType, _ := appliesTo["type"].(string)
	overrideKey := ""
	switch appliesToType {
	case "aggregate":
	case "per-user":
		overrideKey = "users"
	case "per-model":
		overrideKey = "models"
	case "per-virtual-account":
		overrideKey = "virtual_accounts"
	case "metadata":
		overrideKey = "metadata_values"
		if key, _ := appliesTo["metadata"].(string); key == "" {
			return "applies_to.metadata is required when applies_to.type is metadata"
		}
	default:
		return fmt.Sprintf("unknown applies_to type %q", appliesToType)
	}
	if key, ok := appliesTo["metadata"]; ok && appliesToType != "metadata" {
		return fmt.Sprintf("applies_to.metadata %q is only valid when applies_to.type is metadata", key)
	}
	overrides, _ := appliesTo["overrides"].([]any)
	if len(overrides) > 0 && appliesToType == "aggregate" {
		return "applies_to.overrides is not valid for aggregate budgets"
	}
	for _, raw := range overrides {
		o, _ := raw.(map[string]any)
		entities, _ := o[overrideKey].([]any)
		if len(entities) == 0 {
			return fmt.Sprintf("applies_to.overrides entries must set %q for %s budgets", overrideKey, appliesToType)
		}
		ol, ok := o["limits"].(map[string]any)
		if !ok {
			return "applies_to.overrides entries must set limits"
		}
		if msg := validateBudgetV2Limits(ol); msg != "" {
			return msg
		}
	}

	if when, ok := manifest["when"].(map[string]any); ok {
		if subjects, ok := when["subjects"].(map[string]any); ok {
			if _, hasTeams := subjects["teams"]; hasTeams && typ == "team-budget-config" {
				return "when.subjects.teams is not valid on team-budget-config"
			}
		}
	}

	if alerts, ok := manifest["alerts"].(map[string]any); ok {
		thresholds, _ := alerts["thresholds"].([]any)
		if len(thresholds) == 0 {
			return "alerts.thresholds must not be empty"
		}
		for _, raw := range thresholds {
			v, _ := raw.(float64)
			if v <= 0 || v > 100 {
				return "alerts.thresholds must be percentages between 0 and 100"
			}
		}
		if notify, ok := alerts["notify_breaching_user"].(bool); ok && notify && appliesToType != "per-user" {
			return "alerts.notify_breaching_user is only valid for per-user budgets"
		}
		for _, raw := range mapSlice(alerts["notification_target"]) {
			switch raw["type"] {
			case "email", "slack-webhook", "slack-bot", "pagerduty", "ms-teams-webhook":
			default:
				return fmt.Sprintf("unknown notification target type %q", raw["type"])
			}
			if ch, _ := raw["notification_channel"].(string); ch == "" {
				return "notification_channel is required on notification targets"
			}
		}
	}
	return ""
}

// budgetV2Periods are the spend periods a limits block may set.
var budgetV2Periods = []string{"cost_per_day", "cost_per_week", "cost_per_month", "cost_per_quarter", "cost_per_lifetime"}

// validateBudgetV2Limits rejects an empty limits block and non-positive caps.
func validateBudgetV2Limits(limits map[string]any) string {
	set := 0
	for _, p := range budgetV2Periods {
		v, ok := limits[p]
		if !ok {
			continue
		}
		f, isNum := v.(float64)
		if !isNum || f <= 0 {
			return fmt.Sprintf("limits.%s must be a positive number", p)
		}
		set++
	}
	if set == 0 {
		return fmt.Sprintf("limits must set at least one of %v", budgetV2Periods)
	}
	if set != len(limits) {
		return fmt.Sprintf("limits contains unknown periods; supported: %v", budgetV2Periods)
	}
	if _, lifetime := limits["cost_per_lifetime"]; lifetime && set > 1 {
		return `Budget "limits" cannot combine "cost_per_lifetime" with day/week/month/quarter limits`
	}
	return ""
}

// mapSlice converts a decoded JSON array into a slice of objects, skipping
// entries that are not objects.
func mapSlice(v any) []map[string]any {
	raw, _ := v.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
