# A single endpoint proxied at /proxy-api/speech/transcribe.
resource "truefoundry_custom_endpoint_account" "speech" {
  name          = "speech"
  endpoint_type = "azure-speech-service"

  endpoints = [
    {
      name     = "transcribe"
      base_url = "https://my-speech.cognitiveservices.azure.com"

      # Secret references are recommended; raw values also work.
      auth_headers = {
        "Ocp-Apim-Subscription-Key" = "my-tenant:hashicorp:vault-secrets:secret-store:vault::tfy/speech/key"
      }
    },
  ]
}

# Two endpoints load balanced by weight, reachable together at
# /proxy-api/internal-llm/main and individually by endpoint name.
resource "truefoundry_custom_endpoint_account" "internal_llm" {
  name         = "internal-llm"
  routing_type = "weight"
  slug         = "main"

  endpoints = [
    {
      name     = "eu"
      base_url = "https://llm-eu.internal.example.com/v1"
      loadbalancing = {
        weight = 80
      }
    },
    {
      name     = "us"
      base_url = "https://llm-us.internal.example.com/v1"
      loadbalancing = {
        weight                = 20
        fallback_candidate    = true
        fallback_status_codes = ["429", "500", "503"]
      }
    },
  ]
}
