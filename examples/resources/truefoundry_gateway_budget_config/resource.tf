# The tenant-wide AI gateway budget config (singleton — define at most one).
resource "truefoundry_gateway_budget_config" "budget" {
  name = "budget"

  rules = [
    # Daily cost cap per gateway cluster with a Slack alert at 25% usage.
    {
      id       = "cluster-daily-cap"
      limit_to = 150
      unit     = "cost_per_day"

      when = {
        metadata = { tfy_gateway_cluster = "my-cluster" }
      }

      alerts = {
        thresholds = [25]
        notification_targets = [
          {
            type                 = "slack-webhook"
            notification_channel = "my-tenant:slack:slack-webhooks:notification-channel:team-ai-foundation"
          },
        ]
      }
    },

    # Per-user monthly token budget for one model, audit only.
    {
      id       = "per-user-tokens"
      limit_to = 1000000
      unit     = "tokens_per_month"

      when = {
        models = ["my-tenant:azure-openai:eu:model:gpt-4o"]
      }

      budget_applies_per = ["user"]
      audit_mode         = true
    },
  ]
}
