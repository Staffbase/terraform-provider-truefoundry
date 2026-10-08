# A virtual MCP server aggregating existing MCP servers into one endpoint,
# exposing only a curated subset of their tools.
resource "truefoundry_virtual_mcp_server" "docs" {
  name        = "docs"
  description = "Curated documentation tools"

  servers = [
    {
      name          = truefoundry_mcp_server.deepwiki.name
      enabled_tools = ["ask_question"]
    },
    {
      name = truefoundry_mcp_server.internal_api.name
      auth_headers = {
        "X-Api-Key" = "my-tenant:hashicorp:vault-secrets:secret-store:vault::internal/api_key"
      }
    },
  ]
}
