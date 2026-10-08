package mockcp

import (
	"fmt"
	"net/http"
)

// Legacy grants are the access lists embedded in resource manifests:
// `collaborators` on provider accounts, MCP servers, agents, ML repos and
// secret groups, and `permissions` on virtual accounts. Control plane v0.172.0
// moved access into standalone role-binding manifests and keeps accepting the
// legacy fields until 15 Nov 2026, with these semantics:
//
//   - field omitted: the existing grants are left untouched
//   - non-empty list: the grants are replaced
//   - collaborators: []: the grants are cleared
//   - permissions: [] on a virtual account: rejected
//
// After the cut-off any manifest carrying the fields is rejected; tests turn
// that behavior on with RetireLegacyGrants.

// RetireLegacyGrants makes every apply reject manifests that still carry
// collaborators or virtual account permissions, the way the control plane
// behaves after 15 Nov 2026.
func (s *Server) RetireLegacyGrants() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.legacyGrantsRetired = true
}

// rejectLegacyGrant returns a validation message when the manifest carries the
// legacy grant field key and legacy grants are retired. Callers must hold s.mu.
func (s *Server) rejectLegacyGrant(manifest map[string]any, key string) string {
	if _, ok := manifest[key]; !ok || !s.legacyGrantsRetired {
		return ""
	}
	return fmt.Sprintf("Validation Error: %s is no longer supported, grant access with a role-binding manifest instead", key)
}

// carryOverLegacyGrant keeps the stored grants when an apply omits the legacy
// grant field key, since an omitted field leaves existing grants untouched.
func carryOverLegacyGrant(existing, manifest map[string]any, key string) {
	if _, ok := manifest[key]; ok || existing == nil {
		return
	}
	if grants, ok := existing[key]; ok {
		manifest[key] = grants
	}
}

// writeLegacyGrantError writes the retirement rejection, if any, and reports
// whether it did. Callers must hold s.mu.
func (s *Server) writeLegacyGrantError(w http.ResponseWriter, manifest map[string]any, key string) bool {
	if msg := s.rejectLegacyGrant(manifest, key); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": msg})
		return true
	}
	return false
}

// LegacyGrants returns the stored legacy grants of a resource and whether the
// field is present at all. kind is one of "provider-account", "mcp-server",
// "secret-group", "ml-repo", "agent", or "virtual-account" (whose grants are
// its permissions); name is the manifest name.
func (s *Server) LegacyGrants(kind, name string) ([]any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var manifest map[string]any
	key := "collaborators"
	switch kind {
	case "provider-account":
		if a, ok := s.accounts[name]; ok {
			manifest = a.manifest
		}
	case "mcp-server":
		if m, ok := s.mcpservers[name]; ok {
			manifest = m.manifest
		}
	case "secret-group":
		if g, ok := s.secretgroups[name]; ok {
			manifest = g.manifest
		}
	case "ml-repo":
		if r, ok := s.mlrepos[name]; ok {
			manifest = r.manifest
		}
	case "agent":
		if a, ok := s.agents[name]; ok {
			manifest = a.manifest
		}
	case "virtual-account":
		key = "permissions"
		if va, ok := s.vaccounts[name]; ok {
			manifest = va.manifest
		}
	}
	grants, ok := manifest[key].([]any)
	return grants, ok
}
