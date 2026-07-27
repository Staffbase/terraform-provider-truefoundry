# A prompt is a versioned message template in an ML repo, usable via the
# gateway and the TrueFoundry SDKs.
resource "truefoundry_prompt" "ticket_triage" {
  name          = "ticket-triage"
  ml_repo       = truefoundry_ml_repo.gateway_artifacts.name
  description   = "Classifies inbound support tickets"
  version_alias = "v1.0.0"

  messages = [
    {
      role    = "system"
      content = "You triage support tickets for the {{product}} team."
    },
    {
      role    = "user"
      content = "Ticket: {{ticket}}"
    },
  ]

  variables = {
    product = "app"
    ticket  = ""
  }

  model_configuration = {
    provider = "my-openai-account"
    model    = "gpt-5-mini"
    parameters = {
      max_tokens  = 2048
      temperature = 0.1
    }
  }
}
