# A self-hosted A2A agent proxied through the AI Gateway.
resource "truefoundry_remote_agent" "billing" {
  name        = "billing-agent"
  description = "Self-hosted billing agent"
  url         = "https://agents.example.com/billing"

  framework = {
    type            = "a2a"
    agent_card_path = "/.well-known/agent.json"
  }

  auth = {
    type = "header"
    headers = {
      # A TrueFoundry secret reference; raw values work too but end up in state.
      "Authorization" = "my-tenant:hashicorp:vault-secrets:secret-store:vault::agents/billing/token"
    }
  }

  collaborators = [
    {
      subject = "team:everyone"
      role_id = "agent-access"
    },
  ]
}
