package mockcp

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
)

// gatewayInstallation is a stored gateway installation
// ("gateway-installation" manifest) with the tokens minted for it.
type gatewayInstallation struct {
	id       string
	manifest map[string]any
	tokens   int
}

// Name and URL patterns of the gateway installation manifest, read off a
// self-hosted control plane on 2026-10-12.
var (
	gatewayInstallationNameRe = regexp.MustCompile(`^[a-z][a-z0-9\-]{1,33}[a-z0-9]$`)
	gatewayInstallationURLRe  = regexp.MustCompile(`^https?://[^\s]+$`)
)

func isGatewayInstallationType(typ string) bool { return typ == "gateway-installation" }

// GatewayInstallationCount returns the number of stored gateway installations.
func (s *Server) GatewayInstallationCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.installations)
}

// GatewayInstallationTokenCount returns how many tokens were minted for the
// named installation.
func (s *Server) GatewayInstallationTokenCount(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.installations[name]; ok {
		return g.tokens
	}
	return 0
}

// SetGatewayInstallationURL changes an installation's URL out-of-band
// (simulates drift).
func (s *Server) SetGatewayInstallationURL(name, url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.installations[name]; ok {
		g.manifest["url"] = url
	}
}

// DeleteGatewayInstallationByName removes an installation out-of-band.
func (s *Server) DeleteGatewayInstallationByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.installations, name)
}

// applyGatewayInstallation stores a gateway installation keyed by name. Like
// the real control plane, it answers with the stored entry under data and no
// action field.
func (s *Server) applyGatewayInstallation(w http.ResponseWriter, name string, manifest map[string]any, dryRun bool) {
	invalid := func(msg string) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"statusCode": http.StatusUnprocessableEntity,
			"message":    "Manifest validation failed",
			"details":    []map[string]string{{"message": "GatewayInstallationManifest " + msg}},
		})
	}
	if _, ok := manifest["isDefault"].(bool); !ok {
		invalid(": must have required property 'isDefault'")
		return
	}
	if !gatewayInstallationNameRe.MatchString(name) {
		invalid(fmt.Sprintf("/name: must match pattern %q", gatewayInstallationNameRe.String()))
		return
	}
	url, _ := manifest["url"].(string)
	if !gatewayInstallationURLRe.MatchString(url) {
		invalid(fmt.Sprintf("/url: must match pattern %q", gatewayInstallationURLRe.String()))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for other, g := range s.installations {
		if other != name && g.manifest["url"] == url {
			writeJSON(w, http.StatusConflict, map[string]any{
				"statusCode": http.StatusConflict,
				"message":    fmt.Sprintf("URL %s is already used by another gateway installation (%s)", url, other),
			})
			return
		}
	}

	g, ok := s.installations[name]
	if !ok {
		// A dry run previews the next ID without consuming it.
		g = &gatewayInstallation{id: "gwi-" + strconv.Itoa(s.nextID)}
		if !dryRun {
			s.nextID++
		}
	}
	entry := gatewayInstallationEntry(name, &gatewayInstallation{id: g.id, manifest: manifest})
	if !dryRun {
		g.manifest = manifest
		s.installations[name] = g
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entry})
}

// gatewayInstallationEntry renders an installation the way the
// installations endpoints do. Callers must hold s.mu.
func gatewayInstallationEntry(name string, g *gatewayInstallation) map[string]any {
	isDefault, _ := g.manifest["isDefault"].(bool)
	return map[string]any{
		"id":         g.id,
		"name":       name,
		"tenantName": Tenant,
		"isDefault":  isDefault,
		"manifest":   g.manifest,
		"metadata":   map[string]any{},
	}
}

func (s *Server) gatewayInstallationByID(id string) (string, *gatewayInstallation, bool) {
	for name, g := range s.installations {
		if g.id == id {
			return name, g, true
		}
	}
	return "", nil, false
}

// handleListGatewayInstallations implements GET
// /api/svc/v1/llm-gateway/installations: a {data} envelope without
// pagination that ignores query parameters.
func (s *Server) handleListGatewayInstallations(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	names := make([]string, 0, len(s.installations))
	for name := range s.installations {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]map[string]any, 0, len(names))
	for _, name := range names {
		items = append(items, gatewayInstallationEntry(name, s.installations[name]))
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) handleGetGatewayInstallation(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, g, ok := s.gatewayInstallationByID(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"statusCode": http.StatusNotFound, "message": fmt.Sprintf("Gateway installation with id %s not found", r.PathValue("id"))})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": gatewayInstallationEntry(name, g)})
}

func (s *Server) handleDeleteGatewayInstallation(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, _, ok := s.gatewayInstallationByID(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"statusCode": http.StatusNotFound, "message": fmt.Sprintf("Gateway installation with id %s not found", r.PathValue("id"))})
		return
	}
	delete(s.installations, name)
	writeJSON(w, http.StatusOK, map[string]any{})
}

// handleGatewayInstallationToken implements GET
// /api/svc/v1/llm-gateway/installations/token?name=, minting a new token on
// every call.
func (s *Server) handleGatewayInstallationToken(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.installations[name]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"statusCode": http.StatusNotFound, "message": fmt.Sprintf("Gateway installation with name %s not found", name)})
		return
	}
	g.tokens++
	writeJSON(w, http.StatusOK, map[string]string{"token": fmt.Sprintf("gwtoken-%s-%d", name, g.tokens)})
}
