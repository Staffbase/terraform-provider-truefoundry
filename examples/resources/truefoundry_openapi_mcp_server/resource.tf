# An OpenAPI-based MCP server: the AI Gateway exposes the operations of an
# OpenAPI spec as MCP tools calling the described API, behind an OAuth2
# (OIDC) identity provider.
resource "truefoundry_openapi_mcp_server" "picklerick" {
  name        = "picklerick"
  description = "Pickle-Rick build API"
  url         = "https://picklerick.example.com/api"

  openapi_spec_source = {
    type = "remote"
    url  = "https://picklerick.example.com/api/openapi.json"
  }

  auth = {
    type              = "oauth2"
    grant_type        = "authorization_code"
    provider          = "custom"
    jwt_source        = "id_token"
    authorization_url = "https://idp.example.com/auth"
    token_url         = "https://idp.example.com/token"
    client_id         = "my-tenant:hashicorp:vault-secrets:secret-store:vault::idp/client_id"
    client_secret     = "my-tenant:hashicorp:vault-secrets:secret-store:vault::idp/client_secret"
    scopes            = ["openid", "email", "groups", "profile", "offline_access"]

    code_challenge_methods_supported = ["S256"]
  }

  tool_settings = [
    {
      method      = "get"
      path        = "/v1/builds/{id}"
      name        = "get_build"
      description = "Fetch one native app build"
      annotations = {
        read_only_hint = true
      }
    },
  ]

  collaborators = [
    {
      subject = "team:everyone"
      role_id = "mcp-server-user"
    },
  ]
}

# A spec shipped with the Terraform configuration; all operations become
# tools.
resource "truefoundry_openapi_mcp_server" "petstore" {
  name        = "petstore"
  description = "Petstore demo API"
  url         = "https://petstore.example.com"

  openapi_spec_source = {
    type         = "inline"
    openapi_spec = file("${path.module}/petstore-openapi.json")
  }

  enable_all_tools = true
}
