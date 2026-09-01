package client

import (
	"context"
	"net/http"
)

// Roles are tenant governance manifests ("role"). Unlike almost every other
// manifest this package models, the role manifest is camelCase on the wire —
// snake_case display_name is rejected with
// 422 "RoleManifest : must have required property 'displayName'".
//
// The list endpoint is singular and returns a bare JSON array with no
// {data, pagination} envelope, and it ignores a ?name= filter, so GetRole
// matches client-side (like GetBudget).
const rolesPath = "/api/svc/v1/role"

// RoleManifest is a "role" manifest. ⚠️ camelCase field names — see above.
type RoleManifest struct {
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	DisplayName  string   `json:"displayName"`
	Description  string   `json:"description,omitempty"`
	ResourceType string   `json:"resourceType"`
	Permissions  []string `json:"permissions"`
}

// Role is one entry of GET /api/svc/v1/role.
//
// IsEditable is false for the roles TrueFoundry reserves and clients must not
// manage. IsDefault marks a role every subject of the tenant gets — those are
// created by TrueFoundry at onboarding and can be updated in place, but
// deleting one is a tenant-wide permission change.
//
// The control plane normalizes ResourceType: a manifest applied with
// "account" is stored as "tenant", in both this field and Manifest.
// permissionSetsV2 (the control plane's expansion of Permissions) is
// deliberately not modelled — it would be a second source of truth.
type Role struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	ResourceType string       `json:"resourceType"`
	Manifest     RoleManifest `json:"manifest"`
	IsEditable   bool         `json:"isEditable"`
	IsDefault    bool         `json:"isDefault"`
	IsReserved   bool         `json:"isReserved"`
}

// ListRoles returns every role on the tenant, built-in ones included. The
// endpoint returns a bare array and is not paginated.
func (c *Client) ListRoles(ctx context.Context) ([]Role, error) {
	var out []Role
	if err := c.do(ctx, http.MethodGet, rolesPath, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetRole returns the role with the given name, or nil if it does not exist.
// The list endpoint ignores a name filter, so the match happens client-side.
func (c *Client) GetRole(ctx context.Context, name string) (*Role, error) {
	roles, err := c.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	for i, role := range roles {
		if role.Name == name {
			return &roles[i], nil
		}
	}
	return nil, nil
}
