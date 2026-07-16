// Package mockcp is an in-memory mock of the TrueFoundry control plane API
// surface used by this provider. It backs the unit and acceptance tests and
// the manual demo server, so tests need no real TrueFoundry credentials.
// Request/response shapes follow the official API docs
// (https://www.truefoundry.com/docs/apply-api-create-models) and the
// truefoundry-typescript-sdk definitions.
package mockcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Tenant is the tenant name used in generated FQNs.
const Tenant = "mock-tenant"

// Server is an in-memory TrueFoundry control plane.
type Server struct {
	apiKey string

	mu        sync.Mutex
	accounts  map[string]*account        // keyed by manifest name
	vaccounts map[string]*virtualAccount // keyed by manifest name
	gwconfigs map[string]*gatewayConfig  // keyed by manifest type, e.g. "gateway-budget-config"
	nextID    int
}

type account struct {
	id       string
	manifest map[string]any
}

type gatewayConfig struct {
	id       string
	manifest map[string]any
}

type virtualAccount struct {
	id       string
	token    string
	manifest map[string]any
}

// New returns a mock control plane that requires the given bearer API key.
func New(apiKey string) *Server {
	return &Server{apiKey: apiKey, accounts: map[string]*account{}, vaccounts: map[string]*virtualAccount{}, gwconfigs: map[string]*gatewayConfig{}, nextID: 1}
}

// Handler returns the HTTP handler implementing the API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/svc/v1/apply", s.auth(s.handleApply))
	mux.HandleFunc("POST /api/svc/v1/delete", s.auth(s.handleDelete))
	mux.HandleFunc("GET /api/svc/v1/provider-integrations", s.auth(s.handleListIntegrations))
	mux.HandleFunc("GET /api/svc/v1/llm-gateway/config/{type}", s.auth(s.handleGetGatewayConfig))
	mux.HandleFunc("PUT /api/svc/v1/virtual-accounts", s.auth(s.handleApplyVirtualAccount))
	mux.HandleFunc("GET /api/svc/v1/virtual-accounts/{id}", s.auth(s.handleGetVirtualAccount))
	mux.HandleFunc("DELETE /api/svc/v1/virtual-accounts/{id}", s.auth(s.handleDeleteVirtualAccount))
	mux.HandleFunc("GET /api/svc/v1/virtual-accounts/{id}/token", s.auth(s.handleGetVirtualAccountToken))
	return mux
}

// AccountCount returns the number of stored provider accounts.
func (s *Server) AccountCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.accounts)
}

// DeleteAccount removes an account out-of-band (simulates drift/manual deletion).
func (s *Server) DeleteAccount(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.accounts, name)
}

// VirtualAccountCount returns the number of stored virtual accounts.
func (s *Server) VirtualAccountCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.vaccounts)
}

// DeleteVirtualAccountByName removes a virtual account out-of-band.
func (s *Server) DeleteVirtualAccountByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.vaccounts, name)
}

// GatewayConfigCount returns the number of stored gateway configs.
func (s *Server) GatewayConfigCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.gwconfigs)
}

func (s *Server) handleGetGatewayConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, ok := s.gwconfigs[r.PathValue("type")]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "gateway config not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         cfg.id,
		"tenantName": Tenant,
		"type":       r.PathValue("type"),
		"manifest":   cfg.manifest,
	})
}

