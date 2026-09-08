package mockcp

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
)

// role is a stored custom role ("role" manifest). Roles the control plane
// provides itself are seeded rather than applied: reserved ones cannot be
// written at all, default ones can be updated in place but must survive a
// delete.
type role struct {
	id        string
	manifest  map[string]any
	editable  bool
	isDefault bool
	reserved  bool
	builtin   bool
}

// roleNameRe is the role name pattern, verified against a live control plane
// on 2026-09-08: "^[a-z][a-z0-9\\-]{1,33}[a-z0-9]$", i.e. 3-35 characters.
var roleNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,33}[a-z0-9]$`)

// rolePermissions is the permission vocabulary the real control plane
// accepts. Keep in sync with rolePermissionValues in
// internal/provider/role_resource.go.
var rolePermissions = map[string]bool{
	"account:CreateAccount": true, "account:DeleteAccount": true, "account:ManageAccount": true, "account:ReadAccount": true,
	"agent:CreateAgent": true, "agent:DeleteAgent": true, "agent:ManageAgent": true, "agent:ReadAgent": true,
	"agent-app:CreateAgentApp": true, "agent-app:DeleteAgentApp": true, "agent-app:ManageAgentApp": true, "agent-app:ReadAgentApp": true,
	"agent-identity:DeleteAgentIdentities": true, "agent-identity:ManageAgentIdentities": true, "agent-identity:ReadAgentIdentities": true,
	"application:ListApplications": true, "application:ManageApplications": true,
	"cluster:CreateCluster": true, "cluster:DeleteCluster": true, "cluster:ManageClusters": true, "cluster:ReadCluster": true,
	"environment:ListEnvironments": true, "environment:ManageEnvironments": true,
	"external-identity:ListExternalIdentities": true, "external-identity:ManageExternalIdentities": true,
	"gateway-controls:ListGatewayControls": true, "gateway-controls:ManageGatewayControls": true,
	"mcp-server:ApproveMcpServer": true, "mcp-server:CreateMcpServer": true, "mcp-server:DeleteMcpServer": true, "mcp-server:ManageMcpServer": true, "mcp-server:ReadMcpServer": true, "mcp-server:UseMcpServer": true,
	"policy:ListPolicies": true, "policy:ManagePolicies": true,
	"provider-account:CreateProviderAccount": true, "provider-account:DeleteProviderAccount": true, "provider-account:ManageProviderAccount": true, "provider-account:ReadProviderAccount": true, "provider-account:UseIntegrations": true,
	"provider-integration:UseIntegration": true,
	"repository:CreateRepository":         true, "repository:DeleteData": true, "repository:DeleteRepository": true, "repository:ManageRepository": true, "repository:ReadData": true, "repository:ReadRepository": true, "repository:WriteData": true,
	"role:ListRoles": true, "role:ManageRoles": true,
	"secret-group:CreateSecretGroup": true, "secret-group:DeleteSecretGroup": true, "secret-group:ManageSecretGroup": true, "secret-group:ReadData": true, "secret-group:ReadSecretGroup": true, "secret-group:WriteData": true,
	"settings:ListSettings": true, "settings:ManageSettings": true,
	"team:CreateTeam": true, "team:DeleteTeam": true, "team:ManageTeam": true, "team:ReadTeam": true,
	"tenant:AssignRole": true, "tenant:ReadAuditLogs": true,
	"tracing-project:CreateTracingProject": true, "tracing-project:DeleteTracingProject": true, "tracing-project:ManageTracingProject": true, "tracing-project:ReadData": true, "tracing-project:ReadTracingProject": true, "tracing-project:WriteData": true,
	"user:ListUsers": true, "user:ManageUsers": true,
	"virtual-account:CreateVirtualAccount": true, "virtual-account:DeleteVirtualAccount": true, "virtual-account:ManageVirtualAccount": true, "virtual-account:ReadVirtualAccount": true,
	"workspace:CreateWorkspace": true, "workspace:DeleteWorkspace": true, "workspace:ListWorkspaces": true, "workspace:ManageWorkspace": true, "workspace:ReadWorkspace": true,
}

// roleResourceTypes are the scopes the real control plane accepts for
// user-defined roles. "account" is a legacy alias it stores as "tenant";
// the provider rejects it, so the mock does too.
var roleResourceTypes = map[string]bool{"tenant": true, "team": true}

// isRoleType reports whether typ is the custom role manifest type.
func isRoleType(typ string) bool { return typ == "role" }

// seedBuiltinRoles adds the two kinds of control-plane-provided role, so both
// adoption guards are exercised without a test reaching into the store.
func (s *Server) seedBuiltinRoles() {
	s.roles["tenant-admin"] = &role{
		id:       "role-builtin-tenant-admin",
		builtin:  true,
		reserved: true,
		manifest: map[string]any{
			"type": "role", "name": "tenant-admin", "displayName": "Tenant Admin",
			"resourceType": "tenant", "permissions": []any{"tenant:AssignRole"},
		},
	}
	s.roles["member"] = &role{
		id:        "role-builtin-member",
		builtin:   true,
		editable:  true,
		isDefault: true,
		manifest: map[string]any{
			"type": "role", "name": "member", "displayName": "Member",
			"description":  "Role grants member access to the entities in the account.",
			"resourceType": "tenant",
			"permissions":  []any{"user:ListUsers", "team:ReadTeam"},
		},
	}
}

// RoleCount returns the number of stored roles that were created through
// apply, i.e. excluding the seeded built-ins.
func (s *Server) RoleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.roles {
		if !r.builtin {
			n++
		}
	}
	return n
}

// RoleExists reports whether a role with the given name is stored.
func (s *Server) RoleExists(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.roles[name]
	return ok
}

// RolePermissions returns the permissions currently stored for a role.
func (s *Server) RolePermissions(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.roles[name]
	if !ok {
		return nil
	}
	raw, _ := r.manifest["permissions"].([]any)
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		s, _ := p.(string)
		out = append(out, s)
	}
	return out
}

// DeleteRoleByName removes a role out-of-band (simulates drift).
func (s *Server) DeleteRoleByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.roles, name)
}

// applyRole stores a role keyed by name.
func (s *Server) applyRole(w http.ResponseWriter, typ, name string, manifest map[string]any, dryRun bool) {
	if !roleNameRe.MatchString(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Validation Error: RoleManifest /name: must match pattern %q", roleNameRe.String())})
		return
	}
	if msg, status := validateRoleManifest(manifest); msg != "" {
		writeJSON(w, status, map[string]string{"message": msg})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	action := "CREATE"
	var existing map[string]any
	r, ok := s.roles[name]
	if ok {
		if !r.editable {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Role %s is not editable", name)})
			return
		}
		action = "UPDATE"
		existing = r.manifest
	} else {
		r = &role{id: "role-" + strconv.Itoa(s.nextID), editable: true}
		s.nextID++
	}
	if !dryRun {
		r.manifest = manifest
		s.roles[name] = r
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"existingManifest": existing,
		"action":           action,
		"data": map[string]any{
			"id":   r.id,
			"name": name,
			"type": typ,
		},
	})
}

// handleListRoles implements GET /api/svc/v1/role. The real endpoint is
// singular, returns a bare array with no envelope or pagination, and ignores
// every query parameter — a ?name= filter included.
func (s *Server) handleListRoles(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []map[string]any{}
	for name, r := range s.roles {
		resourceType, _ := r.manifest["resourceType"].(string)
		items = append(items, map[string]any{
			"id":           r.id,
			"name":         name,
			"resourceType": resourceType,
			"manifest":     r.manifest,
			"isEditable":   r.editable,
			"isDefault":    r.isDefault,
			"isReserved":   r.reserved,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
	writeJSON(w, http.StatusOK, items)
}

// validateRoleManifest enforces the role manifest rules observed on a real
// control plane. It returns the message and the status to answer with: the
// control plane answers 422 for a missing property and 400 for a
// semantically invalid one.
func validateRoleManifest(manifest map[string]any) (string, int) {
	if display, _ := manifest["displayName"].(string); display == "" {
		return "RoleManifest : must have required property 'displayName'", http.StatusUnprocessableEntity
	}
	resourceType, _ := manifest["resourceType"].(string)
	if resourceType == "" {
		return "RoleManifest : must have required property 'resourceType'", http.StatusUnprocessableEntity
	}
	if !roleResourceTypes[resourceType] {
		return fmt.Sprintf("Resource type %s is not supported", resourceType), http.StatusBadRequest
	}
	permissions, _ := manifest["permissions"].([]any)
	if len(permissions) == 0 {
		return "Role must have at least one permission", http.StatusBadRequest
	}
	for _, raw := range permissions {
		p, _ := raw.(string)
		if !rolePermissions[p] {
			return fmt.Sprintf("Invalid permission: %s", p), http.StatusBadRequest
		}
	}
	return "", http.StatusOK
}
