# A TrueFoundry-hosted agent using a gateway model, an MCP server, and
# structured output.
resource "truefoundry_agent" "support_triage" {
  name        = "support-triage"
  description = "Triages inbound support tickets and suggests a severity"

  model = {
    name = "azure-main/gpt-5-mini"
    params = {
      max_tokens       = 4096
      reasoning_effort = "medium"
    }
  }

  instructions = "Triage the ticket. The product area is {{product}}."

  variables = {
    product = {
      default_value = "app"
      description   = "Product area of the ticket"
    }
  }

  mcp_servers = [
    {
      name                       = truefoundry_mcp_server.wiki.name
      enable_tools               = ["@all"]
      require_approval_for_tools = ["@write", "@destructive"]
    },
  ]

  response_format = {
    type = "json_schema"
    json_schema = {
      name = "triage-result"
      schema = jsonencode({
        type = "object"
        properties = {
          severity = { type = "string" }
          summary  = { type = "string" }
        }
      })
      strict = true
    }
  }

  config = {
    iteration_limit    = 50
    ask_user_questions = { enabled = true }
    context_management = {
      compaction = {
        enabled                     = true
        compaction_threshold_tokens = 50000
      }
    }
  }

  collaborators = [
    {
      subject = "team:everyone"
      role_id = "agent-access"
    },
  ]

  tags = {
    team = "support"
  }
}
