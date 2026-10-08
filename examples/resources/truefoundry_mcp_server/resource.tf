# A remote MCP server proxied by the AI Gateway, authenticating with a
# static header stored as a TrueFoundry secret reference.
resource "truefoundry_mcp_server" "deepwiki" {
  name        = "deepwiki"
  description = "AI-powered documentation for GitHub repositories"
  url         = "https://mcp.deepwiki.com/mcp"

  auth = {
    type       = "header"
    auth_level = "global"
    headers = {
      "Authorization" = "my-tenant:hashicorp:vault-secrets:secret-store:vault::mcp/deepwiki/token"
    }
  }

  tool_settings = [
    {
      name        = "ask_question"
      description = "Ask any question about a GitHub repository"
      annotations = {
        read_only_hint = true
      }
    },
  ]
}

# A remote MCP server behind an OAuth2 (OIDC) identity provider; each
# gateway user authorizes individually via the authorization_code flow.
resource "truefoundry_mcp_server" "internal_api" {
  name        = "internal-api"
  description = "Internal API tools"
  url         = "https://internal.example.com/mcp"

  auth = {
    type              = "oauth2"
    grant_type        = "authorization_code"
    provider          = "custom"
    jwt_source        = "id_token"
    authorization_url = "https://idp.example.com/auth"
    token_url         = "https://idp.example.com/token"
    client_id         = "my-tenant:hashicorp:vault-secrets:secret-store:vault::idp/client_id"
    client_secret     = "my-tenant:hashicorp:vault-secrets:secret-store:vault::idp/client_secret"
    scopes            = ["openid", "email", "profile"]

    code_challenge_methods_supported = ["S256"]
  }
}
