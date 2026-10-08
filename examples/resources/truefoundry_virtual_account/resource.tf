# A service account for CI. Grant it access with a truefoundry_role_binding
# (see below) rather than the deprecated `permissions` attribute.
resource "truefoundry_virtual_account" "ci" {
  name = "ci-bot"

  expiration_date = "2027-01-31"
  token_type      = "jwt"
  tags            = { env = "production" }

  # Optional: turn on control-plane-side periodic token rotation. Setting
  # this makes every read refetch the token, so a plan over many virtual
  # accounts doesn't double its request count unless it's actually needed.
  # auto_rotate = {
  #   interval_days     = 90
  #   grace_period_days = 15
  # }

  # Optional: sync the token into a secret store instead of consuming
  # the sensitive `token` attribute from state.
  secret_store_config = {
    integration_fqn = "my-tenant:hashicorp:vault-secrets:secret-store:vault"
    secret_path     = "tfy/virtualaccounts/ci-bot/value"
  }
}

output "ci_bot_token" {
  value     = truefoundry_virtual_account.ci.token
  sensitive = true
}

# Access to one provider account.
resource "truefoundry_role_binding" "ci" {
  name = "va-ci-bot"

  subjects = [
    { type = "virtualaccount", name = truefoundry_virtual_account.ci.name },
  ]

  permissions = [
    {
      resource_type = "provider-account"
      resource_fqn  = "my-tenant:azure-openai:my-azure-openai"
      role          = "provider-account-access"
    },
  ]
}
