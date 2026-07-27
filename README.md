# Terraform Provider for TrueFoundry

Manage [TrueFoundry](https://www.truefoundry.com) AI Gateway resources with
Terraform, authenticated against a TrueFoundry control plane with an API token:

- **Model access** — provider accounts (OpenAI, Azure OpenAI, Azure AI Foundry,
  AWS Bedrock, virtual models) and the models exposed through them, plus custom
  endpoint accounts for arbitrary HTTP services proxied by the gateway.
- **Gateway configuration** — budgets, data access, data routing, guardrail
  wiring, and global settings, plus guardrail groups (Azure Prompt Shield and
  Content Safety).
- **Platform integrations** — Azure blob storage, Slack notification channels,
  HashiCorp Vault secret stores, and the built-in system secret store.
- **Access** — virtual accounts (service accounts) with scoped permissions.

Built on the [Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework).

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- A TrueFoundry control plane and an API token (personal access token or
  virtual account token)

## Getting started

The provider is published to the Staffbase Terraform Enterprise private
registry. Outside of TFE workspaces, authenticate once with
`terraform login terraform.staffbase.com`.

```hcl
terraform {
  required_providers {
    truefoundry = {
      source = "terraform.staffbase.com/staffbase/truefoundry"
    }
  }
}

provider "truefoundry" {
  host    = "https://my-org.truefoundry.cloud"
  api_key = var.truefoundry_api_key # or via TFY_HOST / TFY_API_KEY env vars
}

resource "truefoundry_provider_account" "azure_openai" {
  name           = "my-azure-openai"
  provider_type  = "azure-openai"
  api_key        = var.azure_openai_api_key
  azure_endpoint = "https://my-resource.openai.azure.com"

  models = [
    {
      name             = "gpt-4o"
      model_id         = "gpt-4o-deployment" # Azure deployment name
      api_version      = "2024-10-21"
      foundation_model = "gpt-4o-2024-11-20"
      deployment_type  = "datazone_eu"
      region           = "francecentral"
      model_types      = ["chat", "responses"]

      cost = {
        metric = "per_1000_tokens"
        input  = 0.0022
        output = 0.0088
      }
    },
  ]
}

data "truefoundry_models" "all" {}

# Service account with access to the gateway, token synced to Vault.
resource "truefoundry_virtual_account" "ci" {
  name = "ci-bot"
  permissions = [
    {
      resource_fqn  = truefoundry_provider_account.azure_openai.fqn
      resource_type = "provider-account"
      role_id       = "provider-account-access"
    },
  ]
}
```

More examples — every provider type, load-balanced custom endpoints, budget
rules, guardrails, data routing — live in
[`examples/resources/`](examples/resources/) and on each resource's
documentation page.

## Best practices

- **Use secret references for credentials.** Every credential attribute
  (`api_key`, connection strings, webhook URLs, bot tokens, …) accepts either a
  raw value or a TrueFoundry secret reference such as
  `my-tenant:hashicorp:vault-secrets:secret-store:vault::path/value`. Secret
  references keep secret material out of the Terraform state and enable drift
  detection; raw values are returned by the control plane as stored and land in
  the state on refresh/import. Secret stores themselves are manageable with
  `truefoundry_hashicorp_account` (Vault) and
  `truefoundry_system_secret_store` (control-plane database).
- **Give CI/CD its own virtual account.** Run Terraform with a
  `truefoundry_virtual_account` token whose permissions cover only the
  resources it manages, rather than a personal access token.
- **Treat gateway configs as singletons.** The `truefoundry_gateway_*_config`
  resources and `truefoundry_gateway_global_settings` are tenant-wide
  singletons: define at most one of each per control plane, and an apply
  replaces the entire rule set or settings manifest. If a config already exists
  on the tenant, import it before the first apply.
- **Hand tokens to consumers via a secret store.** A virtual account's token is
  stored in the Terraform state; prefer `secret_store_config` to sync it into
  Vault instead of wiring the sensitive `token` attribute through outputs.
- **Check the limitations before importing.** Only manage pre-existing
  resources whose features this provider models — see
  [Known limitations](#known-limitations).

## Importing existing resources

Provider accounts, custom endpoint accounts, Azure accounts, Slack accounts,
HashiCorp accounts, system secret stores, guardrail groups, and MCP servers
are imported by their FQN, virtual accounts by their ID, and gateway configs
(tenant-wide singletons) by fixed string IDs:

```sh
terraform import truefoundry_provider_account.azure_openai my-tenant:azure-openai:my-azure-openai
terraform import truefoundry_custom_endpoint_account.speech my-tenant:custom-endpoint:speech
terraform import truefoundry_azure_account.storage my-tenant:azure:my-blobstorage
terraform import truefoundry_slack_account.notifications my-tenant:slack:slack-webhooks
terraform import truefoundry_hashicorp_account.secrets my-tenant:hashicorp:vault-secrets
terraform import truefoundry_system_secret_store.system my-tenant:truefoundry:system-secret-store
terraform import truefoundry_guardrail_group.region my-tenant:guardrail-config-group:my-group
terraform import truefoundry_mcp_server.deepwiki my-tenant:mcp-server:deepwiki
terraform import truefoundry_virtual_mcp_server.docs my-tenant:mcp-server:docs
terraform import truefoundry_virtual_account.ci cmneogofq0002016y0anr48bj
terraform import truefoundry_gateway_budget_config.budget gateway-budget-config
terraform import truefoundry_gateway_data_access_config.access gateway-data-access-config
terraform import truefoundry_gateway_data_routing_config.routing gateway-data-routing-config
terraform import truefoundry_gateway_guardrails_config.guardrails gateway-guardrails-config
terraform import truefoundry_gateway_global_settings.settings gateway-global-settings
```

All attributes — including the models and `api_key` as stored on the control
plane — are populated from the API. Only import accounts whose provider type
and features this provider supports (see [Known limitations](#known-limitations)).

## Provider types

The supported provider types differ in where credentials live:

| `provider_type` | Account-level | Per-model |
| --- | --- | --- |
| `openai` | `api_key`, optional `base_url` | – |
| `azure-openai` | `api_key`, `azure_endpoint` | `api_version` (required), `foundation_model`, `deployment_type`, `region` |
| `azure-foundry` | – (no account auth) | `api_key`, `azure_endpoint` (both required), optional `api_version` |
| `aws-bedrock` | `region` plus `api_key` or `access_key_id`+`secret_access_key` | optional `region` override |
| `virtual-model` | – (no credentials) | `routing` (required): weight- or priority-based targets, optional `slug` |

Invalid combinations are rejected at plan time.

## Documentation

Full schema reference, examples, and import syntax in [`docs/`](docs/):
[provider](docs/index.md),
[`truefoundry_provider_account`](docs/resources/provider_account.md),
[`truefoundry_custom_endpoint_account`](docs/resources/custom_endpoint_account.md),
[`truefoundry_azure_account`](docs/resources/azure_account.md),
[`truefoundry_slack_account`](docs/resources/slack_account.md),
[`truefoundry_hashicorp_account`](docs/resources/hashicorp_account.md),
[`truefoundry_system_secret_store`](docs/resources/system_secret_store.md),
[`truefoundry_gateway_budget_config`](docs/resources/gateway_budget_config.md),
[`truefoundry_gateway_data_access_config`](docs/resources/gateway_data_access_config.md),
[`truefoundry_gateway_data_routing_config`](docs/resources/gateway_data_routing_config.md),
[`truefoundry_gateway_guardrails_config`](docs/resources/gateway_guardrails_config.md),
[`truefoundry_gateway_global_settings`](docs/resources/gateway_global_settings.md),
[`truefoundry_guardrail_group`](docs/resources/guardrail_group.md),
[`truefoundry_mcp_server`](docs/resources/mcp_server.md),
[`truefoundry_virtual_mcp_server`](docs/resources/virtual_mcp_server.md),
[`truefoundry_virtual_account`](docs/resources/virtual_account.md),
[`truefoundry_models`](docs/data-sources/models.md).

Docs are generated from the provider schema, the templates in
[`templates/`](templates/), and the snippets in
`examples/{provider,resources,data-sources}/` with
[terraform-plugin-docs](https://github.com/hashicorp/terraform-plugin-docs) —
never edit `docs/` by hand. After changing a schema description, template, or
example:

```sh
make docs        # regenerate
make docs-check  # validate
```

## Known limitations

- Provider types: `openai`, `azure-openai`, `azure-foundry`, `aws-bedrock`,
  `virtual-model`. Not yet supported: Azure
  certificate/client-secret/workload-identity auth, AWS assumed-role auth,
  volume-based pricing tiers in `cost`, and the advanced routing options
  `retry_config`, `sticky_routing`, `sla_cutoff`, `override_params`, and
  latency-based routing. **Do not manage a pre-existing provider account that
  uses unsupported features** — an apply would strip them from its manifest.
- Custom endpoint accounts (`truefoundry_custom_endpoint_account`): account-level
  default auth headers (`auth_data` on the account manifest) and ownership are
  not modeled — set auth headers per endpoint (same caveat as above for
  pre-existing accounts).
- Azure accounts (`truefoundry_azure_account`): only blob storage integrations
  are modeled. Azure accounts can also hold AKS, container registry, key vault,
  and repos integrations — **do not manage a pre-existing Azure account that
  uses them**, an apply would strip them from its manifest.
- Gateway configs: the budget config (`truefoundry_gateway_budget_config`),
  data access config (`truefoundry_gateway_data_access_config`), data routing
  config (`truefoundry_gateway_data_routing_config`), guardrails config
  (`truefoundry_gateway_guardrails_config`), and global settings
  (`truefoundry_gateway_global_settings`) are modeled. Other gateway configs
  (rate limiting, fallback, load balancing, OTel) and the
  newer tenant/team budget configs (`tenant-budget-config`,
  `team-budget-config`) are not.
- Guardrail types: `azure-prompt-shield` and `azure-content-safety` (API-key auth,
  as `truefoundry_guardrail_group`). Other guardrail integrations (AWS Bedrock
  guardrails, PII detection, custom webhooks, …) are not modeled.
- MCP servers: remote (`truefoundry_mcp_server`) and virtual
  (`truefoundry_virtual_mcp_server`) servers are modeled. OpenAPI-based
  (`mcp-server/openapi`), stdio, and TrueFoundry-managed MCP servers are not —
  **do not manage a pre-existing MCP server of those types with these
  resources** (Read refuses mismatched types on import). Auth0 organization
  settings on `oauth2` auth (`use_organization`, `default_organization`) and
  ownership are not modeled either (same steamroll caveat as above).
- Virtual accounts: auto-rotation, notification targets, ownership, and identity
  provider mappings are not modeled (same caveat as above for pre-existing
  accounts). The account token is stored in the Terraform state; prefer
  `secret_store_config` for handing it to consumers.
- The TrueFoundry API has no dedicated read endpoint for provider accounts, so
  the provider reads accounts back through their model integrations (which
  embed the account manifest). Every account therefore needs at least one
  model. The control plane returns `api_key` values as stored — with secret
  references that is harmless and enables drift detection; with raw keys the
  key material ends up in the Terraform state on refresh/import (one more
  reason to prefer secret references).

## Development

```sh
make build     # compile
make lint      # gofmt + go vet
make test      # unit tests
make testacc   # acceptance tests (needs the terraform CLI on PATH)
```

Acceptance tests run the real `terraform` CLI against an in-memory mock control
plane (`internal/mockcp`) — hermetic, no credentials required.

To run the provider by hand without a registry, use the mock control plane and
a `dev_overrides` CLI config:

```sh
go run ./cmd/mock-control-plane &                # mock control plane on :8330
go build -o /tmp/tfy-bin/terraform-provider-truefoundry .
cat > /tmp/dev.tfrc <<'EOF'
provider_installation {
  dev_overrides { "terraform.staffbase.com/staffbase/truefoundry" = "/tmp/tfy-bin" }
  direct {}
}
EOF
cd examples/demo && TF_CLI_CONFIG_FILE=/tmp/dev.tfrc terraform apply
```

## Releasing

Pushing a `v*` tag builds, signs, and publishes the provider to the
Terraform Enterprise private registry — see [RELEASING.md](RELEASING.md).
