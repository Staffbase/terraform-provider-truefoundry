package client

import "context"

// GuardrailConfigSettings is the type-specific `config` block of a guardrail
// config. ResourceName/APIVersion/CustomHost apply to both Azure types;
// BlocklistNames/SeverityThreshold/Categories to azure-content-safety only.
type GuardrailConfigSettings struct {
	ResourceName      string   `json:"resource_name,omitempty"`
	APIVersion        string   `json:"api_version,omitempty"`
	CustomHost        string   `json:"custom_host,omitempty"`
	BlocklistNames    []string `json:"blocklist_names,omitempty"`
	SeverityThreshold *int64   `json:"severity_threshold,omitempty"`
	Categories        []string `json:"categories,omitempty"`
}

// GuardrailConfig is one guardrail entry in a guardrail config group, e.g.
// type "integration/guardrail-config/azure-prompt-shield".
type GuardrailConfig struct {
	Type              string                  `json:"type"`
	Name              string                  `json:"name"`
	Description       string                  `json:"description,omitempty"`
	AuthData          *APIKeyAuth             `json:"auth_data,omitempty"`
	Operation         string                  `json:"operation,omitempty"` // "validate" for the Azure types
	EnforcingStrategy string                  `json:"enforcing_strategy,omitempty"`
	Config            GuardrailConfigSettings `json:"config"`
}

// GuardrailGroupManifest is a "provider-account/guardrail-config-group"
// manifest carrying guardrail configs.
type GuardrailGroupManifest struct {
	Type          string            `json:"type"`
	Name          string            `json:"name"`
	Collaborators *[]Collaborator   `json:"collaborators,omitempty"`
	Integrations  []GuardrailConfig `json:"integrations"`
}

// GuardrailIntegration is one entry of
// GET /api/svc/v1/provider-integrations?type=guardrail-config.
type GuardrailIntegration struct {
	ID                 string                   `json:"id"`
	Name               string                   `json:"name"`
	FQN                string                   `json:"fqn"`
	Type               string                   `json:"type"`
	Manifest           GuardrailConfig          `json:"manifest"`
	ProviderAccountFQN string                   `json:"providerAccountFqn"`
	ProviderAccount    *EmbeddedProviderAccount `json:"providerAccount,omitempty"`
}

// ListGuardrailIntegrations returns all guardrail config integrations,
// following pagination. fqn optionally filters to a single integration.
func (c *Client) ListGuardrailIntegrations(ctx context.Context, fqn string) ([]GuardrailIntegration, error) {
	return listIntegrations[GuardrailIntegration](ctx, c, "guardrail-config", fqn)
}
