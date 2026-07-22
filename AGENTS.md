# AGENTS.md

This file provides guidance to AI Agents when working with code in this repository.

## What this is

A Terraform provider for TrueFoundry (github.com/Staffbase/terraform-provider-truefoundry): it manages AI Gateway models by applying `provider-account/*` manifests against a TrueFoundry control plane, authenticated with a Bearer API token. Built on the Terraform **Plugin Framework** (`terraform-plugin-framework`, protocol v6) — not the legacy SDKv2; don't mix in `terraform-plugin-sdk` APIs.

## Commands

```sh
make build     # go build ./...
make lint      # gofmt -l . && go vet ./...
make test      # unit tests
make testacc   # acceptance tests (TF_ACC=1; needs the terraform CLI on PATH)
make docs      # regenerate docs/ (tfplugindocs, pinned as go.mod tool)
make docs-check
```

Run a single acceptance test:

```sh
TF_ACC=1 go test ./internal/provider/ -run 'TestAccProviderAccount_lifecycle' -v
```

Acceptance tests are fast (~7s total) and hermetic: they run the real `terraform` binary against an in-process mock control plane — no credentials or network needed. Run them for any change to `internal/provider/`.

Manual end-to-end demo (`examples/demo/main.tf` + `cmd/mock-control-plane` + `dev_overrides`): see README "Testing & validation".

## Architecture

Three packages, strictly layered:

- **`internal/client`** — thin HTTP client for the three TrueFoundry endpoints used: `PUT /api/svc/v1/apply` (create/update by manifest, returns `action` + `data.id`/`data.fqn`), `POST /api/svc/v1/delete` (delete by manifest), `GET /api/svc/v1/provider-integrations?type=model` (read/list). Also defines the wire types (snake_case JSON manifests).
- **`internal/mockcp`** — in-memory mock of that API surface. It is the executable spec of the API contract: unit tests, acceptance tests, and the `cmd/mock-control-plane` demo binary all share it. If you change a manifest field, update its validation too.
- **`internal/provider`** — the framework provider: resources `truefoundry_provider_account`, `truefoundry_custom_endpoint_account`, `truefoundry_azure_account` (blob storage), `truefoundry_slack_account` (notification channels), `truefoundry_hashicorp_account` (Vault secret stores), `truefoundry_system_secret_store` (DB-backed secret stores), `truefoundry_gateway_budget_config`, `truefoundry_gateway_data_access_config`, `truefoundry_gateway_data_routing_config`, `truefoundry_gateway_guardrails_config`, `truefoundry_gateway_global_settings` (tenant-wide singletons), `truefoundry_guardrail_group`, `truefoundry_virtual_account`; data source `truefoundry_models`. Provider config `host`/`api_key` falls back to `TFY_HOST`/`TFY_API_KEY`.

The wire format authority is the manifest schemas in
[truefoundry-typescript-sdk](https://github.com/truefoundry/truefoundry-typescript-sdk) —
`src/serialization/types/*.ts` (Fern maps camelCase TS to snake_case JSON). Deployed control
planes can be ahead of the published SDK; when adding a field that is not in the SDK, verify
acceptance with a `dryRun: true` apply against a real control plane (dryRun does not persist).
Real tenants can also contain manifest features this provider does not model —
see README "Known limitations" before pointing a resource at pre-existing accounts.

### Design constraints that shape the code

- **No dedicated GET endpoint for provider accounts exists**, but every entry in the
  provider-integrations list embeds the owning account (`providerAccount`: id + manifest
  without the integrations list). `Read` reconstructs the account from its integrations plus
  that embedded manifest, which forces: (a) every account needs ≥1 model (schema-enforced),
  because zero integrations is indistinguishable from a deleted account; (b) the control
  plane returns `auth_data.api_key` **as stored** — TrueFoundry secret references round-trip
  cleanly (and drift-detect), raw keys land in state on refresh/import. `terraform import`
  takes the account FQN and relies entirely on this Read path.
  **Gateway configs are the exception**: `GET /api/svc/v1/llm-gateway/config/<type>`
  (e.g. `gateway-budget-config`) returns the stored manifest directly, so
  `truefoundry_gateway_budget_config` reads via that endpoint and needs none of the above.
- **One `truefoundry_provider_account` resource, five provider types** (`openai`, `azure-openai`, `azure-foundry`, `aws-bedrock`, `virtual-model`) with different required-field combinations — notably azure-foundry has *per-model* auth/endpoints and no account-level auth. Account types whose integrations are not models get their own resource (`custom_endpoint_account`, `azure_account`, `slack_account`, `hashicorp_account`, `system_secret_store`) and are listed with their own `?type=` filter (`blob-storage`, `notification-channel`, `secret-store` — the latter shared by hashicorp and truefoundry accounts). In all resources the validation rules live in exactly two places: `ValidateConfig` in the resource (plan-time, attribute-level errors) and `validateManifest` in `mockcp` (mock server-side). Keep them in sync.
- The control plane verifies integration credentials at apply time for some types (Azure blob storage connection strings, Vault token/AppRole) but not others (Slack webhooks/bots) — `dryRun: true` applies exercise this too.
- `name` and `provider_type` are `RequiresReplace`; everything else updates in place via re-apply (apply is idempotent, `CREATE` vs `UPDATE`).

### Adding a provider type (e.g. gemini)

Fetch the account/model manifest shapes from the TypeScript SDK serialization files, then touch: `providerTypeValues` + schema attributes + `ValidateConfig` + `manifestFromModel`/`integrationToModel` (resource), wire structs (`client`), `validateManifest` (`mockcp`), an acceptance-test lifecycle + validation cases, and an example.

## Documentation workflow

`docs/` is **generated** — never hand-edit it. Source of truth is the schema `Description` fields and the snippets in `examples/{provider,resources,data-sources}/` (tfplugindocs' expected layout; `examples/demo/` is not part of docs). After changing either, run `make docs` and commit the regenerated markdown.