func (s *Server) handleApplyVirtualAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Manifest map[string]any `json:"manifest"`
		DryRun   bool           `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Manifest == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid request body"})
		return
	}
	name, _ := req.Manifest["name"].(string)
	if typ, _ := req.Manifest["type"].(string); typ != "virtual-account" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "manifest type must be virtual-account"})
		return
	}
	if name == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "name is required"})
		return
	}
	if _, ok := req.Manifest["permissions"].([]any); !ok {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "permissions are required"})
		return
	}
	if tt, ok := req.Manifest["token_type"].(string); ok && tt != "jwt" && tt != "opaque" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "token_type must be jwt or opaque"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	va, exists := s.vaccounts[name]
	if !exists {
		va = &virtualAccount{id: "va-" + strconv.Itoa(s.nextID)}
		va.token = "mock-token-" + va.id
		s.nextID++
	}
	resp := map[string]any{}
	if !req.DryRun {
		va.manifest = req.Manifest
		s.vaccounts[name] = va
	}
	resp["data"] = map[string]any{"id": va.id, "manifest": va.manifest, "tenantName": Tenant}
	if !exists {
		// The real control plane returns the token only on creation.
		resp["token"] = va.token
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) findVirtualAccount(id string) *virtualAccount {
	for _, va := range s.vaccounts {
		if va.id == id {
			return va
		}
	}
	return nil
}

func (s *Server) handleGetVirtualAccount(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	va := s.findVirtualAccount(r.PathValue("id"))
	if va == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "virtual account not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"id": va.id, "manifest": va.manifest, "tenantName": Tenant}})
}

func (s *Server) handleDeleteVirtualAccount(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, va := range s.vaccounts {
		if va.id == r.PathValue("id") {
			delete(s.vaccounts, name)
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "virtual account not found"})
}

func (s *Server) handleGetVirtualAccountToken(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	va := s.findVirtualAccount(r.PathValue("id"))
	if va == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "virtual account not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": va.token})
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.apiKey {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "invalid or missing API key"})
			return
		}
		next(w, r)
	}
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Manifest map[string]any `json:"manifest"`
		DryRun   bool           `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Manifest == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid request body"})
		return
	}
	typ, _ := req.Manifest["type"].(string)
	name, _ := req.Manifest["name"].(string)
	if isGatewayConfigType(typ) {
		s.applyGatewayConfig(w, typ, name, req.Manifest, req.DryRun)
		return
	}
	if !strings.HasPrefix(typ, "provider-account/") {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": fmt.Sprintf("unsupported manifest type %q", typ)})
		return
	}
	if len(name) < 3 || len(name) > 32 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "name must be 3-32 characters"})
		return
	}
	if msg := validateManifest(typ, req.Manifest); msg != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": msg})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	action := "CREATE"
	var existing map[string]any
	acc, ok := s.accounts[name]
	if ok {
		action = "UPDATE"
		existing = acc.manifest
	} else {
		acc = &account{id: "pa-" + strconv.Itoa(s.nextID)}
		s.nextID++
	}
	if !req.DryRun {
		acc.manifest = req.Manifest
		s.accounts[name] = acc
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"existingManifest": existing,
		"action":           action,
		"data": map[string]any{
			"id":   acc.id,
			"name": name,
			"fqn":  accountFQN(typ, name),
		},
	})
}

// isGatewayConfigType reports whether typ is a tenant-wide gateway config
// manifest type the mock models.
func isGatewayConfigType(typ string) bool {
	return typ == "gateway-budget-config" || typ == "gateway-data-access-config" || typ == "gateway-data-routing-config" || typ == "gateway-guardrails-config"
}

// applyGatewayConfig stores a tenant-wide gateway config (e.g.
// "gateway-budget-config") keyed by its manifest type.
func (s *Server) applyGatewayConfig(w http.ResponseWriter, typ, name string, manifest map[string]any, dryRun bool) {
	if len(name) < 3 || len(name) > 32 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "name must be 3-32 characters"})
		return
	}
	var msg string
	switch typ {
	case "gateway-budget-config":
		msg = validateGatewayBudgetConfig(manifest)
	case "gateway-data-access-config":
		msg = validateGatewayDataAccessConfig(manifest)
	case "gateway-data-routing-config":
		msg = validateGatewayDataRoutingConfig(manifest)
	case "gateway-guardrails-config":
		msg = validateGatewayGuardrailsConfig(manifest)
	}
	if msg != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": msg})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	action := "CREATE"
	var existing map[string]any
	cfg, ok := s.gwconfigs[typ]
	if ok {
		action = "UPDATE"
		existing = cfg.manifest
	} else {
		cfg = &gatewayConfig{id: "gw-" + strconv.Itoa(s.nextID)}
		s.nextID++
	}
	if !dryRun {
		cfg.manifest = manifest
		s.gwconfigs[typ] = cfg
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"existingManifest": existing,
		"action":           action,
		"data": map[string]any{
			"id":   cfg.id,
			"name": name,
			"fqn":  Tenant + ":" + typ,
		},
	})
}

