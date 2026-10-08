package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Secret groups ("secret-group" manifests) are the named buckets that
// individual secrets live in, backed by a secret-store integration of a
// truefoundry_system_secret_store or truefoundry_hashicorp_account. The
// secrets themselves are created out of band; this package models only the
// group.
//
// The manifest is snake_case, but the read endpoint's envelope and entry
// fields are camelCase — both shapes appear in this file.
const secretGroupsPath = "/api/svc/v1/secret-groups"

// SecretGroupManifest is a "secret-group" manifest.
type SecretGroupManifest struct {
	Type           string          `json:"type"`
	Name           string          `json:"name"`
	IntegrationFQN string          `json:"integration_fqn"`
	Collaborators  *[]Collaborator `json:"collaborators,omitempty"`
}

// SecretGroup is one entry of GET /api/svc/v1/secret-groups.
//
// The entry also carries associatedSecrets — metadata about the secrets the
// group holds (names, FQNs, version numbers; no values). It is deliberately
// not modelled: leaving the field off the struct guarantees nothing about
// the tenant's secrets can reach the Terraform state.
type SecretGroup struct {
	ID            string              `json:"id"`
	FQN           string              `json:"fqn"`
	Name          string              `json:"name"`
	TenantName    string              `json:"tenantName"`
	IntegrationID string              `json:"integrationId"`
	Manifest      SecretGroupManifest `json:"manifest"`
}

// ListSecretGroups returns the tenant's secret groups, following pagination.
// fqn optionally filters to the group with that exact FQN, which the endpoint
// applies server-side.
func (c *Client) ListSecretGroups(ctx context.Context, fqn string) ([]SecretGroup, error) {
	var all []SecretGroup
	offset := 0
	for {
		q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {"100"}}
		if fqn != "" {
			q.Set("fqn", fqn)
		}
		var page listIntegrationsResponse[SecretGroup]
		if err := c.do(ctx, http.MethodGet, secretGroupsPath, q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		offset += len(page.Data)
		if len(page.Data) == 0 || offset >= page.Pagination.Total {
			return all, nil
		}
	}
}
