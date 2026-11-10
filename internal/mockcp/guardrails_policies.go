// Copyright 2026, Staffbase SE and contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
//
// You may obtain a copy of the License at
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mockcp

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
)

var guardrailsPolicyNameRE = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// validateGuardrailsPolicy adds the named-policy requirements to the shared
// folded rule validation; keep these hook rules in sync with ValidateConfig.
func validateGuardrailsPolicy(manifest map[string]any) string {
	name, _ := manifest["name"].(string)
	if !guardrailsPolicyNameRE.MatchString(name) {
		return "name must contain only letters, digits, and hyphens"
	}
	// Copy instead of adding a synthetic id to the stored named manifest.
	rule := map[string]any{}
	for k, v := range manifest {
		rule[k] = v
	}
	rule["id"] = name
	if msg := validateGatewayGuardrailsConfig(map[string]any{"rules": []any{rule}}); msg != "" {
		return msg
	}
	when, _ := manifest["when"].(map[string]any)
	target, ok := when["target"].(map[string]any)
	if !ok {
		return "target is required on guardrails policies"
	}
	conditions, _ := target["conditions"].(map[string]any)
	listLen := func(key string) int { v, _ := manifest[key].([]any); return len(v) }
	if conditions["model"] != nil && listLen("llm_input_guardrails")+listLen("llm_output_guardrails") == 0 {
		return "Model targets require at least one LLM input or output guardrail"
	}
	if (conditions["mcpServers"] != nil || conditions["mcpTools"] != nil) && listLen("mcp_tool_pre_invoke_guardrails")+listLen("mcp_tool_post_invoke_guardrails") == 0 {
		return "MCP targets require at least one pre- or post-invoke guardrail"
	}
	return ""
}

func (s *Server) applyGuardrailsPolicy(w http.ResponseWriter, name string, manifest map[string]any, dryRun bool) {
	if msg := validateGuardrailsPolicy(manifest); msg != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": msg})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Real apply checks integrations, not merely the existence of the group.
	refs := map[string]bool{}
	for group, acc := range s.accounts {
		if acc.manifest["type"] != "provider-account/guardrail-config-group" {
			continue
		}
		integrations, _ := acc.manifest["integrations"].([]any)
		for _, raw := range integrations {
			integration, _ := raw.(map[string]any)
			integrationName, _ := integration["name"].(string)
			refs[group+"/"+integrationName] = true
		}
	}
	for _, key := range []string{"llm_input_guardrails", "llm_output_guardrails", "mcp_tool_pre_invoke_guardrails", "mcp_tool_post_invoke_guardrails"} {
		values, _ := manifest[key].([]any)
		for _, raw := range values {
			ref, ok := raw.(string)
			if !ok || !refs[ref] {
				writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Guardrail Integration %v referenced in rule %s does not exist.", raw, name)})
				return
			}
		}
	}
	action := "CREATE"
	cfg, exists := s.guardrailsPolicies[name]
	if exists {
		action = "UPDATE"
	} else {
		cfg = &gatewayConfig{id: "gw-policy-" + strconv.Itoa(s.nextID)}
	}
	preview := &gatewayConfig{id: cfg.id, manifest: manifest}
	if !dryRun {
		s.guardrailsPolicies[name] = preview
		if !exists {
			s.nextID++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": action, "data": guardrailsPolicyJSON(preview)})
}

func guardrailsPolicyJSON(cfg *gatewayConfig) map[string]any {
	return map[string]any{"id": cfg.id, "tenantName": Tenant, "type": "gateway-guardrails-config", "manifest": cfg.manifest,
		"createdBySubject": map[string]any{"id": "mock-user"}, "createdAt": "2026-11-02T00:00:00Z", "updatedAt": "2026-11-02T00:00:00Z"}
}

func (s *Server) handleListGuardrailsPolicies(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.guardrailsPolicies))
	for name := range s.guardrailsPolicies {
		names = append(names, name)
	}
	sort.Strings(names)
	items := []any{}
	for _, name := range names {
		items = append(items, guardrailsPolicyJSON(s.guardrailsPolicies[name]))
	}
	writeJSON(w, http.StatusOK, items)
}

func guardrailsPolicyNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{"statusCode": 404, "message": "Gateway configuration instance not found"})
}

func (s *Server) handleGetGuardrailsPolicy(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.guardrailsPolicies[r.PathValue("name")]
	if cfg == nil {
		guardrailsPolicyNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, guardrailsPolicyJSON(cfg))
}

func (s *Server) handleDeleteGuardrailsPolicy(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := r.PathValue("name")
	if s.guardrailsPolicies[name] == nil {
		guardrailsPolicyNotFound(w)
		return
	}
	delete(s.guardrailsPolicies, name)
	writeJSON(w, http.StatusOK, map[string]any{})
}

// GuardrailsPolicyCount returns the number of named instances (not singletons).
func (s *Server) GuardrailsPolicyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.guardrailsPolicies)
}
