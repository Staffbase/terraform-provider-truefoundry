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

// ML repos and the versioned artifacts they contain (agent skills and
// prompts). Request/response shapes and validation behavior follow the
// truefoundry-typescript-sdk definitions and probes against a real control
// plane (2026-07-27): these endpoints use snake_case envelopes (unlike the
// camelCase agents endpoints), the apply endpoints ignore any dryRun flag
// and always persist, and applied agent-skill sources are rewritten to a
// managed blob-storage source.
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
)

// decodeManifestRequest decodes a {"manifest": {...}} request body. A dryRun
// flag is deliberately not decoded: the real ml-repo and artifact-version
// endpoints ignore it and always persist.
func decodeManifestRequest(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var req struct {
		Manifest map[string]any `json:"manifest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Manifest == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid request body"})
		return nil, false
	}
	return req.Manifest, true
}

// writePage renders a paginated {data, pagination} listing envelope.
func writePage(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 100
	}
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

type mlRepo struct {
	id       string
	manifest map[string]any
}

// artifactSeries is a versioned ML repo artifact — an agent skill or a
// prompt. Each apply bumps latestVersion and replaces the manifest.
type artifactSeries struct {
	id            string
	latestVersion int
	manifest      map[string]any
}

// MlRepoCount returns the number of stored ML repos.
func (s *Server) MlRepoCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.mlrepos)
}

// DeleteMlRepoByName removes an ML repo out-of-band (simulates drift/manual deletion).
func (s *Server) DeleteMlRepoByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mlrepos, name)
}

// AgentSkillCount returns the number of stored agent skills.
func (s *Server) AgentSkillCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.skills)
}

// DeleteAgentSkillByName removes an agent skill out-of-band (simulates drift/manual deletion).
func (s *Server) DeleteAgentSkillByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.skills, name)
}

// PromptCount returns the number of stored prompts.
func (s *Server) PromptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.prompts)
}

// DeletePromptByName removes a prompt out-of-band (simulates drift/manual deletion).
func (s *Server) DeletePromptByName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.prompts, name)
}

// mlRepoNameRe is the ML repo name pattern of the real control plane,
// "^[a-zA-Z][a-zA-Z0-9\\-]{1,98}[a-zA-Z0-9]$" (3-100 characters). ML repo
// names accept upper case, like tenant/team budget config names do.
var mlRepoNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{1,98}[a-zA-Z0-9]$`)

// artifactNameRe mirrors the provider's own agent-skill/prompt name
// validator (internal/provider/names.go's artifactNameRegexp), not a pattern
// read off the real control plane: those two families' apply endpoints
// ignore dryRun and persist, so they must never be probed with a valid
// manifest (see AGENTS.md "Manifest name rules"). Kept deliberately
// permissive, like the provider-side validator it mirrors.
var artifactNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// handleApplyMlRepo implements PUT /api/svc/v1/ml-repos: create an ML repo
// or update an existing one in place (same name keeps the same ID).
func (s *Server) handleApplyMlRepo(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeManifestRequest(w, r)
	if !ok {
		return
	}
	name, _ := req["name"].(string)
	if typ, _ := req["type"].(string); typ != "ml-repo" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Validation Error: MlRepoManifest : unsupported manifest type %q", typ)})
		return
	}
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Validation Error: MlRepoManifest : must have required property 'name'"})
		return
	}
	if !mlRepoNameRe.MatchString(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Validation Error: MlRepoManifest /name: must match pattern %q", mlRepoNameRe.String())})
		return
	}
	storageFQN, _ := req["storage_integration_fqn"].(string)
	if storageFQN == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Validation Error: MlRepoManifest : must have required property 'storage_integration_fqn'"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeLegacyGrantError(w, req, "collaborators") {
		return
	}
	if !s.blobStorageIntegrationExists(storageFQN) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": fmt.Sprintf("Blob storage integration %q not found", storageFQN)})
		return
	}
	repo, exists := s.mlrepos[name]
	if !exists {
		repo = &mlRepo{id: strconv.Itoa(s.nextID)}
		s.nextID++
		s.mlrepos[name] = repo
	}
	carryOverLegacyGrant(repo.manifest, req, "collaborators")
	repo.manifest = req
	writeJSON(w, http.StatusOK, map[string]any{"data": mlRepoJSON(name, repo)})
}