// validateGatewayBudgetConfig enforces the budget rule shape derived from the
// TrueFoundry SDK's BudgetConfig/BudgetRule schemas.
func validateGatewayBudgetConfig(manifest map[string]any) string {
	validUnits := map[string]bool{
		"cost_per_day": true, "cost_per_week": true, "cost_per_month": true,
		"cost_per_quarter": true, "tokens_per_day": true, "tokens_per_month": true,
	}
	rules, ok := manifest["rules"].([]any)
	if !ok {
		return "rules is required"
	}
	seen := map[string]bool{}
	for _, raw := range rules {
		m, _ := raw.(map[string]any)
		id, _ := m["id"].(string)
		if id == "" {
			return "id is required on budget rules"
		}
		if seen[id] {
			return fmt.Sprintf("duplicate budget rule id %q", id)
		}
		seen[id] = true
		if _, ok := m["limit_to"].(float64); !ok {
			return "limit_to is required on budget rules"
		}
		if unit, _ := m["unit"].(string); !validUnits[unit] {
			return fmt.Sprintf("unknown budget unit %q", unit)
		}
		if _, ok := m["when"].(map[string]any); !ok {
			return "when is required on budget rules"
		}
		when, _ := m["when"].(map[string]any)
		subjects, _ := when["subjects"].([]any)
		for _, s := range subjects {
			str, _ := s.(string)
			switch {
			case strings.HasPrefix(str, "user:"), strings.HasPrefix(str, "virtualaccount:"), strings.HasPrefix(str, "team:"):
			default:
				return fmt.Sprintf("Invalid subject type in rule %s. Expected user/virtualaccount/team.", id)
			}
		}
		if alerts, ok := m["alerts"].(map[string]any); ok {
			if thresholds, _ := alerts["thresholds"].([]any); len(thresholds) == 0 {
				return "alerts.thresholds must not be empty"
			}
			targets, _ := alerts["notification_target"].([]any)
			if len(targets) == 0 {
				return "alerts.notification_target must not be empty"
			}
			for _, t := range targets {
				tm, _ := t.(map[string]any)
				switch tm["type"] {
				case "email", "slack-webhook", "slack-bot", "pagerduty", "ms-teams-webhook":
				default:
					return fmt.Sprintf("unknown notification target type %q", tm["type"])
				}
				if ch, _ := tm["notification_channel"].(string); ch == "" {
					return "notification_channel is required on notification targets"
				}
			}
		}
	}
	return ""
}

// validateGatewayDataAccessConfig enforces the data access rule shape
// derived from the TrueFoundry SDK's GatewayDataAccessConfig schemas.
func validateGatewayDataAccessConfig(manifest map[string]any) string {
	rules, ok := manifest["rules"].([]any)
	if !ok {
		return "rules is required"
	}
	seen := map[string]bool{}
	for _, raw := range rules {
		m, _ := raw.(map[string]any)
		id, _ := m["id"].(string)
		if id == "" {
			return "id is required on data access rules"
		}
		if seen[id] {
			return fmt.Sprintf("duplicate data access rule id %q", id)
		}
		seen[id] = true
		scope, _ := m["scope"].(string)
		if scope != "own_data" && scope != "team_data" && scope != "all_data" {
			return fmt.Sprintf("unknown data access scope %q", scope)
		}
		if subjects, _ := m["subjects"].([]any); len(subjects) == 0 {
			return "subjects must not be empty on data access rules"
		}
		dataTypes, _ := m["data_types"].([]any)
		if len(dataTypes) == 0 {
			return "data_types must not be empty on data access rules"
		}
		for _, dt := range dataTypes {
			if dt != "traces" && dt != "metrics" {
				return fmt.Sprintf("unknown data type %q", dt)
			}
		}
		filters, hasFilters := m["filters"].([]any)
		if hasFilters && len(filters) > 0 && scope != "all_data" {
			return "filters are only allowed on all_data rules"
		}
		for _, f := range filters {
			fm, _ := f.(map[string]any)
			field, _ := fm["field"].(string)
			switch field {
			case "metadata":
				if k, _ := fm["key"].(string); k == "" {
					return "key is required on metadata filters"
				}
			case "created_by":
			default:
				return fmt.Sprintf("unknown filter field %q", field)
			}
			if op, _ := fm["operator"].(string); op != "IN" {
				return fmt.Sprintf("unknown filter operator %q", op)
			}
			if value, _ := fm["value"].([]any); len(value) == 0 {
				return "value must not be empty on filters"
			}
		}
	}
	return ""
}

