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

package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Secret groups ("secret-group" manifests) are the named buckets that
// individual secrets live in, backed by a secret-store integration of a
// truefoundry_system_secret_store or truefoundry_hashicorp_account. This file
// models the group; the secrets in it are in secrets.go.
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
// not modelled here: truefoundry_secret_group must not put the tenant's
// secrets into the Terraform state. truefoundry_secret reads them through
// ListSecrets instead.
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
