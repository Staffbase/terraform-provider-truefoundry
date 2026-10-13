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
	"maps"
	"net/http"
	"regexp"
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

	mu           sync.Mutex
	accounts     map[string]*account        // keyed by manifest name
	vaccounts    map[string]*virtualAccount // keyed by manifest name
	gwconfigs    map[string]*gatewayConfig  // keyed by manifest type, e.g. "gateway-budget-config"
	budgets      map[string]*budgetV2       // v2 budget rules, keyed by manifest name
	mcpservers   map[string]*mcpServer      // keyed by manifest name
	secretgroups map[string]*secretGroup    // keyed by manifest name
	roles        map[string]*role           // keyed by manifest name
	agents       map[string]*agent          // keyed by manifest name
	mlrepos      map[string]*mlRepo         // keyed by manifest name
	skills       map[string]*artifactSeries // keyed by manifest name
	prompts      map[string]*artifactSeries // keyed by manifest name
	roleBindings map[string]*roleBinding    // keyed by manifest name
	nextID       int
	tokenReads   int // count of GET .../virtual-accounts/{id}/token requests

	secretValuesHidden  bool // see HideSecretValues
	legacyGrantsRetired bool // see RetireLegacyGrants
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

type mcpServer struct {
	id       string
	manifest map[string]any
}

type agent struct {
	id            string
	latestVersion int
	manifest      map[string]any
}

// New returns a mock control plane that requires the given bearer API key.
func New(apiKey string) *Server {
	s := &Server{apiKey: apiKey, accounts: map[string]*account{}, vaccounts: map[string]*virtualAccount{}, gwconfigs: map[string]*gatewayConfig{}, budgets: map[string]*budgetV2{}, mcpservers: map[string]*mcpServer{}, secretgroups: map[string]*secretGroup{}, roles: map[string]*role{}, agents: map[string]*agent{}, mlrepos: map[string]*mlRepo{}, skills: map[string]*artifactSeries{}, prompts: map[string]*artifactSeries{}, roleBindings: map[string]*roleBinding{}, nextID: 1}
	s.seedBuiltinRoles()
	return s
}

// Handler returns the HTTP handler implementing the API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/svc/v1/apply", s.auth(s.handleApply))
	mux.HandleFunc("POST /api/svc/v1/delete", s.auth(s.handleDelete))
	mux.HandleFunc("GET /api/svc/v1/provider-integrations", s.auth(s.handleListIntegrations))
	mux.HandleFunc("GET /api/svc/v1/mcp-servers", s.auth(s.handleListMcpServers))
	mux.HandleFunc("GET /api/svc/v1/secret-groups", s.auth(s.handleListSecretGroups))
	mux.HandleFunc("GET /api/svc/v1/secret-groups/{id}", s.auth(s.handleGetSecretGroup))
	mux.HandleFunc("PUT /api/svc/v1/secret-groups/{id}", s.auth(s.handleUpdateSecretGroup))
	mux.HandleFunc("GET /api/svc/v1/secrets/{id}", s.auth(s.handleGetSecret))
	mux.HandleFunc("DELETE /api/svc/v1/secrets/{id}", s.auth(s.handleDeleteSecret))
	mux.HandleFunc("GET /api/svc/v1/llm-gateway/config/{type}", s.auth(s.handleGetGatewayConfig))
	mux.HandleFunc("GET /api/svc/v1/llm-gateway/budgets", s.auth(s.handleListBudgets))
	mux.HandleFunc("GET /api/svc/v1/role", s.auth(s.handleListRoles))
	mux.HandleFunc("PUT /api/svc/v1/virtual-accounts", s.auth(s.handleApplyVirtualAccount))
	mux.HandleFunc("GET /api/svc/v1/virtual-accounts/{id}", s.auth(s.handleGetVirtualAccount))
	mux.HandleFunc("DELETE /api/svc/v1/virtual-accounts/{id}", s.auth(s.handleDeleteVirtualAccount))
	mux.HandleFunc("GET /api/svc/v1/virtual-accounts/{id}/token", s.auth(s.handleGetVirtualAccountToken))
	mux.HandleFunc("PUT /api/svc/v1/agents", s.auth(s.handleApplyAgent))
	mux.HandleFunc("GET /api/svc/v1/agents", s.auth(s.handleListAgents))
	mux.HandleFunc("DELETE /api/svc/v1/agents/{id}", s.auth(s.handleDeleteAgent))
	mux.HandleFunc("PUT /api/svc/v1/ml-repos", s.auth(s.handleApplyMlRepo))
	mux.HandleFunc("GET /api/svc/v1/ml-repos", s.auth(s.handleListMlRepos))
	mux.HandleFunc("DELETE /api/svc/v1/ml-repos/{id}", s.auth(s.handleDeleteMlRepo))
	mux.HandleFunc("PUT /api/svc/v1/agent-skill-versions", s.auth(s.handleApplyAgentSkill))
	mux.HandleFunc("GET /api/svc/v1/agent-skills", s.auth(s.handleListAgentSkills))
	mux.HandleFunc("DELETE /api/svc/v1/agent-skills/{id}", s.auth(s.handleDeleteAgentSkill))
	mux.HandleFunc("PUT /api/svc/v1/prompt-versions", s.auth(s.handleApplyPrompt))
	mux.HandleFunc("GET /api/svc/v1/prompts", s.auth(s.handleListPrompts))
	mux.HandleFunc("DELETE /api/svc/v1/prompts/{id}", s.auth(s.handleDeletePrompt))
	mux.HandleFunc("PUT /api/svc/v1/role-bindings", s.auth(s.handleApplyRoleBinding))
	mux.HandleFunc("GET /api/svc/v1/role-bindings", s.auth(s.handleListRoleBindings))
	mux.HandleFunc("GET /api/svc/v1/role-bindings/{id}", s.auth(s.handleGetRoleBinding))
	mux.HandleFunc("DELETE /api/svc/v1/role-bindings/{id}", s.auth(s.handleDeleteRoleBinding))
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

