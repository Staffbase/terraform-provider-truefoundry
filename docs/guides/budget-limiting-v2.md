---
page_title: "Migrating to budget limiting v2"
subcategory: ""
description: |-
  Port truefoundry_gateway_budget_config rules to truefoundry_tenant_budget_config and truefoundry_team_budget_config before the control plane stops enforcing budget limiting v1 on 15 Aug 2026.
---

# Migrating to budget limiting v2

TrueFoundry deprecated budget limiting v1: the control plane stops enforcing
`gateway-budget-config` rules **after 15 Aug 2026**. Both versions run in
parallel until then, and a request is checked against the matching rules of
both.

[`truefoundry_gateway_budget_config`](../resources/gateway_budget_config.md)
(v1, deprecated) is a tenant-wide singleton holding all rules. Its
replacements are one resource per rule:
[`truefoundry_tenant_budget_config`](../resources/tenant_budget_config.md) and
[`truefoundry_team_budget_config`](../resources/team_budget_config.md) (scoped
to one team, with RBAC on `team_name`).

## Field mapping

| v1 | v2 |
| --- | --- |
| `rules[*].id` | `name` of the resource (letters, digits, hyphens; may contain upper case) |
| `audit_mode = true` | `mode = "audit"` (`enforce` blocks, `audit` only tracks) |
| `limit_to` + `unit` | a `limits` block; several periods can be set at once, and `cost_per_lifetime` is new (it cannot be combined with the resetting periods) |
| `unit = "tokens_per_*"` | no equivalent — v2 is cost-based only |
| `budget_applies_per = ["user"]` | `applies_to = { type = "per-user" }`, also `per-model`, `per-virtual-account`, `metadata`, or `aggregate` |
| per-entity exceptions as extra rules | `applies_to.overrides` |
| `when.subjects = ["user:a@example.com"]` | `when.subjects.users = { in = ["a@example.com"] }`, plus `teams` / `virtual_accounts`, each with `in` / `not_in` |
| `when.models`, `when.metadata` | `when.models`, `when.metadata.<key>`, and the new `when.provider_accounts`, all `in` / `not_in` |
| `alerts.notification_targets` | unchanged, plus `alerts.notify_breaching_user` |

## Behavioral differences

Two differences matter when porting rules:

- **Order no longer decides.** In v1 the first matching rule won; in v2 every
  matching rule is evaluated and the request is blocked if any of them is over
  budget. Re-express a v1 exemption as a `not_in` filter on the default rule or
  as an `applies_to.overrides` entry, not as a rule listed earlier.
- **Counting starts at creation.** A new rule does not retroactively count
  spend from earlier in the current period.

## Porting a rule

A v1 rule inside the singleton:

```hcl
resource "truefoundry_gateway_budget_config" "budget" {
  name = "budget"

  rules = [
    {
      id       = "cluster-daily-cap"
      limit_to = 150
      unit     = "cost_per_day"

      when = {
        metadata = { tfy_gateway_cluster = "my-cluster" }
      }
    },
  ]
}
```

becomes a standalone v2 rule — the flat `limit_to`/`unit` pair turns into a
`limits` block, the implicit tenant-wide scope becomes an explicit
`applies_to.type`, and every `when` filter becomes an `in` / `not_in` set:

```hcl
resource "truefoundry_tenant_budget_config" "cluster_daily_cap" {
  name = "cluster-daily-cap"
  mode = "enforce"

  limits = {
    cost_per_day = 150
  }

  applies_to = {
    type = "aggregate"
  }

  when = {
    metadata = {
      tfy_gateway_cluster = { in = ["my-cluster"] }
    }
  }
}
```

Rules that scope to a single team belong in `truefoundry_team_budget_config`
instead, which adds a required `team_name`.

## Rollout

1. Write the v2 rules with `mode = "audit"` and apply them alongside the v1
   config. Audit rules track spend without blocking requests.
2. Let them run for a full budget period and compare the reported spend with
   the v1 rules.
3. Switch the v2 rules to `mode = "enforce"`.
4. Remove the `truefoundry_gateway_budget_config` resource.

The control plane rejects changes to `applies_to.type` after a rule is
created, so this provider replaces the rule instead; `name` and `team_name`
behave the same way.

## More examples

```terraform
# Budget limiting v2: one resource per rule. Rules are order-independent —
# every matching rule is evaluated and a request is blocked when any of them
# in "enforce" mode is over budget.

# Daily cost cap per gateway cluster, with a Slack alert at 25% usage.
resource "truefoundry_tenant_budget_config" "cluster_daily_cap" {
  name = "cluster-daily-cap"
  mode = "enforce"

  limits = {
    cost_per_day = 150
  }

  applies_to = {
    type = "aggregate"
  }

  when = {
    metadata = {
      tfy_gateway_cluster = { in = ["my-cluster"] }
    }
  }

  alerts = {
    thresholds = [25, 90]
    notification_targets = [
      {
        type                 = "slack-webhook"
        notification_channel = "my-tenant:slack:slack-webhooks:notification-channel:team-ai-foundation"
      },
    ]
  }
}

# Separate budget per user, with a higher cap for two power users, tracked
# but not enforced. The breaching user is notified by email.
resource "truefoundry_tenant_budget_config" "per_user_default" {
  name = "per-user-default"
  mode = "audit"

  limits = {
    cost_per_day   = 5
    cost_per_month = 100
  }

  applies_to = {
    type = "per-user"

    overrides = [
      {
        users = ["ada@example.com", "grace@example.com"]
        limits = {
          cost_per_day   = 50
          cost_per_month = 800
        }
      },
    ]
  }

  when = {
    models            = { in = ["gpt-4o"] }
    provider_accounts = { not_in = ["openai-sandbox"] }
  }

  alerts = {
    thresholds            = [75, 90, 100]
    notify_breaching_user = true
    notification_targets = [
      {
        type                 = "email"
        notification_channel = "my-tenant:email:default:notification-channel:budget-alerts"
        to_emails            = ["{{user.email}}", "ai-platform@example.com"]
      },
    ]
  }
}

# A lifetime cap that never resets, partitioned by a request metadata key.
resource "truefoundry_tenant_budget_config" "per_project" {
  name = "per-project-lifetime"
  mode = "enforce"

  limits = {
    cost_per_lifetime = 2000
  }

  applies_to = {
    type     = "metadata"
    metadata = "project"

    overrides = [
      {
        metadata_values = ["search-relevance"]
        limits          = { cost_per_lifetime = 10000 }
      },
    ]
  }
}
```
