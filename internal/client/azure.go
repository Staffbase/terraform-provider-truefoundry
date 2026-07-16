package client

import "context"

// AzureBlobStorage is one blob storage entry in an Azure provider account
// manifest, type "integration/blob-storage/azure/blob". AuthData uses type
// "connection-string" with ConnectionString.
type AzureBlobStorage struct {
	Type               string      `json:"type"`
	Name               string      `json:"name"`
	StorageRoot        string      `json:"storage_root"`
	AuthData           *APIKeyAuth `json:"auth_data,omitempty"`
	AuthorizedSubjects []string    `json:"authorized_subjects,omitempty"`
}

// AzureAccountManifest is a "provider-account/azure" manifest. Azure accounts
// can also carry AKS, container registry, key vault, and repos integrations —
// this provider only models blob storage.
type AzureAccountManifest struct {
	Type           string             `json:"type"`
	Name           string             `json:"name"`
	SubscriptionID string             `json:"subscription_id,omitempty"`
	Integrations   []AzureBlobStorage `json:"integrations"`
}

// BlobStorageIntegration is one entry of
// GET /api/svc/v1/provider-integrations?type=blob-storage.
type BlobStorageIntegration struct {
	ID                 string                   `json:"id"`
	Name               string                   `json:"name"`
	FQN                string                   `json:"fqn"`
	Type               string                   `json:"type"`
	Manifest           AzureBlobStorage         `json:"manifest"`
	ProviderAccountFQN string                   `json:"providerAccountFqn"`
	ProviderAccount    *EmbeddedProviderAccount `json:"providerAccount,omitempty"`
}

// ListBlobStorageIntegrations returns all blob storage integrations,
// following pagination. fqn optionally filters to a single integration.
func (c *Client) ListBlobStorageIntegrations(ctx context.Context, fqn string) ([]BlobStorageIntegration, error) {
	return listIntegrations[BlobStorageIntegration](ctx, c, "blob-storage", fqn)
}
