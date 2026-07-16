# OpenAI: account-level API key.
resource "truefoundry_provider_account" "openai" {
  name    = "demo-openai"
  api_key = var.openai_api_key

  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat", "responses"]
    },
  ]
}

# Azure OpenAI: auth + endpoint on the account, deployments as models.
resource "truefoundry_provider_account" "azure_openai" {
  name           = "demo-azure-openai"
  provider_type  = "azure-openai"
  api_key        = var.azure_openai_api_key
  azure_endpoint = "https://demo.openai.azure.com"

  models = [
    {
      name             = "gpt-4o"
      model_id         = "gpt-4o-deployment" # Azure deployment name, not the base model
      api_version      = "2024-10-21"
      foundation_model = "gpt-4o-2024-11-20"
      deployment_type  = "datazone_eu"
      region           = "francecentral"
      model_types      = ["chat", "responses"]

      # Optional: pricing for gateway cost tracking.
      cost = {
        metric = "per_1000_tokens"
        input  = 0.0022
        output = 0.0088
      }
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
      api_key        = var.foundry_deployment_key
      api_version    = "2024-05-01-preview"
      model_types    = ["chat"]
    },
  ]
}

# AWS Bedrock: account-level region, access-key auth via secret references.
resource "truefoundry_provider_account" "bedrock" {
  name              = "demo-bedrock"
  provider_type     = "aws-bedrock"
  region            = "eu-central-1"
  access_key_id     = var.bedrock_access_key_id
  secret_access_key = var.bedrock_secret_access_key

  models = [
    {
      name        = "cohere-rerank-v3-5"
      model_id    = "cohere.rerank-v3-5:0"
      model_types = ["rerank"]
    },
  ]
}

# Virtual models: one gateway model name routed across real models.
resource "truefoundry_provider_account" "virtual" {
  name          = "demo-virtual"
  provider_type = "virtual-model"

  models = [
    {
      name        = "gpt-4o"
      model_types = ["chat", "responses"]
      routing = {
        type = "weight-based-routing"
        targets = [
          {
            target = "demo-azure-openai/gpt-4o"
            weight = 3
          },
          {
            target = "demo-openai/gpt-4o"
            weight = 1
          },
        ]
      }
    },
    {
      name        = "gpt-4o-failover"
      model_types = ["chat"]
      routing = {
        type = "priority-based-routing"
        targets = [
          {
            target                = "demo-azure-openai/gpt-4o"
            priority              = 0
            fallback_status_codes = ["429", "500"]
          },
          {
            target   = "demo-openai/gpt-4o"
            priority = 1
          },
        ]
      }
    },
  ]
}
