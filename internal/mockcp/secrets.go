package mockcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// secret is one key/value entry of a secret group.
type secret struct {
	id      string
	value   string
	version int
}

// HideSecretValues makes GET /api/svc/v1/secrets/{id} omit secret values, as
// a control plane with DISABLE_SECRET_VALUE_VIEW set does.
func (s *Server) HideSecretValues() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secretValuesHidden = true
}

// SecretValue returns the stored value and version of a secret.
func (s *Server) SecretValue(group, key string) (string, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.secretgroups[group]
	if !ok {
		return "", 0, false
	}
	sec, ok := g.secrets[key]
	if !ok {
		return "", 0, false
	}
	return sec.value, sec.version, true
}

// SetSecretValue changes a secret's value out-of-band (simulates drift).
func (s *Server) SetSecretValue(group, key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.secretgroups[group]
	if !ok {
		return
	}
	if sec, ok := g.secrets[key]; ok {
		sec.value = value
		sec.version++
	}
}

// AddSecret adds a secret out-of-band, as the UI would.
func (s *Server) AddSecret(group, key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.secretgroups[group]
	if !ok {
		return
	}
	if g.secrets == nil {
		g.secrets = map[string]*secret{}
	}
	g.secrets[key] = &secret{id: "secret-" + strconv.Itoa(s.nextID), value: value, version: 1}
	s.nextID++
}

// DeleteSecretByKey removes a secret out-of-band (simulates drift/manual
// deletion).
func (s *Server) DeleteSecretByKey(group, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.secretgroups[group]; ok {
		delete(g.secrets, key)
	}
}

// SecretCount returns the number of secrets across all secret groups.
func (s *Server) SecretCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, g := range s.secretgroups {
		n += len(g.secrets)
	}
	return n
}

// secretGroupEntry renders a secret group the way the secret group endpoints
// do: camelCase, with associatedSecrets but never their values. Callers must
// hold s.mu.
func secretGroupEntry(name string, g *secretGroup) map[string]any {
	keys := make([]string, 0, len(g.secrets))
	for k := range g.secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	associated := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		associated = append(associated, secretEntry(name, g, k))
	}
	return map[string]any{
		"id":                g.id,
		"fqn":               secretGroupFQN(name),
		"name":              name,
		"tenantName":        Tenant,
		"integrationId":     "int-" + name,
		"manifest":          g.manifest,
		"associatedSecrets": associated,
	}
}

// secretEntry renders a secret without its value. Callers must hold s.mu.
func secretEntry(group string, g *secretGroup, key string) map[string]any {
	return map[string]any{
		"id":            g.secrets[key].id,
		"fqn":           secretGroupFQN(group) + ":" + key,
		"name":          key,
		"secretGroupId": g.id,
	}
}

// secretGroupByID finds a secret group by its control plane ID. Callers must
// hold s.mu.
func (s *Server) secretGroupByID(id string) (string, *secretGroup, bool) {
	for name, g := range s.secretgroups {
		if g.id == id {
			return name, g, true
		}
	}
	return "", nil, false
}

// secretByID finds a secret by its control plane ID. Callers must hold s.mu.
func (s *Server) secretByID(id string) (string, *secretGroup, string, bool) {
	for name, g := range s.secretgroups {
		for key, sec := range g.secrets {
			if sec.id == id {
				return name, g, key, true
			}
		}
	}
	return "", nil, "", false
}

// handleGetSecretGroup implements GET /api/svc/v1/secret-groups/{id}.
func (s *Server) handleGetSecretGroup(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, g, ok := s.secretGroupByID(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Secret group not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": secretGroupEntry(name, g)})
}

// handleUpdateSecretGroup implements PUT /api/svc/v1/secret-groups/{id}: the
// request's secrets replace the group's. A key without a value keeps an
// existing secret unchanged, a new key needs a value, and omitted secrets are
// deleted. A secret gets a new version only when its value changes.
func (s *Server) handleUpdateSecretGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Secrets *[]struct {
			Key   string  `json:"key"`
			Value *string `json:"value"`
		} `json:"secrets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid JSON body"})
		return
	}
	if req.Secrets == nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "secrets is required"})
		return
	}
	if len(*req.Secrets) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "At least one secret is required in a secret group"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	name, g, ok := s.secretGroupByID(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Secret group not found"})
		return
	}

	next := map[string]*secret{}
	for _, in := range *req.Secrets {
		if strings.TrimSpace(in.Key) == "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "key is required"})
			return
		}
		if _, dup := next[in.Key]; dup {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Duplicate secret key %s", in.Key)})
			return
		}
		existing, ok := g.secrets[in.Key]
		switch {
		case ok && in.Value == nil:
			next[in.Key] = existing
		case ok:
			updated := *existing
			if *in.Value != existing.value {
				updated.value = *in.Value
				updated.version++
			}
			next[in.Key] = &updated
		case in.Value == nil:
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": fmt.Sprintf("Value of newly added secret %s is null", in.Key)})
			return
		default:
			next[in.Key] = &secret{id: "secret-" + strconv.Itoa(s.nextID), value: *in.Value, version: 1}
			s.nextID++
		}
	}
	g.secrets = next
	writeJSON(w, http.StatusOK, map[string]any{"data": secretGroupEntry(name, g)})
}

// handleGetSecret implements GET /api/svc/v1/secrets/{id}.
func (s *Server) handleGetSecret(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	group, g, key, ok := s.secretByID(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Secret not found"})
		return
	}
	entry := secretEntry(group, g, key)
	if s.secretValuesHidden {
		entry["value"] = nil
	} else {
		entry["value"] = g.secrets[key].value
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entry})
}

// handleDeleteSecret implements DELETE /api/svc/v1/secrets/{id}, which
// answers with the number of deleted secrets.
func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, g, key, ok := s.secretByID(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Secret not found"})
		return
	}
	delete(g.secrets, key)
	writeJSON(w, http.StatusOK, 1)
}
