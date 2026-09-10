# A service account for CI with access to one provider account.
resource "truefoundry_virtual_account" "ci" {
  name = "ci-bot"

  permissions = [
    {
      resource_fqn  = "my-tenant:azure-openai:my-azure-openai"
      resource_type = "provider-account"
      role_id       = "provider-account-access"
    },
  ]

  expiration_date = "2027-01-31"
  token_type      = "jwt"
  tags            = { env = "production" }

  # Only set this for accounts whose token rotation must be caught by
  # Terraform: it doubles the request count of every plan that reads this
  # account (defaults to false). Has no effect when auto_rotate is set below.
  refresh_token = false

  # Optional: turn on control-plane-side periodic token rotation. When set,
  # every read refetches the token regardless of refresh_token.
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