// DropIntegrationAuthField removes field from the auth_data of every
// integration of the named account out-of-band, simulating a manifest stored
// without it (e.g. by an older client).
func (s *Server) DropIntegrationAuthField(name, field string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[name]
	if !ok {
		return
	}
	integrations, _ := a.manifest["integrations"].([]any)
	for _, raw := range integrations {
		m, _ := raw.(map[string]any)
		if auth, ok := m["auth_data"].(map[string]any); ok {
			delete(auth, field)
		}
	}
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

// McpServerCount returns the number of stored MCP servers.
func (s *Server) McpServerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.mcpservers)
}

// DeleteMcpServer removes an MCP server out-of-band (simulates drift/manual deletion).
func (s *Server) DeleteMcpServer(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mcpservers, name)
}

// AgentCount returns the number of stored agents.
func (s *Server) AgentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.agents)
}

// DeleteAgentByName removes an agent out-of-band (simulates drift/manual deletion).
func (s *Server) DeleteAgentByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.agents, name)
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
	if !accountNameRe.MatchString(name) || len(name) < 3 || len(name) > 36 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": fmt.Sprintf("name must be 3-36 characters and match %q", accountNameRe.String())})
		return
	}
	if raw, ok := req.Manifest["permissions"]; ok {
		if permissions, ok := raw.([]any); !ok || len(permissions) == 0 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "permissions must not be empty; delete the virtual account instead"})
			return
		}
	}
	if tt, ok := req.Manifest["token_type"].(string); ok && tt != "jwt" && tt != "opaque" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "token_type must be jwt or opaque"})
		return
	}
	if ar, ok := req.Manifest["auto_rotate"].(map[string]any); ok {
		interval, _ := ar["auto_rotate_interval"].(float64)
		grace, _ := ar["grace_period"].(float64)
		if interval < 30 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "auto_rotate.auto_rotate_interval must be at least 30"})
			return
		}
		if grace < 1 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "auto_rotate.grace_period must be at least 1"})
			return
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeLegacyGrantError(w, req.Manifest, "permissions") {
		return
	}
	va, exists := s.vaccounts[name]
	if !exists {
		va = &virtualAccount{id: "va-" + strconv.Itoa(s.nextID)}
		va.token = "mock-token-" + va.id
		s.nextID++
	}
	carryOverLegacyGrant(va.manifest, req.Manifest, "permissions")
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
	s.tokenReads++
	va := s.findVirtualAccount(r.PathValue("id"))
	if va == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "virtual account not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": va.token})
}

// VirtualAccountTokenReadCount returns how many times the token endpoint has
// been requested, for asserting that reads only fetch it when needed.
func (s *Server) VirtualAccountTokenReadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenReads
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
		s.applyGatewayConfig(w, typ, req.Manifest, req.DryRun)
		return
	}
	if isBudgetV2Type(typ) {
		s.applyBudgetV2(w, typ, name, req.Manifest, req.DryRun)
		return
	}
	if isMcpServerType(typ) {
		s.applyMcpServer(w, typ, name, req.Manifest, req.DryRun)
		return
	}
	if isRoleType(typ) {
		s.applyRole(w, typ, name, req.Manifest, req.DryRun)
		return
	}
	if isSecretGroupType(typ) {
		s.applySecretGroup(w, typ, name, req.Manifest, req.DryRun)
		return
	}
	if !strings.HasPrefix(typ, "provider-account/") {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": fmt.Sprintf("unsupported manifest type %q", typ)})
		return
	}
	if msg := validateAccountName(typ, name); msg != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": msg})
		return
	}
	if msg := validateManifest(typ, req.Manifest); msg != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": msg})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeLegacyGrantError(w, req.Manifest, "collaborators") {
		return
	}
	action := "CREATE"
	var existing map[string]any
	acc, ok := s.accounts[name]
	if ok {
		action = "UPDATE"
		existing = acc.manifest
		carryOverLegacyGrant(existing, req.Manifest, "collaborators")
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
	return typ == "gateway-budget-config" || typ == "gateway-data-access-config" || typ == "gateway-data-routing-config" || typ == "gateway-guardrails-config" || typ == "gateway-global-settings" || typ == "gateway-rate-limiting-config" || typ == "gateway-load-balancing-config" || typ == "gateway-otel-config"
}