// validateGatewayDataRoutingConfig enforces the destination shape derived
// from the TrueFoundry SDK's GatewayDataRoutingConfig schemas.
func validateGatewayDataRoutingConfig(manifest map[string]any) string {
	validStorage := func(m map[string]any) string {
		storage, _ := m["storage"].(map[string]any)
		if storage == nil {
			return "storage is required"
		}
		switch storage["type"] {
		case "controlplane-managed":
			if fqn, _ := storage["storage_integration_fqn"].(string); fqn != "" {
				return "storage_integration_fqn is not allowed on controlplane-managed storage"
			}
			if region, ok := storage["region"].(string); ok && region != "us" && region != "australia" && region != "india" {
				return fmt.Sprintf("unknown storage region %q", region)
			}
		case "customer-managed":
			if fqn, _ := storage["storage_integration_fqn"].(string); fqn == "" {
				return "storage_integration_fqn is required on customer-managed storage"
			}
			if _, ok := storage["region"]; ok {
				return "region is not allowed on customer-managed storage"
			}
		default:
			return fmt.Sprintf("unknown storage type %q", storage["type"])
		}
		return ""
	}

	def, _ := manifest["default"].(map[string]any)
	if def == nil {
		return "default is required"
	}
	if msg := validStorage(def); msg != "" {
		return "default: " + msg
	}
	destinations, _ := manifest["destinations"].([]any)
	seen := map[string]bool{}
	for _, raw := range destinations {
		m, _ := raw.(map[string]any)
		name, _ := m["name"].(string)
		if name == "" {
			return "name is required on destinations"
		}
		if seen[name] {
			return fmt.Sprintf("duplicate destination name %q", name)
		}
		seen[name] = true
		if _, ok := m["enabled"].(bool); !ok {
			return "enabled is required on destinations"
		}
		if msg := validStorage(m); msg != "" {
			return fmt.Sprintf("destination %s: %s", name, msg)
		}
		when, _ := m["when"].([]any)
		if len(when) == 0 {
			return "when must not be empty on destinations"
		}
		for _, c := range when {
			cm, _ := c.(map[string]any)
			field, _ := cm["field"].(string)
			switch field {
			case "metadata":
				if k, _ := cm["key"].(string); k == "" {
					return "key is required on metadata conditions"
				}
			case "created_by":
			default:
				return fmt.Sprintf("unknown condition field %q", field)
			}
			if op, _ := cm["operator"].(string); op != "IN" {
				return fmt.Sprintf("unknown condition operator %q", op)
			}
			if value, _ := cm["value"].([]any); len(value) == 0 {
				return "value must not be empty on conditions"
			}
		}
	}
	return ""
}

