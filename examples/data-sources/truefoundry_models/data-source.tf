# All models on the gateway.
data "truefoundry_models" "all" {}

# Only the models of one provider account.
data "truefoundry_models" "azure_openai" {
  provider_account_fqn = truefoundry_provider_account.azure_openai.fqn
}

output "gateway_model_fqns" {
  value = [for m in data.truefoundry_models.all.models : m.fqn]
}