// isMcpServerType reports whether typ is a standalone MCP server manifest
// type the mock models.
func isMcpServerType(typ string) bool {
	return typ == "mcp-server/remote" || typ == "mcp-server/openapi" || typ == "mcp-server/virtual"
}

// applyMcpServer stores a standalone MCP server manifest, keyed by name.
// Unlike provider accounts, the real control plane's apply response for MCP
// servers carries no data.id — clients resolve the ID via the list endpoint.
func (s *Server) applyMcpServer(w http.ResponseWriter, typ, name string, manifest map[string]any, dryRun bool) {
	if !mcpServerNameRe.MatchString(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Validation Error: %s /name: must match pattern %q", mcpManifestSchemaName(typ), mcpServerNameRe.String())})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeLegacyGrantError(w, manifest, "collaborators") {
		return
	}
	if msg := s.validateMcpServerManifest(typ, manifest); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": msg})
		return
	}

	action := "CREATE"
	var existing map[string]any
	srv, ok := s.mcpservers[name]
	if ok {
		action = "UPDATE"
		existing = srv.manifest
		carryOverLegacyGrant(existing, manifest, "collaborators")
	} else {
		srv = &mcpServer{id: "mcp-" + strconv.Itoa(s.nextID)}
		s.nextID++
	}
	if !dryRun {
		srv.manifest = manifest
		s.mcpservers[name] = srv
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"existingManifest": existing,
		"action":           action,
		"data": map[string]any{
			"name":     name,
			"fqn":      Tenant + ":mcp-server:" + name,
			"manifest": manifest,
			"proxyUrl": "{{mcpProxyBaseURL}}/" + Tenant + "/mcp/" + name + "/server",
		},
	})
}

// accountNameRe is the provider account name pattern the real control plane
// enforces: "^[a-z][a-z0-9\\-]{1,N}[a-z0-9]$" for every account type, with N
// depending on the type (see maxAccountNameLenFor).
var accountNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)

// maxAccountNameLenFor is the longest name each provider account type accepts,
// read off a live control plane on 2026-09-08 with dryRun applies.
func maxAccountNameLenFor(typ string) int {
	switch typ {
	case "provider-account/virtual-model":
		return 64
	case "provider-account/azure", "provider-account/hashicorp", "provider-account/slack",
		"provider-account/guardrail-config-group":
		return 36
	default:
		return 32
	}
}

// validateAccountName returns a non-empty message when name violates the
// pattern or the type's length bound.
func validateAccountName(typ, name string) string {
	max := maxAccountNameLenFor(typ)
	if !accountNameRe.MatchString(name) || len(name) < 3 || len(name) > max {
		return fmt.Sprintf("name must be 3-%d characters and match %q", max, accountNameRe.String())
	}
	return ""
}

// mcpServerNameRe is the MCP server name pattern the real control plane
// enforces.
var mcpServerNameRe = regexp.MustCompile(`^[a-z][a-z0-9\-]{1,71}[a-z0-9]$`)

// mcpManifestSchemaName returns the schema name the real control plane uses
// in validation error messages for an MCP server manifest type.
func mcpManifestSchemaName(typ string) string {
	switch typ {
	case "mcp-server/virtual":
		return "VirtualMCPServerManifest"
	case "mcp-server/openapi":
		return "OpenAPIMCPServerManifest"
	}
	return "RemoteMCPServerManifest"
}

