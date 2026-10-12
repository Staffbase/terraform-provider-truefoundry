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

- **`internal/client`** — thin HTTP client for the TrueFoundry endpoints used: `PUT /api/svc/v1/apply` (create/update by manifest, returns `action` + `data.id`/`data.fqn`), `POST /api/svc/v1/delete` (delete by manifest), `GET /api/svc/v1/provider-integrations?type=model` (read/list), `GET /api/svc/v1/mcp-servers` (read/list MCP servers), `GET /api/svc/v1/secret-groups?fqn=` (read/list secret groups), `GET/PUT /api/svc/v1/secret-groups/{id}` + `GET/DELETE /api/svc/v1/secrets/{id}` (secrets inside a group), `GET /api/svc/v1/role` (read/list roles — singular, bare array), `GET /api/svc/v1/llm-gateway/budgets?type=` (read/list v2 budget rules), `PUT/GET /api/svc/v1/agents` + `DELETE /api/svc/v1/agents/{id}` (versioned agent CRUD), `PUT/GET/DELETE /api/svc/v1/ml-repos` (ML repo CRUD), the versioned artifact endpoints `PUT /api/svc/v1/agent-skill-versions` + `GET/DELETE /api/svc/v1/agent-skills` and `PUT /api/svc/v1/prompt-versions` + `GET/DELETE /api/svc/v1/prompts`, and `PUT/GET /api/svc/v1/role-bindings` + `GET/DELETE /api/svc/v1/role-bindings/{id}` (role binding CRUD). Also defines the wire types (snake_case JSON manifests).
- **`internal/mockcp`** — in-memory mock of that API surface. It is the executable spec of the API contract: unit tests, acceptance tests, and the `cmd/mock-control-plane` demo binary all share it. If you change a manifest field, update its validation too.
- **`internal/provider`** — the framework provider: resources `truefoundry_provider_account`, `truefoundry_custom_endpoint_account`, `truefoundry_azure_account` (blob storage), `truefoundry_slack_account` (notification channels), `truefoundry_hashicorp_account` (Vault secret stores), `truefoundry_system_secret_store` (DB-backed secret stores), `truefoundry_gateway_budget_config` (budget limiting v1, deprecated), `truefoundry_tenant_budget_config` + `truefoundry_team_budget_config` (budget limiting v2), `truefoundry_gateway_data_access_config`, `truefoundry_gateway_data_routing_config`, `truefoundry_gateway_guardrails_config`, `truefoundry_gateway_global_settings` (tenant-wide singletons), `truefoundry_guardrail_group`, `truefoundry_mcp_server` (remote MCP servers), `truefoundry_openapi_mcp_server` (OpenAPI-based MCP servers), `truefoundry_virtual_mcp_server` (MCP server aggregations), `truefoundry_agent` (TrueFoundry-hosted agents), `truefoundry_remote_agent` (self-hosted agents), `truefoundry_ml_repo` (artifact containers), `truefoundry_agent_skill` (versioned SKILL.md artifacts), `truefoundry_prompt` (versioned chat_prompt templates), `truefoundry_virtual_account`, `truefoundry_role` (custom roles), `truefoundry_role_binding` (access grants), `truefoundry_secret_group` (secret buckets on a secret store), `truefoundry_secret` (one secret in a group); data source `truefoundry_models`. Provider config `host`/`api_key` falls back to `TFY_HOST`/`TFY_API_KEY`.

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
  **Budget limiting v2 rules are not gateway configs**: `tenant-budget-config` and
  `team-budget-config` manifests are individually named rules (many per tenant), applied
  and deleted through the generic manifest endpoints but read via
  `GET /api/svc/v1/llm-gateway/budgets?type=` — `llm-gateway/config/<type>` returns an
  empty 200 for them, and the budgets list endpoint honors only the `type` query
  parameter (`name` is ignored, so match client-side). The list response wraps its
  array in a `{"data": [...]}` envelope with no `pagination` key (changed from a bare
  array observed on 2026-07-28; re-verified 2026-09-15 after a "cannot unmarshal object
  into Go value of type []client.BudgetV2" error surfaced on the dev tenant — `ListBudgets`
  now decodes via the shared `listIntegrationsResponse[T]` envelope type). Verified against
  a real control plane 2026-07-28: apply honors `dryRun`, the deployed manifest schema is ahead of the
  published SDK (`when.provider_accounts`, `limits.cost_per_lifetime`, and
  `alerts.notify_breaching_user` are accepted), unknown manifest fields pass through
  unvalidated, the rule name pattern is `^[a-zA-Z](?:[a-zA-Z0-9\-]{0,60}[a-zA-Z0-9])$`
  (upper case allowed, unlike other manifests), and `team_name` is checked against real
  teams (`400 "Invalid team id"`). Per the docs `applies_to.type` cannot change after
  creation, so it is `RequiresReplace` along with `name`/`team_name`. v1 stops working
  after 15 Aug 2026; `truefoundry_gateway_budget_config` carries a `DeprecationMessage`
  and the README has the field-by-field migration table.
  **Roles are camelCase and read from a singular endpoint**: the `role`
  manifest uses `displayName`/`resourceType` (snake_case `display_name` is
  rejected with `422 "RoleManifest : must have required property
  'displayName'"`), and roles are listed via `GET /api/svc/v1/role` —
  singular, a bare JSON array with no `{data, pagination}` envelope, and it
  ignores `?name=`, so `GetRole` matches client-side (unlike `GetBudget`,
  whose list endpoint now uses a `{"data": [...]}` envelope — see above). The
  control plane normalizes `resourceType`: a manifest applied with the legacy
  `account` is stored as `tenant`, so the provider accepts only `tenant` and
  `team` (any other scope is rejected outright with `400 "Resource type <x>
  is not supported"`). List entries carry `isEditable`/`isDefault`/
  `isReserved`: `truefoundry_role.Read` refuses to adopt a reserved role, and
  `Delete` leaves a TrueFoundry-provided default role (e.g. `member`) on the
  control plane, warning instead — deleting one is a tenant-wide permission
  change. The permission vocabulary is a closed 81-string set enumerated from
  the dev (2026-07-30) and Core (2026-09-01) control planes, which disagreed
  in both directions; it is duplicated in `internal/mockcp/roles.go` and must
  stay in sync.
  **Secret groups** are read via `GET /api/svc/v1/secret-groups` — paginated,
  camelCase `{data, pagination}` envelope, and its `?fqn=` filter does work
  (unlike the roles and budgets endpoints), though `getSecretGroupByName`
  matches on the name because the tenant-prefixed FQN is unknown before the
  first read. `integration_fqn` must name a `secret-store` integration that
  exists. List entries also carry `associatedSecrets` (metadata about the
  secrets in the group — names, FQNs, versions; no values); it is deliberately
  absent from the wire struct, so nothing about the tenant's secrets can reach
  the state. Deleting or renaming a group destroys the secrets it holds.
  **Secrets** (`truefoundry_secret`) have no manifest. Per the published API
  reference (2026-10-12, **not verified against a real control plane** — no
  tenant API key at hand when it was written): `PUT
  /api/svc/v1/secret-groups/{id}` with `{"secrets": [{key, value?}]}` replaces
  the group's whole secret list — a key without a value keeps an existing
  secret, a new key without a value is rejected (422), omitted keys are
  deleted, and an empty list is rejected (400), so the last secret of a group
  can only go through `DELETE /api/svc/v1/secrets/{id}`. Values are never
  returned by the group endpoints; `GET /api/svc/v1/secrets/{id}` returns one
  unless the control plane sets `DISABLE_SECRET_VALUE_VIEW`. Because every
  write is a read-modify-write of the whole list, `client.PutSecret` and
  `client.DeleteSecret` hold a per-group mutex: Terraform applies resources in
  parallel, and two unserialized writes would each drop the other's new
  secret.
  **MCP servers too**: standalone `mcp-server/remote` / `mcp-server/openapi` / `mcp-server/virtual` manifests
  (not provider-account integrations) are read via `GET /api/svc/v1/mcp-servers?fqn=`.
  Their apply response carries no `data.id`, so the resources read the ID back from
  that list endpoint after each apply. The `provider-account/mcp-server-group`
  manifest type also exists but is broken on real control planes (instant 500
  "Could not validate for type mcp-server" for any group with integrations) — don't
  use it.
  **Agents have their own versioned endpoints**: `truefoundry-agent` and
  `remote-agent/self-hosted` manifests go through `PUT /api/svc/v1/agents`
  (every apply creates a new immutable version; the response's `data` is the
  new AgentVersion with `agentId` and a versioned FQN like
  `tenant:agent:name:3`), are read via `GET /api/svc/v1/agents?name=` (list
  entries embed the latest version's manifest in `latestVersionDetails`), and
  deleted via `DELETE /api/svc/v1/agents/{id}`. `terraform import` takes the
  agent name (the list endpoint filters by name, not FQN). The control plane
  rejects agents referencing MCP servers that don't exist on the tenant, and
  enforces the name pattern `^[a-z](?:[a-z0-9]|-(?!-)){1,30}[a-z0-9]$`
  (verified via dryRun 2026-07-27). The legacy `agent` manifest type is not
  modeled.
  **ML repos and their artifacts (agent skills, prompts) follow the same
  split**: `ml-repo` manifests go through `PUT/GET/DELETE /api/svc/v1/ml-repos`
  (in-place update, plain CRUD; `storage_integration_fqn` must reference an
  existing blob-storage integration), while `agent-skill` and `chat_prompt`
  manifests are versioned artifacts like agents (`PUT
  /api/svc/v1/agent-skill-versions` / `prompt-versions`, read via `GET
  /api/svc/v1/agent-skills?name=` / `prompts?name=` with the latest version
  embedded under `latest_version`, deleted by series ID). Beware three quirks
  verified against a real control plane (2026-07-27): these list endpoints use
  **snake_case** envelopes (the agents list is camelCase); the artifact apply
  endpoints **ignore `dryRun` and always persist** (don't probe them with real
  credentials); and the control plane rewrites every applied skill source to a
  managed blob-storage URI, so `truefoundry_agent_skill.Read` preserves the
  state's `source` instead of refreshing it (content drift is undetectable).
  Inline skill sources must start with YAML frontmatter whose `name` matches
  the manifest name. Prompts model only plain-string messages, variables, and
  model_configuration — the advanced chat_prompt blocks are dropped on apply.
- **Access moved into role bindings (control plane v0.172.0, DEVS-1545)**:
  the per-resource `collaborators` (provider accounts incl. custom endpoints
  and guardrail groups, MCP servers, agents, ML repos, secret groups) and the
  virtual account `permissions` are deprecated in favour of standalone
  `role-binding` manifests, and rejected after **15 Nov 2026**. Until then the
  control plane distinguishes three wire states, which is why every manifest
  struct carries `*[]Collaborator` with `omitempty`: field omitted = existing
  grants untouched, `[]` = cleared, non-empty = replaced (`permissions: []`
  is rejected). Before this change six resources sent `collaborators: []`
  whenever the attribute was unset, silently revoking access granted in the
  UI. The attributes stay as deprecated (no breaking release); `Read` leaves
  an unset attribute unset (removing it from config must not resurface the
  untouched grants as drift), keeps the prior value when the response has
  none (control planes may stop echoing the fields), and adopts the remote
  grants only on the first read after an import (flagged via the
  `legacy_grants_import` private state key). `mockcp` carries omitted grants
  over from the stored manifest and `RetireLegacyGrants()` simulates the
  post-cut-off rejection; `TestAccLegacyGrants_retired` guards against any
  resource sending the fields on its own. `truefoundry_role_binding` goes
  through the dedicated endpoints (camelCase `{data}` envelopes, list is
  paginated `{data, pagination}` with an exact `?name=` filter — matched
  client-side anyway), reads by ID, and imports by name. Its shape and the
  one-subject-or-one-permission rule come from the published API reference
  (2026-10-08) and are **not verified against a real control plane yet** —
  `dryRun: true` is documented on its `PUT`, so probe with that.
- **Virtual account tokens are not refreshed by default**: `Read` used to
  call `GetVirtualAccountToken` unconditionally, doubling the request count of
  any plan over many virtual accounts (DEVS-1533, 2026-09-10). Now the token
  is set once on create and left as-is on refresh, unless `auto_rotate` is
  set (control-plane-side periodic rotation always forces a refetch, since
  a rotating token's whole point is for Terraform to detect the change) or
  the token has never been populated yet (a fresh import). Considered a
  manual per-resource opt-in flag as a middle ground but dropped it:
  `auto_rotate` is the only real source of out-of-band rotation, so a
  second toggle on top would be redundant. The `auto_rotate` manifest shape
  (`auto_rotate_interval`/`grace_period`, minimum 30/1 days) is taken from
  the published API docs/SDK, **not verified against a real control
  plane** — Staffbase doesn't use the feature, so there was no live account
  to `dryRun` apply against. Verify before trusting the exact field
  names/bounds if this ever needs to work for real.
- **`truefoundry_slack_account.webhooks[*].webhook_url` is masked on read**:
  `GET /api/svc/v1/provider-integrations?type=notification-channel` returns
  the literal string `"[redacted]"` in `auth_data.webhook_url`, not the
  stored value — verified against the dev control plane (DEVS-1432-adjacent,
  2026-09-15) after it caused a perpetual `~ webhook_url` diff on every plan.
  `Read` now preserves the prior state's `webhook_url` instead of overwriting
  it from the manifest (same tradeoff as `truefoundry_agent_skill`'s
  `source`); a fresh import therefore reads back as `[redacted]` and the
  acceptance test ignores that attribute (`ImportStateVerifyIgnore`).
  `mockcp` reproduces the masking (`maskSlackWebhookURL`) so this stays
  regression-tested. `bot_token` was not probed and may have the same
  behavior — unverified.
- **One `truefoundry_provider_account` resource, five provider types** (`openai`, `azure-openai`, `azure-foundry`, `aws-bedrock`, `virtual-model`) with different required-field combinations — notably azure-foundry has *per-model* auth/endpoints and no account-level auth. Account types whose integrations are not models get their own resource (`custom_endpoint_account`, `azure_account`, `slack_account`, `hashicorp_account`, `system_secret_store`) and are listed with their own `?type=` filter (`blob-storage`, `notification-channel`, `secret-store` — the latter shared by hashicorp and truefoundry accounts). In all resources the validation rules live in exactly two places: `ValidateConfig` in the resource (plan-time, attribute-level errors) and `validateManifest` in `mockcp` (mock server-side). Keep them in sync.
- The control plane verifies integration credentials at apply time for some types (Azure blob storage connection strings, Vault token/AppRole) but not others (Slack webhooks/bots) — `dryRun: true` applies exercise this too.
- `name` and `provider_type` are `RequiresReplace`; everything else updates in place via re-apply (apply is idempotent, `CREATE` vs `UPDATE`).

### Manifest name rules

The control plane validates manifest names with a JSON-schema pattern of the
shape `^[a-z][a-z0-9\-]{1,N}[a-z0-9]$` — a **lowercase letter first**, a letter
or digit last — and only `N` differs per manifest family. Neither the TypeScript
nor the Python SDK carries any length or pattern constraint, so the SDK is no
authority here; the bounds below were read off a live control plane on
2026-09-08, one `dryRun: true` apply per family (binary search on the name
length, then the pattern from the error message). When a manifest fails its own
union branch the control plane reports the *last* branch's error
(`ProviderAccounts : must have required property 'aws_account_id'`) — that
message means "no branch matched", not that the name is fine.

| manifest family | name rule |
| --- | --- |
| `provider-account/{openai,azure-openai,azure-foundry,aws-bedrock,custom-endpoint,truefoundry}` | 3-32 |
| `provider-account/{azure,hashicorp,slack,guardrail-config-group}`, `virtual-account` | 3-36 |
| `provider-account/virtual-model` | 3-64 |
| integration names (nested in an account) | 1-64 |
| `mcp-server/{remote,openapi,virtual}` | 3-73 |
| `truefoundry-agent`, `remote-agent/self-hosted` | 3-32, no consecutive hyphens |
| `role` | 3-35 |
| `role-binding` | 3-66 (from the API reference, not probed) |
| `secret-group` | 5-50, no consecutive hyphens |
| `ml-repo` | 3-100, **upper case allowed** |
| `tenant-budget-config`, `team-budget-config` | 2-62, upper case allowed |
| every `gateway-*-config` / `gateway-global-settings` | **not validated at all** (1 and 300 characters both accepted) |

`provider_account` covers types with two different bounds, so its schema
validator allows the wider 64 and `ValidateConfig` enforces 32 for every type
but `virtual-model`. `agent-skill` and `chat_prompt` names are unprobed: their
apply endpoints ignore `dryRun` and persist, so they must never be probed with a
valid manifest.

### Adding a provider type (e.g. gemini)

Fetch the account/model manifest shapes from the TypeScript SDK serialization files, then touch: `providerTypeValues` + schema attributes + `ValidateConfig` + `manifestFromModel`/`integrationToModel` (resource), wire structs (`client`), `validateManifest` (`mockcp`), an acceptance-test lifecycle + validation cases, and an example.

## Commits

Every commit message follows [Conventional Commits](https://www.conventionalcommits.org/),
scoped with the ticket where one exists:

```
feat(DEVS-1432): add budget limiting v2 resources
chore(deps): bump golang.org/x/net from 0.54.0 to 0.55.0
fix: stop diffing pure reorderings of unordered collections
```

Format: `type(optional-scope)!: subject`, header ≤ 100 characters. Allowed
types: `feat`, `fix`, `chore`, `ci`, `docs`, `refactor`, `test`, `perf`,
`build`, `style`, `revert`. The CI `Commit messages` job checks every
non-merge commit on a pull request and fails on a violation.

The prefix is a convention for readable history only — the release version
and the changelog come from **PR labels** (table below), not from commit
messages. So `feat:` on a commit does not bump the minor version, and a
`!`/`BREAKING CHANGE` footer does nothing on its own: label the PR
`breaking` as well.

## Pull requests

Open PRs as drafts (`gh pr create --draft`) and fill in
[`.github/pull_request_template.md`](.github/pull_request_template.md) — goal,
what changed, how to review, Terraform impact, checklist. Keep the description
under 200 words and never list changed files; GitHub shows those already.

**Every PR needs a label.** Unlike the calver service repos, this provider is
consumed with semver constraints (`~> 0.2`), so
[release-drafter](.github/release-drafter.yml) resolves the next version *and*
the changelog section purely from PR labels — an unlabelled PR silently lands in
no changelog section and only bumps the patch version.

| Label                              | Version bump | Changelog section    |
| ---------------------------------- | ------------ | -------------------- |
| `breaking`, `major`                | major        | 💥 Breaking Changes  |
| `feature`, `enhancement`, `minor`  | minor        | 🚀 Features          |
| `fix`, `bug`                       | patch        | 🐛 Bug Fixes         |
| `documentation`                    | patch        | 📖 Documentation     |
| `chore`, `dependencies`, `patch`   | patch        | 🧰 Maintenance       |
| `skip-changelog`                   | patch        | excluded             |

`major`/`minor`/`patch` are the org-wide labels and behave as aliases for the
semantic ones. The first matching category wins, so a PR labelled both
`breaking` and `feature` lands under 💥 Breaking Changes.

Choosing the label is a judgement about the *practitioner-visible Terraform
surface*, not about the size of the diff:

- `breaking` — renamed/removed attribute or resource, a new required field, a
  changed default, or anything that makes an existing configuration fail or
  forces replacement of existing resources.
- `feature`/`enhancement` — new resource, data source, provider type, or a new
  optional attribute.
- `fix`/`bug` — wrong wire format, drift that is not detected, a crash, a
  validation rule that rejects valid configuration.
- `documentation` — README, AGENTS.md, examples, regenerated `docs/`.
- `chore`/`dependencies` — refactors, tests, CI, dependency bumps.
- `skip-changelog` — nothing a provider consumer would care about.

Cutting the actual release is described in [RELEASING.md](RELEASING.md).

## Documentation workflow

`docs/` is **generated** — never hand-edit it. Source of truth is the schema `Description` fields, the templates in `templates/`, and the snippets in `examples/{provider,resources,data-sources}/` (tfplugindocs' expected layout; `examples/demo/` is not part of docs). After changing either, run `make docs` and commit the regenerated markdown.

### Where prose belongs

- **Schema `Description`** — anything about one resource or attribute. It is
  the only place a practitioner sees inside `terraform providers schema` and on
  the resource doc page.
- **`templates/guides/<topic>.md.tmpl`** → generated to `docs/guides/` — a
  topic that spans several resources and has a start and an end: migrations
  (e.g. [budget limiting v2](templates/guides/budget-limiting-v2.md.tmpl)),
  multi-resource workflows, deprecation walkthroughs. Guides need
  `page_title` / `subcategory` / `description` frontmatter, and links to
  resource pages are relative (`../resources/foo.md`). Keep the README to a
  short summary plus a link to the guide — the guide is versioned with the
  provider and sits next to the resource docs, the README is not.
- **`README.md`** — what the provider is, getting started, best practices,
  import syntax, known limitations. Not migration walkthroughs.
- **There is no `CHANGELOG.md` and no `UPGRADE.md`.** Per-version notes come
  from [release-drafter](.github/release-drafter.yml) via PR labels and live in
  the GitHub release; a hand-maintained changelog would duplicate and drift
  from it. A breaking provider change is explained in its PR description, its
  release note, and — when practitioners have to rewrite configuration — a
  guide.
