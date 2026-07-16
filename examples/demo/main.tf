terraform {
  required_providers {
    truefoundry = {
      source = "terraform.staffbase.com/staffbase/truefoundry"
    }
  }
}

# host/api_key can also come from TFY_HOST / TFY_API_KEY.
provider "truefoundry" {
  host    = "http://127.0.0.1:8330"
  api_key = "local-token"
}

resource "truefoundry_provider_account" "openai" {
  name    = "demo-openai"
  api_key = "sk-demo-upstream-key"

  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat", "responses"]
    },
    {
      name        = "text-embedding-3-small"
      model_id    = "text-embedding-3-small"
      model_types = ["embedding"]
    },
  ]
}

# Azure OpenAI: auth + endpoint on the account, deployments as models.
resource "truefoundry_provider_account" "azure_openai" {
  name           = "demo-azure-openai"
  provider_type  = "azure-openai"
  api_key        = "azure-openai-account-key"
  azure_endpoint = "https://demo.openai.azure.com"

  models = [
    {
      name             = "gpt-4o"
      model_id         = "gpt-4o-deployment" # Azure deployment name, not the base model
      api_version      = "2024-10-21"
      foundation_model = "gpt-4o-2024-11-20"
      deployment_type  = "datazone_eu"
      model_types      = ["chat", "responses"]
    },
  ]
}

# Azure AI Foundry: no account-level auth; endpoint + key per model deployment.
resource "truefoundry_provider_account" "azure_foundry" {
  name          = "demo-azure-foundry"
  provider_type = "azure-foundry"

  models = [
    {
      name           = "phi-4"
      model_id       = "phi-4-deployment"
      azure_endpoint = "https://phi-4.eastus.models.ai.azure.com"
      api_key        = "foundry-deployment-key"
      api_version    = "2024-05-01-preview"
      model_types    = ["chat"]
    },
  ]
}

data "truefoundry_models" "all" {
  depends_on = [
    truefoundry_provider_account.openai,
    truefoundry_provider_account.azure_openai,
    truefoundry_provider_account.azure_foundry,
  ]
}

output "provider_account_fqn" {
  value = truefoundry_provider_account.openai.fqn
}

output "gateway_models" {
  value = [for m in data.truefoundry_models.all.models : m.fqn]
}