// validateMcpServerManifest enforces the MCP server manifest rules derived
// from the TrueFoundry SDK schemas and observed control plane behavior.
// Callers must hold s.mu (virtual servers check their source servers exist).
func (s *Server) validateMcpServerManifest(typ string, manifest map[string]any) string {
	if desc, _ := manifest["description"].(string); desc == "" {
		return "description is required"
	}

	validateHeaders := func(auth map[string]any) string {
		headers, ok := auth["headers"].(map[string]any)
		if !ok || len(headers) == 0 {
			return "auth_data of type header requires headers"
		}
		return ""
	}

	validateAuthData := func(manifest map[string]any) string {
		auth, ok := manifest["auth_data"].(map[string]any)
		if !ok {
			return ""
		}
		switch authType, _ := auth["type"].(string); authType {
		case "passthrough":
		case "header":
			if msg := validateHeaders(auth); msg != "" {
				return msg
			}
			if level, _ := auth["auth_level"].(string); level != "global" && level != "per_user" {
				return "auth_data of type header requires auth_level global or per_user"
			}
		case "oauth2":
			if u, _ := auth["token_url"].(string); u == "" {
				return "auth_data of type oauth2 requires token_url"
			}
			if gt, _ := auth["grant_type"].(string); gt != "authorization_code" && gt != "client_credentials" {
				return "auth_data of type oauth2 requires grant_type authorization_code or client_credentials"
			}
			if p, _ := auth["provider"].(string); p != "custom" && p != "auth0" && p != "okta" {
				return "auth_data of type oauth2 requires provider custom, auth0, or okta"
			}
			if js, _ := auth["jwt_source"].(string); js != "access_token" && js != "id_token" {
				return "auth_data of type oauth2 requires jwt_source access_token or id_token"
			}
		default:
			return "auth_data type must be header, oauth2, or passthrough"
		}
		return ""
	}

	switch typ {
	case "mcp-server/remote":
		if u, _ := manifest["url"].(string); u == "" {
			return "url is required for a remote MCP server"
		}
		if msg := validateAuthData(manifest); msg != "" {
			return msg
		}
		for _, raw := range toolSettingsOf(manifest) {
			setting, _ := raw.(map[string]any)
			if toolName, _ := setting["name"].(string); toolName == "" {
				return "tool_settings entries require a name"
			}
		}
	case "mcp-server/openapi":
		if u, _ := manifest["url"].(string); u == "" {
			return "url is required for an OpenAPI MCP server"
		}
		source, ok := manifest["openapi_spec_source"].(map[string]any)
		if !ok {
			return "openapi_spec_source is required for an OpenAPI MCP server"
		}
		switch sourceType, _ := source["type"].(string); sourceType {
		case "remote", "upload":
			if u, _ := source["url"].(string); u == "" {
				return "openapi_spec_source of type " + sourceType + " requires url"
			}
		case "inline":
			if spec, _ := source["openapi_spec"].(string); spec == "" {
				if _, ok := source["openapi_spec"].(map[string]any); !ok {
					return "openapi_spec_source of type inline requires openapi_spec"
				}
			}
		default:
			return "openapi_spec_source type must be remote, upload, or inline"
		}
		if msg := validateAuthData(manifest); msg != "" {
			return msg
		}
		for _, raw := range toolSettingsOf(manifest) {
			setting, _ := raw.(map[string]any)
			method, _ := setting["method"].(string)
			switch method {
			case "get", "post", "put", "patch", "delete", "head":
			default:
				return "tool_settings entries require a method of get, post, put, patch, delete, or head"
			}
			for _, key := range []string{"path", "name", "description"} {
				if v, _ := setting[key].(string); v == "" {
					return "tool_settings entries require a " + key
				}
			}
		}
	case "mcp-server/virtual":
		servers, _ := manifest["servers"].([]any)
		if len(servers) == 0 {
			return "a virtual MCP server needs at least one source server"
		}
		for _, raw := range servers {
			source, _ := raw.(map[string]any)
			sourceName, _ := source["name"].(string)
			if sourceName == "" {
				return "servers entries require a name"
			}
			if _, ok := s.mcpservers[sourceName]; !ok {
				return fmt.Sprintf("Virtual MCP Server validation failed:\nSource server %q not found", sourceName)
			}
			if auth, ok := source["auth_data"].(map[string]any); ok {
				if authType, _ := auth["type"].(string); authType != "header" {
					return "source server auth_data type must be header"
				}
				if msg := validateHeaders(auth); msg != "" {
					return msg
				}
			}
		}
	}
	return ""
}

func toolSettingsOf(manifest map[string]any) []any {
	settings, _ := manifest["tool_settings"].([]any)
	return settings
}

// handleListMcpServers implements GET /api/svc/v1/mcp-servers.
func (s *Server) handleListMcpServers(w http.ResponseWriter, r *http.Request) {
	fqnFilter := r.URL.Query().Get("fqn")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 100
	}

	s.mu.Lock()
	var items []map[string]any
	for name, srv := range s.mcpservers {
		fqn := Tenant + ":mcp-server:" + name
		if fqnFilter != "" && fqn != fqnFilter {
			continue
		}
		items = append(items, map[string]any{
			"id":           srv.id,
			"name":         name,
			"fqn":          fqn,
			"manifest":     srv.manifest,
			"accountId":    "acc-" + Tenant,
			"createdAt":    "2026-07-15T00:00:00Z",
			"updatedAt":    "2026-07-15T00:00:00Z",
			"isTfyManaged": false,
			"proxyUrl":     "{{mcpProxyBaseURL}}/" + Tenant + "/mcp/" + name + "/server",
		})
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

// agentNameRe is (an equivalent of) the agent name pattern the real control
// plane enforces: `^[a-z](?:[a-z0-9]|-(?!-)){1,30}[a-z0-9]$` — Go regexp has
// no lookahead, so double hyphens are excluded structurally and the length is
// checked separately.
var agentNameRe = regexp.MustCompile(`^[a-z](-?[a-z0-9]){2,31}$`)

// handleApplyAgent implements PUT /api/svc/v1/agents: create an agent or a
// new version of an existing one. Verified against a real control plane
// (2026-07-27): the response carries the new AgentVersion under data, dryRun
// validates without persisting, and referenced MCP servers must exist (404).
func (s *Server) handleApplyAgent(w http.ResponseWriter, r *http.Request) {
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
	if typ != "truefoundry-agent" && typ != "remote-agent/self-hosted" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Validation Error: AgentManifest : unsupported manifest type %q", typ)})
		return
	}
	if !agentNameRe.MatchString(name) || len(name) > 32 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": `Validation Error: AgentManifest /name: must match pattern "^[a-z](?:[a-z0-9]|-(?!-)){1,30}[a-z0-9]$"`})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeLegacyGrantError(w, req.Manifest, "collaborators") {
		return
	}
	if msg := s.validateAgentManifest(typ, req.Manifest); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": msg})
		return
	}
	if missing := s.missingAgentMcpServers(req.Manifest); len(missing) > 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": fmt.Sprintf("MCP server(s) not found: %q", strings.Join(missing, `", "`))})
		return
	}

	a, exists := s.agents[name]
	if !exists {
		a = &agent{id: "agent-" + strconv.Itoa(s.nextID)}
		s.nextID++
	}
	carryOverLegacyGrant(a.manifest, req.Manifest, "collaborators")
	version := a.latestVersion + 1
	if !req.DryRun {
		a.latestVersion = version
		a.manifest = req.Manifest
		s.agents[name] = a
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": agentVersionJSON(a.id, name, req.Manifest, version),
	})
}

