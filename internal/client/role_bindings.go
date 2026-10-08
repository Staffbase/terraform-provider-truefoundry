package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Role bindings ("role-binding" manifests) grant a role on a resource to
// users, teams, virtual accounts, or agents. They replace the per-resource
// collaborators and the virtual account permissions from control plane
// v0.172.0 on. Like roles, the manifest is camelCase on the wire.
//
// The endpoints are taken from the published API reference
// (https://www.truefoundry.com/docs/api-reference/role-bindings, 2026-10-08):
//
//	PUT    /api/svc/v1/role-bindings       – create or update, matched by name
//	GET    /api/svc/v1/role-bindings       – paginated list, ?name= exact filter
//	GET    /api/svc/v1/role-bindings/{id}  – one binding, 404 when gone
//	DELETE /api/svc/v1/role-bindings/{id}
const roleBindingsPath = "/api/svc/v1/role-bindings"

// RoleBindingSubject is a principal of a role binding. Name is the email for
// type "user" and the team, virtual account, or agent name otherwise.
type RoleBindingSubject struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// RoleBindingPermission grants Role on the resource identified by
// ResourceType and ResourceFQN.
type RoleBindingPermission struct {
	ResourceType string `json:"resourceType"`
	ResourceFQN  string `json:"resourceFqn"`
	Role         string `json:"role"`
}

// RoleBindingManifest is a "role-binding" manifest. The control plane accepts
// either one subject with many permissions or many subjects with one
// permission, never many of both.
type RoleBindingManifest struct {
	Type        string                  `json:"type"`
	Name        string                  `json:"name"`
	Subjects    []RoleBindingSubject    `json:"subjects"`
	Permissions []RoleBindingPermission `json:"permissions"`
}

// RoleBinding is a stored role binding.
type RoleBinding struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Manifest RoleBindingManifest `json:"manifest"`
}

type roleBindingEnvelope struct {
	Data RoleBinding `json:"data"`
}

// ApplyRoleBinding creates or updates the role binding named in manifest.
func (c *Client) ApplyRoleBinding(ctx context.Context, manifest RoleBindingManifest, dryRun bool) (*RoleBinding, error) {
	body := map[string]any{"manifest": manifest, "dryRun": dryRun}
	var out roleBindingEnvelope
	if err := c.do(ctx, http.MethodPut, roleBindingsPath, nil, body, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// GetRoleBinding returns the role binding with the given ID, or nil if it
// does not exist.
func (c *Client) GetRoleBinding(ctx context.Context, id string) (*RoleBinding, error) {
	var out roleBindingEnvelope
	err := c.do(ctx, http.MethodGet, roleBindingsPath+"/"+url.PathEscape(id), nil, nil, &out)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// GetRoleBindingByName returns the role binding with the given name, or nil
// if it does not exist. The list endpoint documents an exact-match name
// filter, but other list endpoints of this control plane silently ignore
// theirs, so the match is repeated client-side and every page is followed.
func (c *Client) GetRoleBindingByName(ctx context.Context, name string) (*RoleBinding, error) {
	offset := 0
	for {
		q := url.Values{"name": {name}, "offset": {strconv.Itoa(offset)}, "limit": {"100"}}
		var page listIntegrationsResponse[RoleBinding]
		if err := c.do(ctx, http.MethodGet, roleBindingsPath, q, nil, &page); err != nil {
			return nil, err
		}
		for i, binding := range page.Data {
			if binding.Name == name {
				return &page.Data[i], nil
			}
		}
		offset += len(page.Data)
		if len(page.Data) == 0 || offset >= page.Pagination.Total {
			return nil, nil
		}
	}
}

// DeleteRoleBinding deletes the role binding with the given ID.
func (c *Client) DeleteRoleBinding(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, roleBindingsPath+"/"+url.PathEscape(id), nil, nil, nil)
}
