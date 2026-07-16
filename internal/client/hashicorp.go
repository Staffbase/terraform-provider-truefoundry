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