// blobStorageIntegrationExists reports whether any stored account exposes a
// blob-storage integration with the given FQN. Callers must hold s.mu.
func (s *Server) blobStorageIntegrationExists(fqn string) bool {
	for name, acc := range s.accounts {
		typ, _ := acc.manifest["type"].(string)
		if integrationKindFor(typ) != "blob-storage" {
			continue
		}
		for _, raw := range listOf(acc.manifest, "integrations") {
			m, _ := raw.(map[string]any)
			integrationName, _ := m["name"].(string)
			if accountFQN(typ, name)+":blob-storage:"+integrationName == fqn {
				return true
			}
		}
	}
	return false
}

// mlRepoJSON renders an MlRepo response object.
func mlRepoJSON(name string, repo *mlRepo) map[string]any {
	return map[string]any{
		"id":          repo.id,
		"name":        name,
		"tenant_name": Tenant,
		"manifest":    repo.manifest,
		"created_by_subject": map[string]any{
			"subjectId": "mock-subject", "subjectType": "user", "subjectSlug": "mock-user", "subjectDisplayName": "Mock User",
		},
		"created_at": "2026-07-15T00:00:00Z",
		"account_id": "acc-" + Tenant,
	}
}

// handleListMlRepos implements GET /api/svc/v1/ml-repos.
func (s *Server) handleListMlRepos(w http.ResponseWriter, r *http.Request) {
	nameFilter := r.URL.Query().Get("name")

	s.mu.Lock()
	var items []map[string]any
	for name, repo := range s.mlrepos {
		if nameFilter != "" && name != nameFilter {
			continue
		}
		items = append(items, mlRepoJSON(name, repo))
	}
	s.mu.Unlock()

	sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
	writePage(w, r, items)
}

// handleDeleteMlRepo implements DELETE /api/svc/v1/ml-repos/{id}.
func (s *Server) handleDeleteMlRepo(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, repo := range s.mlrepos {
		if repo.id == r.PathValue("id") {
			delete(s.mlrepos, name)
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "ML Repo not found"})
}

// handleApplyAgentSkill implements PUT /api/svc/v1/agent-skill-versions:
// create an agent skill or a new version of an existing one. Verified
// against a real control plane (2026-07-27): inline sources must start with
// YAML frontmatter whose name matches the manifest name, and the stored
// source is rewritten to a managed blob-storage source whose description is
// the frontmatter description. A dryRun flag is ignored — the apply always
// persists.
func (s *Server) handleApplyAgentSkill(w http.ResponseWriter, r *http.Request) {
	manifest, ok := decodeManifestRequest(w, r)
	if !ok {
		return
	}
	if typ, _ := manifest["type"].(string); typ != "agent-skill" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Validation Error: AgentSkillManifest : unsupported manifest type %q", typ)})
		return
	}
	name, mlRepoName, ok := requireArtifactNames(w, manifest)
	if !ok {
		return
	}

	source, _ := manifest["source"].(map[string]any)
	sourceType, _ := source["type"].(string)
	var storedSource map[string]any
	switch sourceType {
	case "inline":
		skillMd, _ := source["skill_md"].(string)
		fmName, fmDescription, ok := skillFrontmatter(skillMd)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "SKILL.md must start with YAML frontmatter (a line `---`, then fields, then a closing `---`)."})
			return
		}
		if fmName != name {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("SKILL.md frontmatter name '%s' must match manifest name '%s'.", fmName, name)})
			return
		}
		storedSource = map[string]any{"type": "blob-storage", "description": fmDescription, "uri": managedStorageURI(mlRepoName, name)}
	case "blob-storage":
		uri, _ := source["uri"].(string)
		description, _ := source["description"].(string)
		if uri == "" || description == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "`source` of type blob-storage requires uri and description"})
			return
		}
		storedSource = map[string]any{"type": "blob-storage", "description": description, "uri": managedStorageURI(mlRepoName, name)}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "`source` must be an inline or blob storage source for agent skill of type agent-skill"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.mlrepos[mlRepoName]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": fmt.Sprintf("ML Repo %q not found", mlRepoName)})
		return
	}
	stored := map[string]any{}
	maps.Copy(stored, manifest)
	stored["source"] = storedSource
	version := s.applyArtifact(s.skills, name, stored)
	writeJSON(w, http.StatusOK, map[string]any{
		"data": artifactVersionJSON("agent-skill", "agent_skill_id", s.skills[name], repo, mlRepoName, name, version),
	})
}