// validateAgentManifest enforces the agent manifest rules derived from the
// TrueFoundry SDK schemas and dryRun applies against a real control plane.
func (s *Server) validateAgentManifest(typ string, manifest map[string]any) string {
	if desc, _ := manifest["description"].(string); desc == "" {
		return "description is required"
	}
	switch typ {
	case "truefoundry-agent":
		model, _ := manifest["model"].(map[string]any)
		if modelName, _ := model["name"].(string); modelName == "" {
			return "Validation Error: AgentManifest : must have required property 'model'"
		}
		for _, raw := range listOf(manifest, "skills") {
			skill, _ := raw.(map[string]any)
			if fqn, _ := skill["fqn"].(string); fqn == "" {
				return "skills entries require an fqn"
			}
		}
		for _, raw := range listOf(manifest, "mcp_servers") {
			server, _ := raw.(map[string]any)
			if serverName, _ := server["name"].(string); serverName == "" {
				return "mcp_servers entries require a name"
			}
		}
		if format, ok := manifest["response_format"].(map[string]any); ok {
			switch formatType, _ := format["type"].(string); formatType {
			case "text", "json_object":
			case "json_schema":
				schema, _ := format["json_schema"].(map[string]any)
				if schemaName, _ := schema["name"].(string); schemaName == "" {
					return "response_format of type json_schema requires json_schema with a name"
				}
			default:
				return "response_format type must be text, json_schema, or json_object"
			}
		}
	case "remote-agent/self-hosted":
		if u, _ := manifest["url"].(string); u == "" {
			return "url is required for a self-hosted remote agent"
		}
		framework, ok := manifest["framework"].(map[string]any)
		if !ok {
			return "framework is required for a self-hosted remote agent"
		}
		switch frameworkType, _ := framework["type"].(string); frameworkType {
		case "a2a":
			if p, _ := framework["agent_card_path"].(string); p == "" {
				return "framework of type a2a requires agent_card_path"
			}
		case "custom":
		default:
			return "framework type must be a2a or custom"
		}
		if auth, ok := manifest["auth_data"].(map[string]any); ok {
			switch authType, _ := auth["type"].(string); authType {
			case "passthrough":
			case "header":
				if headers, _ := auth["headers"].(map[string]any); len(headers) == 0 {
					return "auth_data of type header requires headers"
				}
			default:
				return "auth_data type must be header or passthrough"
			}
		}
	}
	return ""
}

// missingAgentMcpServers returns the MCP servers referenced by a
// truefoundry-agent manifest that do not exist. Callers must hold s.mu.
func (s *Server) missingAgentMcpServers(manifest map[string]any) []string {
	var missing []string
	for _, raw := range listOf(manifest, "mcp_servers") {
		server, _ := raw.(map[string]any)
		name, _ := server["name"].(string)
		if _, ok := s.mcpservers[name]; name != "" && !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func listOf(manifest map[string]any, key string) []any {
	list, _ := manifest[key].([]any)
	return list
}

// agentVersionJSON renders an AgentVersion response object.
func agentVersionJSON(agentID, name string, manifest map[string]any, version int) map[string]any {
	return map[string]any{
		"id":       fmt.Sprintf("%s-v%d", agentID, version),
		"agentId":  agentID,
		"fqn":      fmt.Sprintf("%s:agent:%s:%d", Tenant, name, version),
		"manifest": manifest,
		"version":  version,
		"createdBySubject": map[string]any{
			"subjectId": "mock-subject", "subjectType": "user", "subjectSlug": "mock-user", "subjectDisplayName": "Mock User",
		},
		"createdAt": "2026-07-15T00:00:00Z",
		"updatedAt": "2026-07-15T00:00:00Z",
	}
}

// agentTypeDiscriminator maps a manifest type to the list entry's type field.
func agentTypeDiscriminator(manifestType string) string {
	if manifestType == "remote-agent/self-hosted" {
		return "remote-agent"
	}
	return manifestType
}

// handleListAgents implements GET /api/svc/v1/agents.
func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	nameFilter := r.URL.Query().Get("name")
	prefixFilter := r.URL.Query().Get("namePrefix")
	typeFilter := r.URL.Query().Get("type")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 100
	}

	s.mu.Lock()
	var items []map[string]any
	for name, a := range s.agents {
		manifestType, _ := a.manifest["type"].(string)
		discriminator := agentTypeDiscriminator(manifestType)
		if nameFilter != "" && name != nameFilter {
			continue
		}
		if nameFilter == "" && prefixFilter != "" && !strings.HasPrefix(name, prefixFilter) {
			continue
		}
		if typeFilter != "" && discriminator != typeFilter {
			continue
		}
		items = append(items, map[string]any{
			"id":                   a.id,
			"name":                 name,
			"fqn":                  Tenant + ":agent:" + name,
			"tenantName":           Tenant,
			"accountId":            "acc-" + Tenant,
			"latestVersion":        a.latestVersion,
			"type":                 discriminator,
			"latestVersionDetails": agentVersionJSON(a.id, name, a.manifest, a.latestVersion),
			"createdBy":            "mock-user@example.com",
			"createdAt":            "2026-07-15T00:00:00Z",
			"updatedAt":            "2026-07-15T00:00:00Z",
		})
	}
	s.mu.Unlock()

	sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
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

// handleDeleteAgent implements DELETE /api/svc/v1/agents/{id}.
func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, a := range s.agents {
		if a.id == r.PathValue("id") {
			delete(s.agents, name)
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "agent not found"})
}

