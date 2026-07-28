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
