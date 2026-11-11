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

import "context"

// VaultIntegration is one HashiCorp Vault KV secret store entry in a
// hashicorp provider account manifest, type
// "integration/secret-store/hashicorp/vault". AuthData uses type "token"
// with Token, or type "approle" with RoleID and SecretID.
type VaultIntegration struct {
	Type               string      `json:"type"`
	Name               string      `json:"name"`
	VaultURL           string      `json:"vault_url"`
	KVMountPath        string      `json:"kv_mount_path"`
	RootPath           string      `json:"root_path,omitempty"`
	Namespace          string      `json:"namespace,omitempty"`
	AuthData           *APIKeyAuth `json:"auth_data,omitempty"`
	AuthorizedSubjects []string    `json:"authorized_subjects,omitempty"`
}

// HashicorpAccountManifest is a "provider-account/hashicorp" manifest
// carrying Vault secret store integrations.
type HashicorpAccountManifest struct {
	Type         string             `json:"type"`
	Name         string             `json:"name"`
	Integrations []VaultIntegration `json:"integrations"`
}

// SecretStoreIntegration is one entry of
// GET /api/svc/v1/provider-integrations?type=secret-store.
type SecretStoreIntegration struct {
	ID                 string                   `json:"id"`
	Name               string                   `json:"name"`
	FQN                string                   `json:"fqn"`
	Type               string                   `json:"type"`
	Manifest           VaultIntegration         `json:"manifest"`
	ProviderAccountFQN string                   `json:"providerAccountFqn"`
	ProviderAccount    *EmbeddedProviderAccount `json:"providerAccount,omitempty"`
}

// ListSecretStoreIntegrations returns all secret store integrations,
// following pagination. fqn optionally filters to a single integration.
func (c *Client) ListSecretStoreIntegrations(ctx context.Context, fqn string) ([]SecretStoreIntegration, error) {
	return listIntegrations[SecretStoreIntegration](ctx, c, "secret-store", fqn)
}