// handleApplyPrompt implements PUT /api/svc/v1/prompt-versions: create a
// prompt or a new version of an existing one. Like the agent-skill endpoint,
// a dryRun flag is ignored.
func (s *Server) handleApplyPrompt(w http.ResponseWriter, r *http.Request) {
	manifest, ok := decodeManifestRequest(w, r)
	if !ok {
		return
	}
	if typ, _ := manifest["type"].(string); typ != "chat_prompt" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Validation Error: ChatPromptManifest : unsupported manifest type %q", typ)})
		return
	}
	name, mlRepoName, ok := requireArtifactNames(w, manifest)
	if !ok {
		return
	}
	if msg := validatePromptManifest(manifest); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": msg})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.mlrepos[mlRepoName]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": fmt.Sprintf("ML Repo %q not found", mlRepoName)})
		return
	}
	version := s.applyArtifact(s.prompts, name, manifest)
	writeJSON(w, http.StatusOK, map[string]any{
		"data": artifactVersionJSON("chat_prompt", "prompt_id", s.prompts[name], repo, mlRepoName, name, version),
	})
}

// validatePromptManifest enforces the chat_prompt manifest rules derived
// from the TrueFoundry SDK schemas.
func validatePromptManifest(manifest map[string]any) string {
	messages, _ := manifest["messages"].([]any)
	if len(messages) == 0 {
		return "Validation Error: ChatPromptManifest : must have required property 'messages'"
	}
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		switch role, _ := message["role"].(string); role {
		case "system", "user", "assistant", "developer", "tool":
		default:
			return fmt.Sprintf("messages entries require a valid role, got %q", role)
		}
	}
	if config, ok := manifest["model_configuration"].(map[string]any); ok {
		provider, _ := config["provider"].(string)
		model, _ := config["model"].(string)
		if provider == "" || model == "" {
			return "model_configuration requires provider and model"
		}
	}
	return ""
}

// requireArtifactNames extracts and requires the name and ml_repo of an
// artifact manifest, writing the real control plane's error otherwise.
func requireArtifactNames(w http.ResponseWriter, manifest map[string]any) (name, mlRepoName string, ok bool) {
	name, _ = manifest["name"].(string)
	mlRepoName, _ = manifest["ml_repo"].(string)
	if name == "" || mlRepoName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "ML Repo name and artifact name both are required to apply manifest"})
		return "", "", false
	}
	if !artifactNameRe.MatchString(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": fmt.Sprintf("Validation Error: name must match pattern %q", artifactNameRe.String())})
		return "", "", false
	}
	return name, mlRepoName, true
}

// applyArtifact stores a new version of a series. Callers must hold s.mu.
func (s *Server) applyArtifact(series map[string]*artifactSeries, name string, manifest map[string]any) int {
	a, exists := series[name]
	if !exists {
		a = &artifactSeries{id: "artifact-" + strconv.Itoa(s.nextID)}
		s.nextID++
		series[name] = a
	}
	a.latestVersion++
	a.manifest = manifest
	return a.latestVersion
}

// skillFrontmatter extracts the name and description fields of a SKILL.md
// YAML frontmatter block. ok is false when the document does not start with
// one.
func skillFrontmatter(md string) (name, description string, ok bool) {
	rest, found := strings.CutPrefix(md, "---\n")
	if !found {
		return "", "", false
	}
	block, _, found := strings.Cut(rest, "\n---")
	if !found {
		return "", "", false
	}
	for line := range strings.SplitSeq(block, "\n") {
		if v, found := strings.CutPrefix(line, "name:"); found {
			name = strings.TrimSpace(v)
		}
		if v, found := strings.CutPrefix(line, "description:"); found {
			description = strings.TrimSpace(v)
		}
	}
	return name, description, true
}

