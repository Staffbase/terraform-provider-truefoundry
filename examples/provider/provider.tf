terraform {
  required_providers {
    truefoundry = {
      source = "staffbase/truefoundry"
    }
  }
}

# Credentials can also come from the TFY_HOST / TFY_API_KEY environment variables.
provider "truefoundry" {
  host    = "https://my-org.truefoundry.cloud"
  api_key = var.truefoundry_api_key
}
