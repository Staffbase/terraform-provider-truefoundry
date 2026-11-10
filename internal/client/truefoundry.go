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

// TrueFoundrySecretStore is one DB-backed system secret store entry in a
// TrueFoundry provider account manifest, type
// "integration/secret-store/truefoundry/db". Secrets are stored in the
// control plane's own database; there is no configuration beyond the name.
type TrueFoundrySecretStore struct {
	Type               string   `json:"type"`
	Name               string   `json:"name"`
	AuthorizedSubjects []string `json:"authorized_subjects,omitempty"`
}

// TrueFoundryAccountManifest is a "provider-account/truefoundry" manifest
// carrying system secret store integrations.
type TrueFoundryAccountManifest struct {
	Type         string                   `json:"type"`
	Name         string                   `json:"name"`
	Integrations []TrueFoundrySecretStore `json:"integrations"`
}

// TrueFoundrySecretStoreIntegration is one entry of
// GET /api/svc/v1/provider-integrations?type=secret-store, decoded for
// truefoundry/db stores (the same listing also returns Vault stores, see
// SecretStoreIntegration).
type TrueFoundrySecretStoreIntegration struct {
	ID                 string                   `json:"id"`
	Name               string                   `json:"name"`
	FQN                string                   `json:"fqn"`
	Type               string                   `json:"type"`
	Manifest           TrueFoundrySecretStore   `json:"manifest"`
	ProviderAccountFQN string                   `json:"providerAccountFqn"`
	ProviderAccount    *EmbeddedProviderAccount `json:"providerAccount,omitempty"`
}

// ListTrueFoundrySecretStoreIntegrations returns all secret store
// integrations decoded as system secret stores, following pagination. fqn
// optionally filters to a single integration. Callers must filter for
// manifest type "integration/secret-store/truefoundry/db".
func (c *Client) ListTrueFoundrySecretStoreIntegrations(ctx context.Context, fqn string) ([]TrueFoundrySecretStoreIntegration, error) {
	return listIntegrations[TrueFoundrySecretStoreIntegration](ctx, c, "secret-store", fqn)
}
