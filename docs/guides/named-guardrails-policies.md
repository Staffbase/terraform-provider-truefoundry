---
page_title: "Migrating to named guardrail policies"
subcategory: "AI Gateway"
description: |-
  Replace the deprecated folded guardrails config with one named policy per rule.
---

# Migrating to named guardrail policies

Control plane v0.179 replaces the folded `gateway-guardrails-config` singleton
with individually named policies. The folded format is removed on **15 November
2026**. On v0.179+ its singleton GET returns an empty body, so the deprecated
resource stops refresh, import, and apply read-back with a migration error.
This preserves existing state rather than treating the retired endpoint as a
missing resource; only a 404 response means not found. Use
[`truefoundry_gateway_guardrails_policy`](../resources/gateway_guardrails_policy.md)
instead of
[`truefoundry_gateway_guardrails_config`](../resources/gateway_guardrails_config.md).

## Field mapping

| Folded config | Named policy |
| --- | --- |
| One `rules` element | One resource |
| Rule `id` | `name` |
| Rule `when` | `when` (a target is now required) |
| Rule's four guardrail lists | Same top-level attributes |
| Rule `custom_error_message` | `custom_error_message` |
| Singleton `name` / computed `id` | Not transferred |

Every policy needs a `when.target` with at least one of `model`, `mcp_servers`,
or `mcp_tools`. A model target needs an LLM input or output guardrail; an MCP
target needs a pre- or post-invoke guardrail. A policy targeting both needs
both kinds of hooks. Use `*` when you intend to cover all targets. Omit unused
guardrail lists in Terraform; the provider sends the four lists required by
the API, using empty arrays for unused hooks.

## Migration

1. Back up the existing Terraform state and review the policies already
   present in the TrueFoundry UI. The control plane may already have converted
   folded rules into named policies.
2. Replace the folded resource configuration with one named policy resource
   per former rule. Use the rule's `id` as its `name` and transfer the other
   fields according to the table above. Add explicit targets and hooks where
   older rules did not require them.
3. Remove **only the folded resource** from state with `terraform state rm
   truefoundry_gateway_guardrails_config.example`. Do not destroy it as part
   of migration: its generic delete could remove the old configuration while
   you are still migrating. State removal does not delete remote objects.
4. Import each existing named policy by its name, for example:

   ```sh
   terraform import truefoundry_gateway_guardrails_policy.region dev-de1-001-gr-frc
   ```

5. Review `terraform plan`. Existing policies should need no changes except
   the intentional target/hook changes above. Create policies that do not
   already exist and verify matching requests before retiring any remaining
   folded configuration through the supported control plane workflow.

A state `moved` block cannot split a singleton's rules into independent
resources; use state removal and imports rather than moving its computed ID.
Review the [v0.179 announcement](https://www.truefoundry.com/docs/change-announcements/named-guardrails-config-v0.179.md)
and [guardrails configuration documentation](https://www.truefoundry.com/docs/ai-gateway/guardrails-configuration).
