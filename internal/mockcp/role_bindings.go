package mockcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
)

// roleBinding is a stored "role-binding" manifest.
type roleBinding struct {
	id       string
	manifest map[string]any
}

// roleBindingNameRe is the role binding name pattern from the published API
// reference (2026-10-08): 3-66 characters.
var roleBindingNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,64}[a-z0-9]$`)

// roleBindingSubjectTypes are the principal kinds a binding accepts.
var roleBindingSubjectTypes = map[string]bool{"user": true, "team": true, "virtualaccount": true, "agent": true}

// RoleBindingCount returns the number of stored role bindings.
func (s *Server) RoleBindingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.roleBindings)
}

// DeleteRoleBindingByName removes a role binding out-of-band (simulates
// drift/manual deletion).
func (s *Server) DeleteRoleBindingByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.roleBindings, name)
}

// handleApplyRoleBinding implements PUT /api/svc/v1/role-bindings: create or
// update a role binding, matched by name.
func (s *Server) handleApplyRoleBinding(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Manifest map[string]any `json:"manifest"`
		DryRun   bool           `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Manifest == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid request body"})
		return
	}
	if msg := validateRoleBindingManifest(req.Manifest); msg != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"statusCode": http.StatusUnprocessableEntity, "message": msg})
		return
	}
	name, _ := req.Manifest["name"].(string)

	s.mu.Lock()
	defer s.mu.Unlock()
	rb, exists := s.roleBindings[name]
	if !exists {
		rb = &roleBinding{id: "rb-" + strconv.Itoa(s.nextID)}
		s.nextID++
	}
	if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{"data": roleBindingJSON(name, &roleBinding{id: rb.id, manifest: req.Manifest})})
		return
	}
	rb.manifest = req.Manifest
	s.roleBindings[name] = rb
	writeJSON(w, http.StatusOK, map[string]any{"data": roleBindingJSON(name, rb)})
}

// handleListRoleBindings implements GET /api/svc/v1/role-bindings with its
// documented exact-match id and name filters.
func (s *Server) handleListRoleBindings(w http.ResponseWriter, r *http.Request) {
	idFilter := r.URL.Query().Get("id")
	nameFilter := r.URL.Query().Get("name")

	s.mu.Lock()
	items := []map[string]any{}
	for name, rb := range s.roleBindings {
		if (idFilter != "" && rb.id != idFilter) || (nameFilter != "" && name != nameFilter) {
			continue
		}
		items = append(items, roleBindingJSON(name, rb))
	}
	s.mu.Unlock()

	sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
	writePage(w, r, items)
}

// handleGetRoleBinding implements GET /api/svc/v1/role-bindings/{id}.
func (s *Server) handleGetRoleBinding(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, rb := range s.roleBindings {
		if rb.id == r.PathValue("id") {
			writeJSON(w, http.StatusOK, map[string]any{"data": roleBindingJSON(name, rb)})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"statusCode": http.StatusNotFound, "message": "Role binding not found"})
}

// handleDeleteRoleBinding implements DELETE /api/svc/v1/role-bindings/{id}.
func (s *Server) handleDeleteRoleBinding(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, rb := range s.roleBindings {
		if rb.id == r.PathValue("id") {
			delete(s.roleBindings, name)
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"statusCode": http.StatusNotFound, "message": "Role binding not found"})
}

// validateRoleBindingManifest enforces the role binding manifest schema of the
// published API reference, plus the documented rule that a binding has either
// one subject or one permission.
func validateRoleBindingManifest(manifest map[string]any) string {
	if typ, _ := manifest["type"].(string); typ != "role-binding" {
		return fmt.Sprintf("Validation Error: RoleBindingManifest /type: must be equal to constant (got %q)", typ)
	}
	name, _ := manifest["name"].(string)
	if !roleBindingNameRe.MatchString(name) {
		return fmt.Sprintf("Validation Error: RoleBindingManifest /name: must match pattern %q", roleBindingNameRe.String())
	}
	subjects, _ := manifest["subjects"].([]any)
	if len(subjects) == 0 {
		return "Validation Error: RoleBindingManifest : a role binding needs at least one subject"
	}
	for _, raw := range subjects {
		subject, _ := raw.(map[string]any)
		typ, _ := subject["type"].(string)
		if !roleBindingSubjectTypes[typ] {
			return fmt.Sprintf("Validation Error: RoleBindingSubject /type: must be equal to one of the allowed values (got %q)", typ)
		}
		if n, _ := subject["name"].(string); n == "" {
			return "Validation Error: RoleBindingSubject : must have required property 'name'"
		}
	}
	permissions, _ := manifest["permissions"].([]any)
	if len(permissions) == 0 {
		return "Validation Error: RoleBindingManifest : a role binding needs at least one permission"
	}
	for _, raw := range permissions {
		permission, _ := raw.(map[string]any)
		for _, key := range []string{"resourceType", "resourceFqn", "role"} {
			if v, _ := permission[key].(string); v == "" {
				return fmt.Sprintf("Validation Error: RoleBindingPermission : must have required property '%s'", key)
			}
		}
	}
	if len(subjects) > 1 && len(permissions) > 1 {
		return "A role binding can have either one subject with many permissions or many subjects with one permission, not both"
	}
	return ""
}

// roleBindingJSON renders a RoleBinding response object.
func roleBindingJSON(name string, rb *roleBinding) map[string]any {
	return map[string]any{
		"id":        rb.id,
		"name":      name,
		"accountId": "acc-" + Tenant,
		"manifest":  rb.manifest,
		"createdBySubject": map[string]any{
			"subjectId": "mock-subject", "subjectType": "user", "subjectSlug": "mock-user", "subjectDisplayName": "Mock User",
		},
		"createdAt": "2026-10-08T00:00:00Z",
		"updatedAt": "2026-10-08T00:00:00Z",
	}
}
