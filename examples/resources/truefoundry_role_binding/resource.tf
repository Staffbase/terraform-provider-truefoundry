# One subject with many permissions: a virtual account that may use two
# provider accounts and an MCP server.
resource "truefoundry_role_binding" "ci_bot" {
  name = "va-ci-bot"

  subjects = [
    { type = "virtualaccount", name = "ci-bot" },
  ]

  permissions = [
    {
      resource_type = "provider-account"
      resource_fqn  = "my-tenant:azure-openai:my-azure-openai"
      role          = "provider-account-access"
    },
    {
      resource_type = "provider-account"
      resource_fqn  = truefoundry_custom_endpoint_account.speech.fqn
      role          = "provider-account-access"
    },
    {
      resource_type = "mcp-server"
      resource_fqn  = truefoundry_mcp_server.deepwiki.fqn
      role          = "mcp-server-user"
    },
  ]
}

# Many subjects with one permission: who manages a guardrail group. To give
# several subjects several roles, use one binding per role.
resource "truefoundry_role_binding" "guardrail_managers" {
  name = "guardrails-francecentral-managers"

  subjects = [
    { type = "user", name = "jane@example.com" },
    { type = "team", name = "ai-platform" },
  ]

  permissions = [
    {
      resource_type = "provider-account"
      resource_fqn  = truefoundry_guardrail_group.region.fqn
      role          = "provider-account-manager"
    },
  ]
}
