# A self-hosted gateway plane, registered on the control plane with the URL it
# serves.
resource "truefoundry_gateway_installation" "gateway_eu" {
  name = "gateway-eu"
  url  = "https://gateway.example.com"
}

# The gateway plane authenticates with the token as TFY_API_KEY. Hand it over
# through a secret store rather than reading it from the state.
resource "vault_kv_secret_v2" "gateway_token" {
  mount = "secret"
  name  = "tfy-llm-gateway/gateway-eu"
  data_json = jsonencode({
    TFY_API_KEY = truefoundry_gateway_installation.gateway_eu.token
  })
}
