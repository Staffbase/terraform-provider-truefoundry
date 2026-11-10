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

const mlReposPath = "/api/svc/v1/ml-repos"

// MlRepoManifest is an ml-repo manifest (type "ml-repo"). ML repos are the
// storage-backed containers that versioned artifacts — agent skills and
// prompts — live in; StorageIntegrationFQN references a blob storage
// integration (e.g. of a truefoundry_azure_account).
type MlRepoManifest struct {
	Type                  string          `json:"type"`
	Name                  string          `json:"name"`
	Description           string          `json:"description,omitempty"`
	StorageIntegrationFQN string          `json:"storage_integration_fqn"`
	Collaborators         *[]Collaborator `json:"collaborators,omitempty"`
}

// MlRepo is one entry of GET /api/svc/v1/ml-repos and the data of a
// successful PUT.
type MlRepo struct {
	ID         string         `json:"id"`
	TenantName string         `json:"tenant_name"`
	Manifest   MlRepoManifest `json:"manifest"`
}

// ApplyMlRepo creates or updates an ML repo from its manifest.
func (c *Client) ApplyMlRepo(ctx context.Context, manifest MlRepoManifest) (*MlRepo, error) {
	body := map[string]any{"manifest": manifest}
	var out struct {
		Data MlRepo `json:"data"`
	}
	if err := c.do(ctx, http.MethodPut, mlReposPath, nil, body, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// ListMlRepos returns the tenant's ML repos, following pagination. name
// optionally filters to the repo with that exact name.
func (c *Client) ListMlRepos(ctx context.Context, name string) ([]MlRepo, error) {
	var all []MlRepo
	offset := 0
	for {
		q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {"100"}}
		if name != "" {
			q.Set("name", name)
		}
		var page listIntegrationsResponse[MlRepo]
		if err := c.do(ctx, http.MethodGet, mlReposPath, q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		offset += len(page.Data)
		if len(page.Data) == 0 || offset >= page.Pagination.Total {
			return all, nil
		}
	}
}

// DeleteMlRepo deletes an ML repo (and the artifacts it contains) by its
// control plane ID.
func (c *Client) DeleteMlRepo(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, mlReposPath+"/"+url.PathEscape(id), nil, nil, nil)
}