// validateGatewayGuardrailsConfig enforces the guardrails rule shape derived
// from the TrueFoundry SDK's GuardrailsConfig schemas.
func validateGatewayGuardrailsConfig(manifest map[string]any) string {
	validInNotIn := func(m map[string]any, what string) string {
		if cond, _ := m["condition"].(string); cond != "in" && cond != "not_in" {
			return fmt.Sprintf("unknown %s condition %q", what, m["condition"])
		}
		if values, _ := m["values"].([]any); len(values) == 0 {
			return fmt.Sprintf("%s values must not be empty", what)
		}
		return ""
	}
	rules, ok := manifest["rules"].([]any)
	if !ok {
		return "rules is required"
	}
	seen := map[string]bool{}
	for _, raw := range rules {
		m, _ := raw.(map[string]any)
		id, _ := m["id"].(string)
		if id == "" {
			return "id is required on guardrails rules"
		}
		if seen[id] {
			return fmt.Sprintf("duplicate guardrails rule id %q", id)
		}
		seen[id] = true
		when, ok := m["when"].(map[string]any)
		if !ok {
			return "when is required on guardrails rules"
		}
		if target, ok := when["target"].(map[string]any); ok {
			if op, _ := target["operator"].(string); op != "or" {
				return fmt.Sprintf("unknown target operator %q", target["operator"])
			}
			conditions, _ := target["conditions"].(map[string]any)
			if len(conditions) == 0 {
				return "target conditions must not be empty"
			}
			for key, raw := range conditions {
				cm, _ := raw.(map[string]any)
				switch key {
				case "model", "mcpServers", "mcpTools":
					if msg := validInNotIn(cm, key); msg != "" {
						return msg
					}
				default:
					return fmt.Sprintf("unknown target condition %q", key)
				}
			}
			if tools, ok := conditions["mcpTools"].(map[string]any); ok {
				values, _ := tools["values"].([]any)
				for _, v := range values {
					vm, _ := v.(map[string]any)
					if name, _ := vm["name"].(string); name == "" {
						return "name is required on mcpTools values"
					}
					if _, ok := vm["enable_all_tools"].(bool); !ok {
						return "enable_all_tools is required on mcpTools values"
					}
				}
			}
		}
		if subjects, ok := when["subjects"].(map[string]any); ok {
			if op, _ := subjects["operator"].(string); op != "and" && op != "or" {
				return fmt.Sprintf("unknown subjects operator %q", subjects["operator"])
			}
			conditions, _ := subjects["conditions"].(map[string]any)
			in, _ := conditions["in"].([]any)
			notIn, _ := conditions["not_in"].([]any)
			if len(in) == 0 && len(notIn) == 0 {
				return "subjects conditions must not be empty"
			}
		}
		for _, key := range []string{"llm_input_guardrails", "llm_output_guardrails", "mcp_tool_pre_invoke_guardrails", "mcp_tool_post_invoke_guardrails"} {
			if _, ok := m[key].([]any); !ok {
				return fmt.Sprintf("%s is required on guardrails rules", key)
			}
		}
	}
	return ""
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Manifest map[string]any `json:"manifest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Manifest == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid request body"})
		return
	}
	name, _ := req.Manifest["name"].(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	if typ, _ := req.Manifest["type"].(string); isGatewayConfigType(typ) {
		if _, ok := s.gwconfigs[typ]; !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "gateway config not found"})
			return
		}
		delete(s.gwconfigs, typ)
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if _, ok := s.accounts[name]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": fmt.Sprintf("provider account %q not found", name)})
		return
	}
	delete(s.accounts, name)
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) handleListIntegrations(w http.ResponseWriter, r *http.Request) {
	requestedType := r.URL.Query().Get("type")
	if requestedType == "" {
		requestedType = "model"
	}
	validTypes := map[string]bool{"model": true, "guardrail-config": true, "blob-storage": true, "secret-store": true, "notification-channel": true}
	if !validTypes[requestedType] {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "unsupported type filter"})
		return
	}
	fqnFilter := r.URL.Query().Get("fqn")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 100
	}

	s.mu.Lock()
	var items []map[string]any
	for name, acc := range s.accounts {
		typ, _ := acc.manifest["type"].(string)
		if integrationKindFor(typ) != requestedType {
			continue
		}
		integrationKind := requestedType
		integrations, _ := acc.manifest["integrations"].([]any)
		// The real control plane embeds the provider account (manifest
		// without the integrations list) in every integration entry.
		accountManifest := map[string]any{}
		for k, v := range acc.manifest {
			if k != "integrations" {
				accountManifest[k] = v
			}
		}
		embeddedAccount := map[string]any{
			"id":       acc.id,
			"name":     name,
			"fqn":      accountFQN(typ, name),
			"provider": strings.TrimPrefix(typ, "provider-account/"),
			"manifest": accountManifest,
		}
		for i, raw := range integrations {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			modelName, _ := m["name"].(string)
			fqn := accountFQN(typ, name) + ":" + integrationKind + ":" + modelName
			if fqnFilter != "" && fqn != fqnFilter {
				continue
			}
			items = append(items, map[string]any{
				"id":                 fmt.Sprintf("%s-int-%d", acc.id, i),
				"name":               modelName,
				"fqn":                fqn,
				"type":               integrationKind,
				"manifest":           m,
				"providerAccountFqn": accountFQN(typ, name),
				"providerAccount":    embeddedAccount,
				"createdAt":          "2026-07-15T00:00:00Z",
				"updatedAt":          "2026-07-15T00:00:00Z",
			})
		}
	}
	s.mu.Unlock()

	sort.Slice(items, func(i, j int) bool { return items[i]["fqn"].(string) < items[j]["fqn"].(string) })
	total := len(items)
	if offset > total {
		offset = total
	}
	end := min(offset+limit, total)
	page := items[offset:end]
	if page == nil {
		page = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":       page,
		"pagination": map[string]int{"total": total, "offset": offset, "limit": limit},
	})
}

// integrationKindFor returns the integration type exposed by a provider
// account type in the provider-integrations listing.
func integrationKindFor(accountType string) string {
	switch accountType {
	case "provider-account/guardrail-config-group":
		return "guardrail-config"
	case "provider-account/azure":
		return "blob-storage"
	case "provider-account/slack":
		return "notification-channel"
	case "provider-account/hashicorp":
		return "secret-store"
	case "provider-account/truefoundry":
		return "secret-store"
	default:
		return "model"
	}
}

// validateManifest enforces the per-provider-type manifest rules the real
// control plane validates, as derived from the TrueFoundry SDK schemas.
func validateManifest(typ string, manifest map[string]any) string {
	hasAPIKey := func(m map[string]any) bool {
		auth, _ := m["auth_data"].(map[string]any)
		key, _ := auth["api_key"].(string)
		return key != ""
	}
	integrations, _ := manifest["integrations"].([]any)

	switch typ {
	case "provider-account/openai":
		if !hasAPIKey(manifest) {
			return "auth_data with api_key is required"
		}
	case "provider-account/azure-openai":
		if !hasAPIKey(manifest) {
			return "auth_data with api_key is required"
		}
		if endpoint, _ := manifest["azure_endpoint"].(string); endpoint == "" {
			return "azure_endpoint is required"
		}
		for _, raw := range integrations {
			m, _ := raw.(map[string]any)
			if v, _ := m["api_version"].(string); v == "" {
				return "api_version is required on azure-openai model integrations"
			}
		}
	case "provider-account/azure-foundry":
		for _, raw := range integrations {
			m, _ := raw.(map[string]any)
			if !hasAPIKey(m) {
				return "auth_data with api_key is required on azure-foundry model integrations"
			}
			if endpoint, _ := m["azure_endpoint"].(string); endpoint == "" {
				return "azure_endpoint is required on azure-foundry model integrations"
			}
		}
	case "provider-account/virtual-model":
		for _, raw := range integrations {
			m, _ := raw.(map[string]any)
			routing, _ := m["routing_config"].(map[string]any)
			if routing == nil {
				return "routing_config is required on virtual model integrations"
			}
			routingType, _ := routing["type"].(string)
			if routingType != "weight-based-routing" && routingType != "priority-based-routing" && routingType != "latency-based-routing" {
				return fmt.Sprintf("unknown routing_config type %q", routingType)
			}
			targets, _ := routing["load_balance_targets"].([]any)
			if len(targets) == 0 {
				return "routing_config needs at least one load_balance_target"
			}
		}
	case "provider-account/aws-bedrock":
		if region, _ := manifest["region"].(string); region == "" {
			return "region is required"
		}
		auth, _ := manifest["auth_data"].(map[string]any)
		apiKey, _ := auth["api_key"].(string)
		accessKey, _ := auth["access_key_id"].(string)
		secretKey, _ := auth["secret_access_key"].(string)
		if apiKey == "" && (accessKey == "" || secretKey == "") {
			return "auth_data with api_key or access_key_id/secret_access_key is required"
		}
	case "provider-account/custom-endpoint":
		routingType, _ := manifest["routing_type"].(string)
		if routingType != "" && routingType != "weight" && routingType != "priority" {
			return fmt.Sprintf("unknown routing_type %q", routingType)
		}
		for _, raw := range integrations {
			m, _ := raw.(map[string]any)
			if u, _ := m["base_url"].(string); u == "" {
				return "base_url is required on custom endpoint integrations"
			}
			if _, hasLB := m["loadbalancing_config"].(map[string]any); routingType != "" && !hasLB {
				return "loadbalancing_config is required when routing_type is set"
			}
		}
	case "provider-account/azure":
		for _, raw := range integrations {
			m, _ := raw.(map[string]any)
			// Azure accounts also support AKS/ACR/key-vault/repos
			// integrations; the mock (like the provider) models blob
			// storage only.
			if m["type"] != "integration/blob-storage/azure/blob" {
				return fmt.Sprintf("unknown azure integration type %q", m["type"])
			}
			if root, _ := m["storage_root"].(string); root == "" {
				return "storage_root is required on blob storage integrations"
			}
			if auth, ok := m["auth_data"].(map[string]any); ok {
				cs, _ := auth["connection_string"].(string)
				if auth["type"] != "connection-string" || cs == "" {
					return "auth_data must be type connection-string with connection_string"
				}
			}
		}
	case "provider-account/truefoundry":
		for _, raw := range integrations {
			m, _ := raw.(map[string]any)
			if m["type"] != "integration/secret-store/truefoundry/db" {
				return fmt.Sprintf("unknown truefoundry integration type %q", m["type"])
			}
		}
	case "provider-account/hashicorp":
		for _, raw := range integrations {
			m, _ := raw.(map[string]any)
			if m["type"] != "integration/secret-store/hashicorp/vault" {
				return fmt.Sprintf("unknown hashicorp integration type %q", m["type"])
			}
			if u, _ := m["vault_url"].(string); u == "" {
				return "vault_url is required on vault integrations"
			}
			if p, _ := m["kv_mount_path"].(string); p == "" {
				return "kv_mount_path is required on vault integrations"
			}
			auth, _ := m["auth_data"].(map[string]any)
			switch auth["type"] {
			case "token":
				if t, _ := auth["token"].(string); t == "" {
					return "auth_data type token requires token"
				}
			case "approle":
				roleID, _ := auth["role_id"].(string)
				secretID, _ := auth["secret_id"].(string)
				if roleID == "" || secretID == "" {
					return "auth_data type approle requires role_id and secret_id"
				}
			default:
				return "auth_data must be type token or approle"
			}
		}
	case "provider-account/slack":
		for _, raw := range integrations {
			m, _ := raw.(map[string]any)
			auth, _ := m["auth_data"].(map[string]any)
			switch m["type"] {
			case "integration/notification-channel/slack-webhook":
				if u, _ := auth["webhook_url"].(string); auth["type"] != "slack-webhook" || u == "" {
					return "auth_data must be type slack-webhook with webhook_url"
				}
			case "integration/notification-channel/slack-bot":
				if t, _ := auth["bot_token"].(string); auth["type"] != "slack-bot" || t == "" {
					return "auth_data must be type slack-bot with bot_token"
				}
			default:
				return fmt.Sprintf("unknown slack integration type %q", m["type"])
			}
		}
	case "provider-account/guardrail-config-group":
		for _, raw := range integrations {
			m, _ := raw.(map[string]any)
			if !hasAPIKey(m) {
				return "auth_data with api_key is required on guardrail configs"
			}
			cfg, _ := m["config"].(map[string]any)
			if rn, _ := cfg["resource_name"].(string); rn == "" {
				return "config.resource_name is required on guardrail configs"
			}
			if av, _ := cfg["api_version"].(string); av == "" {
				return "config.api_version is required on guardrail configs"
			}
			switch m["type"] {
			case "integration/guardrail-config/azure-content-safety":
				if _, ok := cfg["severity_threshold"].(float64); !ok {
					return "config.severity_threshold is required on azure-content-safety guardrails"
				}
				if cats, _ := cfg["categories"].([]any); len(cats) == 0 {
					return "config.categories is required on azure-content-safety guardrails"
				}
			case "integration/guardrail-config/azure-prompt-shield":
				// no extra config fields
			default:
				return fmt.Sprintf("unknown guardrail config type %q", m["type"])
			}
		}
	default:
		return fmt.Sprintf("unknown provider account type %q", typ)
	}
	return ""
}

// accountFQN mimics the control plane FQN scheme, e.g.
// "mock-tenant:openai:my-account" for type "provider-account/openai".
func accountFQN(manifestType, name string) string {
	provider := strings.TrimPrefix(manifestType, "provider-account/")
	return Tenant + ":" + provider + ":" + name
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
