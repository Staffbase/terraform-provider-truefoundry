# AGENTS.md

Terraform provider for TrueFoundry AI Gateway (Bearer token, manifests applied against a control plane). Built on **terraform-plugin-framework** (protocol v6) — never mix in `terraform-plugin-sdk` (SDKv2) APIs.

## Commands

`make build | lint | test | testacc | docs | docs-check` (see Makefile). Single acceptance test:

```sh
TF_ACC=1 go test ./internal/provider/ -run 'TestAccProviderAccount_lifecycle' -v
```

Acceptance tests are hermetic (real `terraform` binary vs in-process mock, ~7s). Run them for any change to `internal/provider/`.

## Architecture

Layered: `internal/client` (HTTP + wire types, snake_case manifests) → `internal/mockcp` → `internal/provider`.

- `mockcp` is the executable spec of the API contract, shared by unit tests, acceptance tests and `cmd/mock-control-plane`. Changing a manifest field means updating its validation there too.
- Validation lives in exactly two places per resource: `ValidateConfig` (plan time) and `validateManifest` in `mockcp`. Keep them in sync.
- Wire format authority: manifest schemas in [truefoundry-typescript-sdk](https://github.com/truefoundry/truefoundry-typescript-sdk) `src/serialization/types/*.ts`. Deployed control planes can be ahead of the SDK. Verify new fields with a `dryRun: true` apply against a real control plane (does not persist).
- `name` and `provider_type` are `RequiresReplace`; everything else updates in place by re-apply.
- Real tenants can hold manifest features the provider does not model; see README "Known limitations".

## Control-plane gotchas

Endpoints and envelopes are inconsistent. Check the client code before assuming; these are the non-obvious ones.

### Provider accounts

- No GET endpoint. `Read` rebuilds the account from `provider-integrations?type=` entries, each embedding `providerAccount`. So every account needs ≥1 model (zero integrations looks like a deleted account). `import` takes the account FQN.
- `auth_data.api_key` returns as stored: secret refs round-trip, raw keys land in state.
- Non-model account types have own resources and `?type=` filters (`blob-storage`, `notification-channel`, `secret-store`; the last is shared by hashicorp and truefoundry accounts).
- Credentials are verified at apply for some types (Azure blob, Vault) but not others (Slack).
- `microsoft-foundry` has account-level `api_key` + endpoints and a required per-model `publisher`; `azure-foundry` has per-model auth. Control plane rejects `decision` on `azure-foundry`.
- `decision` model type is ahead of the SDK (enforced server-side). Provider does not restrict model types per provider type.
- `slack_account.webhooks[*].webhook_url` is masked as `"[redacted]"` on read. `Read` keeps prior state value; import reads `[redacted]` (test uses `ImportStateVerifyIgnore`). `bot_token` unprobed.

### Gateway configs and budgets

- Gateway configs (`gateway-*-config`, `gateway-global-settings`) read via `GET llm-gateway/config/<type>`.
- v2 budgets (`tenant-budget-config`, `team-budget-config`) are many named rules: apply/delete generic, read via `GET llm-gateway/budgets?type=`. `llm-gateway/config/<type>` returns empty 200 for them. List honors only `type`; match name client-side. Envelope is `{"data": [...]}`; the shape changed once before (bare array), so decode via `listIntegrationsResponse[T]`.
- `applies_to.type` is immutable: `RequiresReplace` with `name`/`team_name`. `team_name` is checked against real teams.
- v1 `truefoundry_gateway_budget_config` is deprecated (dead after 15 Aug 2026). Migration table in README.

### Guardrail policies (verified 2026-11-02)

- Since control plane v0.179 each guardrail rule is its own named `gateway-guardrails-config` (top-level `name`, `when`, four required hook arrays, optional `custom_error_message`, no `rules`): `truefoundry_gateway_guardrails_policy`. The folded `rules[]` format (`truefoundry_gateway_guardrails_config`) is deprecated and removed **15 Nov 2026**.
- Apply via generic `PUT apply` (honors `dryRun`, returns `action` + `data.id`). Unknown guardrail refs → 400. Read/list via `GET llm-gateway/config/gateway-guardrails-config/instances[/{name}]` (URL-encode name): list is a **bare array**, missing → 404 `Gateway configuration instance not found`. Delete via `DELETE .../instances/{name}` (seen in the UI, **not exercised live**); 404 = already deleted.
- `when.target` needs a model / mcpServers / mcpTools condition (`*` allowed); model targets need an LLM input/output hook, MCP targets a pre/post-invoke hook. Only named policies enforce this.
- On v0.179+ the folded singleton GET answers an **empty 200**. The client returns `ErrFoldedGuardrailsRetired` so refresh/import/read-back stop with migration guidance instead of dropping state; only 404 means missing. `mockcp.SetFoldedGuardrailsRetired` simulates it.
- The instances list also answers 200 on the older Core control plane; that alone does not prove named apply works there.
- Shared rule validators reject empty optional strings/collections (they normalize to null on read and would diff forever): omit instead.

### Roles and role bindings

- `role` manifest is **camelCase** (`displayName`, `resourceType`). Listed via singular `GET /api/svc/v1/role`: bare array, ignores `?name=`, match client-side.
- Only `tenant` and `team` scopes accepted (`account` is normalized to `tenant`).
- The list returns every role, built-in ones included. On the Cloud control plane at least one carries `permissions` as a single string (2026-11-03), so `RolePermissions` also decodes a string; otherwise one odd role breaks every role read.
- `Read` refuses to adopt reserved roles. `Delete` leaves TrueFoundry default roles (e.g. `member`) in place with a warning.
- Permission vocabulary is a closed 81-string set, duplicated in `internal/mockcp/roles.go`. Keep in sync.
- `role_binding`: camelCase `{data}` envelopes, paginated list, read by ID, import by name. Shape from API reference, **not verified against a real control plane**; probe with `dryRun`.

### Legacy access fields → role bindings

Per-resource `collaborators` and virtual account `permissions` are deprecated (control plane v0.172.0), rejected after **15 Nov 2026**. Three wire states, hence `*[]Collaborator` + `omitempty` on every manifest struct: omitted = grants untouched, `[]` = cleared, non-empty = replaced (`permissions: []` rejected). Never send `collaborators: []` for an unset attribute (it silently revoked UI-granted access).

- `Read` leaves unset attributes unset, keeps prior value if the response omits them, and adopts remote grants only on the first read after import (private state key `legacy_grants_import`).
- `mockcp.RetireLegacyGrants()` simulates the cut-off; `TestAccLegacyGrants_retired` guards it.

### Secrets and secret groups

- Secret groups: `GET secret-groups` (camelCase `{data, pagination}`; `?fqn=` works, but match by name since the tenant-prefixed FQN is unknown before first read). `integration_fqn` must be an existing `secret-store`.
- `associatedSecrets` is deliberately absent from the wire struct so no secret metadata reaches state.
- Deleting or renaming a group destroys its secrets.
- `truefoundry_secret` has no manifest. `PUT secret-groups/{id}` with `{"secrets":[{key, value?}]}` replaces the whole list: key without value keeps the secret, new key without value → 422, omitted keys are deleted, empty list → 400. Last secret goes through `DELETE secrets/{id}`. **Not verified against a real control plane.**
- Every write is read-modify-write, so `client.PutSecret`/`DeleteSecret` hold a per-group mutex (Terraform applies in parallel).
- Values are never returned by group endpoints; `GET secrets/{id}` returns one unless `DISABLE_SECRET_VALUE_VIEW`.

### Gateway installations

- Apply via generic `PUT apply` (`gateway-installation`, requires `isDefault`, no `action` in response). Generic delete rejects the type; use `DELETE llm-gateway/installations/{id}`.
- List ignores `?name=`; stored manifest may omit `isDefault`, so read the entry's top-level one. A URL belongs to one installation per tenant (409).
- `GET .../installations/token?name=` mints a new token on every call: call only on create, never on refresh.

### MCP servers

- `mcp-server/{remote,openapi,virtual}` read via `GET mcp-servers?fqn=`. Apply response has no `data.id`; read the ID back from the list after each apply.
- Never use `provider-account/mcp-server-group` (500s on real control planes).

### Agents

- `truefoundry-agent` and `remote-agent/self-hosted` go through `PUT /agents`: every apply creates a new immutable version (FQN like `tenant:agent:name:3`). Read via `GET agents?name=` (latest manifest in `latestVersionDetails`). Delete by `agents/{id}`. Import takes the agent **name**, not FQN.
- Control plane rejects agents referencing nonexistent MCP servers. Legacy `agent` manifest not modeled.

### ML repos, agent skills, prompts

- `ml-repo`: plain CRUD, in-place update; `storage_integration_fqn` must be an existing blob-storage integration.
- `agent-skill` / `chat_prompt` are versioned artifacts (`agent-skill-versions`, `prompt-versions`; read via `agent-skills?name=` / `prompts?name=`, latest under `latest_version`; delete by series ID).
- These list endpoints use **snake_case** envelopes (agents list is camelCase).
- Their apply endpoints **ignore `dryRun` and always persist**. Never probe them with real credentials or valid manifests.
- Control plane rewrites skill sources to a managed blob URI, so `agent_skill.Read` preserves state `source` (content drift undetectable). Inline sources need YAML frontmatter with `name` matching the manifest name.
- Prompts model only plain-string messages, variables, `model_configuration`; other `chat_prompt` blocks are dropped on apply.

### Virtual accounts

Token is set on create and not refetched on refresh (it doubled request count, DEVS-1533), unless `auto_rotate` is set or the token was never populated (fresh import). `auto_rotate` shape (`auto_rotate_interval`/`grace_period`, min 30/1 days) is from docs/SDK, **not verified live**.

## Manifest name rules

Control plane pattern `^[a-z][a-z0-9\-]{1,N}[a-z0-9]$`: lowercase letter first, letter/digit last; only `N` varies. SDKs carry no constraint, so they are no authority. Bounds below were probed live (2026-09-08) with `dryRun`. A failing union branch reports the *last* branch's error (e.g. `must have required property 'aws_account_id'`): that means "no branch matched", not that the name is fine.

| manifest family | name rule |
| --- | --- |
| `provider-account/{openai,azure-openai,azure-foundry,aws-bedrock,custom-endpoint,truefoundry}` | 3-32 |
| `provider-account/{azure,hashicorp,slack,guardrail-config-group}`, `virtual-account` | 3-36 |
| `provider-account/virtual-model` | 3-64 |
| integration names (nested in an account) | 1-64 |
| `mcp-server/{remote,openapi,virtual}` | 3-73 |
| `truefoundry-agent`, `remote-agent/self-hosted` | 3-32, no consecutive hyphens |
| `role` | 3-35 |
| `role-binding` | 3-66 (from API reference, not probed) |
| `secret-group` | 5-50, no consecutive hyphens |
| `gateway-installation` | 3-35, consecutive hyphens allowed |
| `ml-repo` | 3-100, upper case allowed |
| `tenant-budget-config`, `team-budget-config` | 2-62, upper case allowed |
| named `gateway-guardrails-config` policies | letters, digits, hyphens; no length limit |
| other `gateway-*-config`, `gateway-global-settings` | not validated |

`provider_account` spans two bounds: schema validator allows 64, `ValidateConfig` enforces 32 for all but `virtual-model`. `agent-skill`/`chat_prompt` names are unprobed (see above).

## Adding a provider type

Fetch manifest shapes from the SDK serialization files, then touch: `providerTypeValues`, schema attributes, `ValidateConfig`, `manifestFromModel`/`integrationToModel`, client wire structs, `validateManifest` (`mockcp`), acceptance lifecycle + validation tests, an example.

## Commits

Conventional Commits, ticket as scope where one exists: `feat(DEVS-1432): ...`, `chore(deps): ...`, `fix: ...`. Format `type(scope)!: subject`, header ≤ 100 chars. Types: `feat fix chore ci docs refactor test perf build style revert`. CI checks every non-merge commit.

Commit prefixes do not drive releases: version and changelog come from **PR labels**. `!`/`BREAKING CHANGE` does nothing alone; label the PR `breaking`.

## Pull requests

Draft PRs (`gh pr create --draft`), fill in `.github/pull_request_template.md`, under 200 words, never list changed files.

**Every PR needs a label**: consumers use semver constraints (`~> 0.2`) and release-drafter derives version + changelog from labels only. Unlabelled = no changelog section, patch bump.

| Label | Bump | Section |
| --- | --- | --- |
| `breaking`, `major` | major | Breaking Changes |
| `feature`, `enhancement`, `minor` | minor | Features |
| `fix`, `bug` | patch | Bug Fixes |
| `documentation` | patch | Documentation |
| `chore`, `dependencies`, `patch` | patch | Maintenance |
| `skip-changelog` | patch | excluded |

First matching category wins (`breaking` + `feature` → Breaking Changes). Pick by practitioner-visible Terraform surface, not diff size:

- `breaking`: renamed/removed attribute or resource, new required field, changed default, anything forcing replacement or failing existing configs.
- `feature`: new resource, data source, provider type, or optional attribute.
- `fix`: wrong wire format, undetected drift, crash, validation rejecting valid config.
- `documentation`: README, AGENTS.md, examples, regenerated `docs/`.
- `chore`: refactors, tests, CI, dependency bumps.
- `skip-changelog`: nothing a consumer cares about.

Releasing: see RELEASING.md.

## Documentation

`docs/` is **generated**; never hand-edit. Sources: schema `Description` fields, `templates/`, `examples/{provider,resources,data-sources}/` (`examples/demo/` excluded). After changing them run `make docs` and commit the output.

- Per-resource/attribute prose → schema `Description`.
- Multi-resource topics with a start and end (migrations, deprecation walkthroughs) → `templates/guides/<topic>.md.tmpl` (needs `page_title`/`subcategory`/`description` frontmatter; relative links like `../resources/foo.md`). README gets a short summary + link.
- README: what it is, getting started, best practices, import syntax, known limitations. No migration walkthroughs.
- No `CHANGELOG.md` or `UPGRADE.md`; release notes come from release-drafter. Explain breaking changes in the PR description, release note, and a guide if configs must be rewritten.
