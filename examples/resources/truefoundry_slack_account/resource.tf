# Slack notification channels: an incoming webhook and a bot token,
# both stored as TrueFoundry secret references.
resource "truefoundry_slack_account" "notifications" {
  name = "slack-webhooks"

  webhooks = [
    {
      name        = "team-ai-foundation"
      webhook_url = "my-tenant:hashicorp:vault-secrets:secret-store:vault::slack/team-ai-foundation/value"
    },
  ]

  bots = [
    {
      name                = "alerts-bot"
      bot_token           = "my-tenant:hashicorp:vault-secrets:secret-store:vault::slack/alerts-bot/value"
      authorized_subjects = ["team:everyone"]
    },
  ]
}
