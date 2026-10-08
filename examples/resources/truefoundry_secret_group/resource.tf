# The secret store the group is backed by. A truefoundry_hashicorp_account
# works the same way.
resource "truefoundry_system_secret_store" "system" {
  name = "system-secret-store"

  secret_stores = [
    { name = "system-secret-store" },
  ]
}

# The group itself. Secrets inside it are created out of band — through the
# TrueFoundry UI or API — which keeps secret material out of the Terraform
# state. Terraform manages who can reach the group, not what is in it.
resource "truefoundry_secret_group" "system" {
  name            = "system"
  integration_fqn = "${truefoundry_system_secret_store.system.fqn}:secret-store:system-secret-store"

  # Destroying or renaming the group deletes every secret in it, and Terraform
  # cannot recreate them. Guard groups that back live credentials.
  lifecycle {
    prevent_destroy = true
  }
}

# Secrets in the group are referenced from any credential attribute by their
# FQN, <tenant>:<group>:<secret>:
#
#   resource "truefoundry_provider_account" "openai" {
#     # ...
#     api_key = "tfy-secret://${truefoundry_secret_group.system.fqn}:openai-api-key"
#   }