// applyGatewayConfig stores a tenant-wide gateway config (e.g.
// "gateway-budget-config") keyed by its manifest type.
// The real control plane applies no name pattern or length rule to gateway
// config manifests at all (verified 2026-09-08: 1- and 300-character names are
// both accepted), so neither does the mock — hence no name parameter.
func (s *Server) applyGatewayConfig(w http.ResponseWriter, typ string, manifest map[string]any, dryRun bool) {
	name, _ := manifest["name"].(string)
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
	case "gateway-rate-limiting-config":
		msg = validateGatewayRateLimitingConfig(manifest)
	case "gateway-load-balancing-config":
		msg = validateGatewayLoadBalancingConfig(manifest)
	case "gateway-global-settings":
		msg = validateGatewayGlobalSettings(manifest)
	case "gateway-otel-config":
		msg = validateGatewayOtelConfig(manifest)
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

// rateLimitRuleIDRe is the rate limiting rule id pattern the real control
// plane enforces; the braces allow templated ids.
var rateLimitRuleIDRe = regexp.MustCompile(`^[A-Za-z{}][A-Za-z0-9{}._-]{0,60}[A-Za-z0-9{}._]$`)

// rateLimitMetadataAppliesPerRe matches the "metadata.<key>" partitioning.
var rateLimitMetadataAppliesPerRe = regexp.MustCompile(`^metadata\.[A-Za-z0-9_-]+$`)

// validateGatewayRateLimitingConfig enforces the rate limiting rule shape
// derived from the TrueFoundry SDK's RateLimitConfig/RateLimitRule schemas
// plus the semantic checks a deployed control plane applies (rule id pattern,
// unit vocabulary, and the applies-per vocabulary and its two-value cap).
func validateGatewayRateLimitingConfig(manifest map[string]any) string {
	validUnits := map[string]bool{
		"requests_per_minute": true, "requests_per_hour": true, "requests_per_day": true,
		"tokens_per_minute": true, "tokens_per_hour": true, "tokens_per_day": true,
	}
	validAppliesPer := map[string]bool{"user": true, "virtualaccount": true, "model": true}
	rules, ok := manifest["rules"].([]any)
	if !ok {
		return "rules is required"
	}
	seen := map[string]bool{}
	for _, raw := range rules {
		m, _ := raw.(map[string]any)
		id, _ := m["id"].(string)
		if !rateLimitRuleIDRe.MatchString(id) {
			return fmt.Sprintf("Validation Error: GatewayConfig /rules/0/id: must match pattern %q", rateLimitRuleIDRe.String())
		}
		if seen[id] {
			return fmt.Sprintf("duplicate rate limiting rule id %q", id)
		}
		seen[id] = true
		if _, ok := m["limit_to"].(float64); !ok {
			return "limit_to is required on rate limiting rules"
		}
		if unit, _ := m["unit"].(string); !validUnits[unit] {
			return fmt.Sprintf("Validation Error: GatewayConfig /rules/0/unit: must be equal to one of the allowed values (got %q)", unit)
		}
		if _, ok := m["when"].(map[string]any); !ok {
			return "when is required on rate limiting rules"
		}
		appliesPer, _ := m["rate_limit_applies_per"].([]any)
		if len(appliesPer) > 2 {
			return fmt.Sprintf("Rate limit rule %s cannot have more than two rate limit applies per values.", id)
		}
		for _, raw := range appliesPer {
			v, _ := raw.(string)
			if validAppliesPer[v] || rateLimitMetadataAppliesPerRe.MatchString(v) {
				continue
			}
			return fmt.Sprintf("Rate limiting rule %s has invalid rate limit applies per value %s. "+
				"Allowed values are: user, virtualaccount, model, or metadata.* (e.g., metadata.appId).", id, v)
		}
	}
	return ""
}

// loadBalancingRuleIDRe is the load balancing rule id pattern the real
// control plane enforces.
var loadBalancingRuleIDRe = regexp.MustCompile(`^[a-zA-Z](?:[a-zA-Z0-9\-]{0,60}[a-zA-Z0-9])$`)

// validateGatewayLoadBalancingConfig enforces the load balancing rule shape
// derived from the TrueFoundry SDK's LoadBalancingConfig schemas plus the rule
// id pattern a deployed control plane enforces. The weight/priority checks
// mirror the real control plane, which rejects a weight-based target without
// `weight` and a priority-based one without `priority`. It does not reject the
// converse (a `weight` on a priority- or latency-based target), so neither
// does this mock — that rule lives in the resource's ValidateConfig only.
func validateGatewayLoadBalancingConfig(manifest map[string]any) string {
	validTypes := map[string]bool{
		"weight-based-routing": true, "latency-based-routing": true, "priority-based-routing": true,
	}
	rules, ok := manifest["rules"].([]any)
	if !ok {
		return "rules is required"
	}
	seen := map[string]bool{}
	for _, raw := range rules {
		m, _ := raw.(map[string]any)
		id, _ := m["id"].(string)
		if !loadBalancingRuleIDRe.MatchString(id) {
			return fmt.Sprintf("Validation Error: GatewayConfig /rules/0/id: must match pattern %q", loadBalancingRuleIDRe.String())
		}
		if seen[id] {
			return fmt.Sprintf("duplicate load balancing rule id %q", id)
		}
		seen[id] = true
		typ, _ := m["type"].(string)
		if !validTypes[typ] {
			return fmt.Sprintf("unknown load balancing rule type %q", typ)
		}
		if _, ok := m["when"].(map[string]any); !ok {
			return "when is required on load balancing rules"
		}
		targets, _ := m["load_balance_targets"].([]any)
		if len(targets) == 0 {
			return fmt.Sprintf("load_balance_targets must not be empty on rule %s", id)
		}
		for _, raw := range targets {
			t, _ := raw.(map[string]any)
			target, _ := t["target"].(string)
			if !strings.Contains(target, "/") {
				return fmt.Sprintf("target %q must be a <provider-account>/<model> name", target)
			}
			_, hasWeight := t["weight"].(float64)
			_, hasPriority := t["priority"].(float64)
			switch typ {
			case "weight-based-routing":
				if !hasWeight {
					return fmt.Sprintf("weight is required on weight-based-routing target %s", target)
				}
			case "priority-based-routing":
				if !hasPriority {
					return fmt.Sprintf("priority is required on priority-based-routing target %s", target)
				}
			}
		}
	}
	return ""
}

// validateGatewayOtelConfig enforces the exporter shape derived from the
// TrueFoundry SDK's GatewayOtelConfig schemas plus the encoding vocabulary a
// deployed control plane enforces.
func validateGatewayOtelConfig(manifest map[string]any) string {
	if name, _ := manifest["name"].(string); name != "" {
		return "gateway-otel-config has no name field"
	}
	validEncodings := map[string]bool{"proto": true, "json": true}
	validate := func(key string, tracesOnly bool) string {
		raw, ok := manifest[key]
		if !ok {
			return ""
		}
		m, ok := raw.(map[string]any)
		if !ok {
			return key + " must be an object"
		}
		typ, _ := m["type"].(string)
		if typ != "http" && typ != "grpc" {
			return fmt.Sprintf("unknown exporter type %q", typ)
		}
		if endpoint, _ := m["endpoint"].(string); endpoint == "" {
			return "endpoint is required on exporters"
		}
		encoding, hasEncoding := m["encoding"].(string)
		switch typ {
		case "http":
			if !hasEncoding {
				return "encoding is required on http exporters"
			}
			if !validEncodings[encoding] {
				return fmt.Sprintf("Validation Error: GatewayConfig /%s/encoding: must be equal to one of the allowed values (got %q)", key, encoding)
			}
		case "grpc":
			if hasEncoding {
				return "encoding is not valid on grpc exporters"
			}
		}
		if tracesOnly {
			return ""
		}
		for _, field := range []string{"additional_resource_attributes", "exclude_request_data", "when"} {
			if _, ok := m[field]; ok {
				return fmt.Sprintf("%s is only valid on the traces exporter", field)
			}
		}
		return ""
	}
	if msg := validate("otel_traces_exporter_config", true); msg != "" {
		return msg
	}
	return validate("otel_metrics_exporter_config", false)
}

// validateGatewayGlobalSettings enforces the manifest shape derived from the
// TrueFoundry SDK's GlobalSettings schema (model_configuration is not in the
// published SDK; shape taken from a deployed control plane). All settings are
// optional — an empty manifest is valid.
func validateGatewayGlobalSettings(manifest map[string]any) string {
	if name, _ := manifest["name"].(string); name != "" {
		return "gateway-global-settings has no name field"
	}
	if raw, ok := manifest["retry_settings"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return "retry_settings must be an object"
		}
		if attempts, ok := m["attempts"].(float64); !ok || attempts < 1 {
			return "attempts is required on retry_settings and must be at least 1"
		}
		if codes, ok := m["on_status_codes"]; ok {
			list, ok := codes.([]any)
			if !ok || len(list) == 0 {
				return "on_status_codes must be a non-empty list"
			}
			for _, c := range list {
				if s, _ := c.(string); s == "" {
					return "on_status_codes entries must be non-empty strings"
				}
			}
		}
	}
	if raw, ok := manifest["logging_settings"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return "logging_settings must be an object"
		}
		typ, _ := m["type"].(string)
		switch typ {
		case "always", "never":
			if _, ok := m["log_body_by_default"]; ok {
				return "log_body_by_default is only valid with logging type header_controlled"
			}
		case "header_controlled":
			if _, ok := m["log_body_by_default"].(bool); !ok {
				return "log_body_by_default is required with logging type header_controlled"
			}
		default:
			return fmt.Sprintf("unknown logging settings type %q", typ)
		}
	}
	if raw, ok := manifest["semantic_cache_settings"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return "semantic_cache_settings must be an object"
		}
		if model, _ := m["semantic_cache_embedding_model"].(string); model == "" {
			return "semantic_cache_embedding_model is required on semantic_cache_settings"
		}
	}
	if raw, ok := manifest["guardrail_settings"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return "guardrail_settings must be an object"
		}
		_, hasTimeout := m["timeout_ms"]
		_, hasParallel := m["enable_parallel_execution"]
		if !hasTimeout && !hasParallel {
			return "guardrail_settings needs at least one of timeout_ms and enable_parallel_execution"
		}
		if hasTimeout {
			if timeout, ok := m["timeout_ms"].(float64); !ok || timeout < 1 {
				return "timeout_ms must be a number of at least 1"
			}
		}
		if hasParallel {
			if _, ok := m["enable_parallel_execution"].(bool); !ok {
				return "enable_parallel_execution must be a boolean"
			}
		}
	}
	if raw, ok := manifest["model_configuration"]; ok {
		m, ok := raw.(map[string]any)
		if !ok {
			return "model_configuration must be an object"
		}
		models, ok := m["allowed_models"].([]any)
		if !ok {
			return "allowed_models is required on model_configuration"
		}
		for _, v := range models {
			if s, _ := v.(string); s == "" {
				return "allowed_models entries must be non-empty strings"
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
	if typ, _ := req.Manifest["type"].(string); isBudgetV2Type(typ) {
		if _, ok := s.budgets[name]; !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": fmt.Sprintf("Resource with name %s not found", name)})
			return
		}
		delete(s.budgets, name)
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if typ, _ := req.Manifest["type"].(string); isMcpServerType(typ) {
		if _, ok := s.mcpservers[name]; !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": fmt.Sprintf("Resource with name %s not found", name)})
			return
		}
		delete(s.mcpservers, name)
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if typ, _ := req.Manifest["type"].(string); isRoleType(typ) {
		r, ok := s.roles[name]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": fmt.Sprintf("Resource with name %s not found", name)})
			return
		}
		if !r.editable {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Role %s is not editable", name)})
			return
		}
		delete(s.roles, name)
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if typ, _ := req.Manifest["type"].(string); isSecretGroupType(typ) {
		if _, ok := s.secretgroups[name]; !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": fmt.Sprintf("Resource with name %s not found", name)})
			return
		}
		delete(s.secretgroups, name)
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
			responseManifest := m
			if integrationKind == "notification-channel" {
				// The real control plane masks slack-webhook auth_data on
				// read (returns the literal string "[redacted]" instead of
				// the stored URL); reproduce that here so acceptance tests
				// exercise the same masking the provider's Read must handle.
				responseManifest = maskSlackWebhookURL(m)
			}
			items = append(items, map[string]any{
				"id":                 fmt.Sprintf("%s-int-%d", acc.id, i),
				"name":               modelName,
				"fqn":                fqn,
				"type":               integrationKind,
				"manifest":           responseManifest,
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

// maskSlackWebhookURL returns a copy of a notification-channel integration
// manifest with auth_data.webhook_url replaced by "[redacted]", mirroring
// the real control plane's masking of slack-webhook auth data on read.
func maskSlackWebhookURL(m map[string]any) map[string]any {
	auth, _ := m["auth_data"].(map[string]any)
	if auth == nil || auth["type"] != "slack-webhook" {
		return m
	}
	masked := make(map[string]any, len(m))
	maps.Copy(masked, m)
	maskedAuth := make(map[string]any, len(auth))
	maps.Copy(maskedAuth, auth)
	maskedAuth["webhook_url"] = "[redacted]"
	masked["auth_data"] = maskedAuth
	return masked
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
				if rp, present := auth["role_path"]; present {
					if s, ok := rp.(string); !ok || s == "" {
						return "auth_data role_path must be a non-empty string"
					}
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
