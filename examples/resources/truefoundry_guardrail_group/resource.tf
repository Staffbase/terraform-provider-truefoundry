# A guardrail group with Azure Prompt Shield and Content Safety,
# keys referenced from Vault.
resource "truefoundry_guardrail_group" "region" {
  name = "guardrails-francecentral"

  collaborators = [
    {
      subject = "user:jane@example.com"
      role_id = "provider-account-manager"
    },
  ]

  guardrails = [
    {
      type               = "azure-prompt-shield"
      name               = "ps-francecentral"
      description        = "Jailbreak and prompt injection detection"
      api_key            = "my-tenant:hashicorp:vault-secrets:secret-store:vault::contentsafety/francecentral/value"
      enforcing_strategy = "enforce_but_ignore_on_error"
      resource_name      = "cog-guardrails-francecentral"
      api_version        = "2024-09-01"
    },
    {
      type               = "azure-content-safety"
      name               = "cs-francecentral"
      description        = "Hate, self-harm, sexual, and violence detection"
      api_key            = "my-tenant:hashicorp:vault-secrets:secret-store:vault::contentsafety/francecentral/value"
      enforcing_strategy = "enforce_but_ignore_on_error"
      resource_name      = "cog-guardrails-francecentral"
      api_version        = "2024-09-01"
      severity_threshold = 4
      categories         = ["Hate", "SelfHarm", "Sexual", "Violence"]
    },
  ]
}
