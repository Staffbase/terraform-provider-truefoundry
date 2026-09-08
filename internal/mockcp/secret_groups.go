package mockcp

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// secretGroup is a stored secret group ("secret-group" manifest). The
// secrets it holds are created out of band and are not modelled.
type secretGroup struct {
	id       string
	manifest map[string]any
}

// secretGroupNameRe is the secret group name pattern, verified against a live
// control plane on 2026-09-08: "^(?!.*--)[a-z][a-z0-9-]{3,48}[a-z0-9]$"; RE2
// has no lookahead, so the "no consecutive hyphens" rule is instead expressed
// by only ever allowing a hyphen directly before an alphanumeric (an
// optional-hyphen group repeated, not an alternation); the length bound
// (5-50) is checked separately.
var secretGroupNameRe = regexp.MustCompile(`^[a-z](-?[a-z0-9])*$`)

// isSecretGroupType reports whether typ is the secret group manifest type.
func isSecretGroupType(typ string) bool { return typ == "secret-group" }

// SecretGroupCount returns the number of stored secret groups.
func (s *Server) SecretGroupCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.secretgroups)
}

// DeleteSecretGroupByName removes a secret group out-of-band (simulates
// drift/manual deletion).
func (s *Server) DeleteSecretGroupByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.secretgroups, name)
}

// applySecretGroup stores a secret group keyed by name.
func (s *Server) applySecretGroup(w http.ResponseWriter, typ, name string, manifest map[string]any, dryRun bool) {
	if !secretGroupNameRe.MatchString(name) || len(name) < 5 || len(name) > 50 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Validation Error: SecretGroupManifest /name: must match pattern %q", secretGroupNameRe.String())})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if msg, status := s.validateSecretGroupManifest(manifest); msg != "" {
		writeJSON(w, status, map[string]string{"message": msg})
		return
	}

	action := "CREATE"
	var existing map[string]any
	g, ok := s.secretgroups[name]
	if ok {
		action = "UPDATE"
		existing = g.manifest
	} else {
		g = &secretGroup{id: "sg-" + strconv.Itoa(s.nextID)}
		s.nextID++
	}
	if !dryRun {
		g.manifest = manifest
		s.secretgroups[name] = g
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"existingManifest": existing,
		"action":           action,
		"data": map[string]any{
			"id":   g.id,
			"name": name,
			"fqn":  secretGroupFQN(name),
			"type": typ,
		},
	})
}

// handleListSecretGroups implements GET /api/svc/v1/secret-groups. Unlike the
// roles endpoint, this one is paginated with a camelCase {data, pagination}
// envelope and honors a server-side ?fqn= filter.
func (s *Server) handleListSecretGroups(w http.ResponseWriter, r *http.Request) {
	fqnFilter := r.URL.Query().Get("fqn")
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 100
	}

	s.mu.Lock()
	items := []map[string]any{}
	for name, g := range s.secretgroups {
		fqn := secretGroupFQN(name)
		if fqnFilter != "" && fqn != fqnFilter {
			continue
		}
		items = append(items, map[string]any{
			"id":            g.id,
			"fqn":           fqn,
			"name":          name,
			"tenantName":    Tenant,
			"integrationId": "int-" + name,
			"manifest":      g.manifest,
			// The real endpoint also returns associatedSecrets here; the
			// client deliberately does not model it.
			"associatedSecrets": []any{},
		})
	}
	s.mu.Unlock()

	sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
	total := len(items)
	if offset > total {
		offset = total
	}
	end := min(offset+limit, total)
	writeJSON(w, http.StatusOK, map[string]any{
		"data":       items[offset:end],
		"pagination": map[string]int{"total": total, "offset": offset, "limit": limit},
	})
}

// validateSecretGroupManifest enforces the secret group manifest rules. The
// integration must exist and must be a secret store, which the real control
// plane checks too. Callers must hold s.mu.
func (s *Server) validateSecretGroupManifest(manifest map[string]any) (string, int) {
	fqn, _ := manifest["integration_fqn"].(string)
	if fqn == "" {
		return "SecretGroupManifest : must have required property 'integration_fqn'", http.StatusUnprocessableEntity
	}
	if _, ok := manifest["collaborators"].([]any); !ok {
		return "SecretGroupManifest : must have required property 'collaborators'", http.StatusUnprocessableEntity
	}
	if !s.secretStoreIntegrationExists(fqn) {
		return fmt.Sprintf("Integration with fqn %s not found", fqn), http.StatusNotFound
	}
	return "", http.StatusOK
}

// secretStoreIntegrationExists reports whether fqn names a secret-store
// integration of a stored provider account, building FQNs the same way
// handleListIntegrations does. Callers must hold s.mu.
func (s *Server) secretStoreIntegrationExists(fqn string) bool {
	for name, acc := range s.accounts {
		typ, _ := acc.manifest["type"].(string)
		if integrationKindFor(typ) != "secret-store" {
			continue
		}
		integrations, _ := acc.manifest["integrations"].([]any)
		for _, raw := range integrations {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			storeName, _ := m["name"].(string)
			if accountFQN(typ, name)+":secret-store:"+storeName == fqn {
				return true
			}
		}
	}
	return false
}

// secretGroupFQN builds a secret group FQN, which is tenant-scoped and —
// unlike an integration FQN — carries no provider or kind segment.
func secretGroupFQN(name string) string {
	return strings.Join([]string{Tenant, name}, ":")
}