// managedStorageURI mimics the TrueFoundry-managed blob storage location an
// applied artifact source is rewritten to.
func managedStorageURI(mlRepoName, name string) string {
	return fmt.Sprintf("wasbs://mock@mock.blob.core.windows.net/tfy-managed-storage/%s/mlfoundry/%s/artifacts/%s/", Tenant, mlRepoName, name)
}

// artifactVersionJSON renders an AgentSkillVersion or PromptVersion response
// object; seriesIDKey is "agent_skill_id" or "prompt_id".
func artifactVersionJSON(kind, seriesIDKey string, a *artifactSeries, repo *mlRepo, mlRepoName, name string, version int) map[string]any {
	manifest := map[string]any{}
	maps.Copy(manifest, a.manifest)
	manifest["version"] = version
	return map[string]any{
		"id":         fmt.Sprintf("%s-v%d", a.id, version),
		seriesIDKey:  a.id,
		"fqn":        fmt.Sprintf("%s:%s/%s/%s:%d", kind, Tenant, mlRepoName, name, version),
		"ml_repo_id": repo.id,
		"manifest":   manifest,
		"created_by_subject": map[string]any{
			"subjectId": "mock-subject", "subjectType": "user", "subjectSlug": "mock-user", "subjectDisplayName": "Mock User",
		},
		"created_at": "2026-07-15T00:00:00Z",
		"updated_at": "2026-07-15T00:00:00Z",
	}
}

// handleListAgentSkills implements GET /api/svc/v1/agent-skills.
func (s *Server) handleListAgentSkills(w http.ResponseWriter, r *http.Request) {
	s.listArtifacts(w, r, s.skills, "agent-skill", "agent_skill_id")
}

// handleListPrompts implements GET /api/svc/v1/prompts.
func (s *Server) handleListPrompts(w http.ResponseWriter, r *http.Request) {
	s.listArtifacts(w, r, s.prompts, "chat_prompt", "prompt_id")
}

// listArtifacts renders an artifact series listing with the latest version
// embedded under latest_version, filtered by an exact name or fqn.
func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request, series map[string]*artifactSeries, kind, seriesIDKey string) {
	nameFilter := r.URL.Query().Get("name")
	fqnFilter := r.URL.Query().Get("fqn")

	s.mu.Lock()
	var items []map[string]any
	for name, a := range series {
		mlRepoName, _ := a.manifest["ml_repo"].(string)
		repo := s.mlrepos[mlRepoName]
		if repo == nil {
			// The containing repo was deleted out-of-band; hide the artifact.
			continue
		}
		fqn := fmt.Sprintf("%s:%s/%s/%s", kind, Tenant, mlRepoName, name)
		if nameFilter != "" && name != nameFilter {
			continue
		}
		if fqnFilter != "" && fqn != fqnFilter {
			continue
		}
		items = append(items, map[string]any{
			"id":             a.id,
			"ml_repo_id":     repo.id,
			"type":           kind,
			"name":           name,
			"fqn":            fqn,
			"latest_version": artifactVersionJSON(kind, seriesIDKey, a, repo, mlRepoName, name, a.latestVersion),
			"created_at":     "2026-07-15T00:00:00Z",
			"updated_at":     "2026-07-15T00:00:00Z",
		})
	}
	s.mu.Unlock()

	sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
	writePage(w, r, items)
}

// handleDeleteAgentSkill implements DELETE /api/svc/v1/agent-skills/{id}.
func (s *Server) handleDeleteAgentSkill(w http.ResponseWriter, r *http.Request) {
	s.deleteArtifact(w, r, s.skills, "Agent skill")
}

// handleDeletePrompt implements DELETE /api/svc/v1/prompts/{id}.
func (s *Server) handleDeletePrompt(w http.ResponseWriter, r *http.Request) {
	s.deleteArtifact(w, r, s.prompts, "Prompt")
}

func (s *Server) deleteArtifact(w http.ResponseWriter, r *http.Request, series map[string]*artifactSeries, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, a := range series {
		if a.id == r.PathValue("id") {
			delete(series, name)
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"message": kind + " not found"})
}
