resource "truefoundry_system_secret_store" "system" {
  name = "system-secret-store"

  secret_stores = [
    { name = "system-secret-store" },
  ]
}

resource "truefoundry_secret_group" "system" {
  name            = "system"
  integration_fqn = "${truefoundry_system_secret_store.system.fqn}:secret-store:system-secret-store"
}

# A Vault AppRole created by the vault provider, handed to the control plane
# without leaving Terraform. The values are stored in the Terraform state.
resource "truefoundry_secret" "vault_role_id" {
  secret_group_id = truefoundry_secret_group.system.id
  key             = "vault-role-id"
  value           = vault_approle_auth_backend_role.truefoundry.role_id
}

resource "truefoundry_secret" "vault_secret_id" {
  secret_group_id = truefoundry_secret_group.system.id
  key             = "vault-secret-id"
  value           = vault_approle_auth_backend_role_secret_id.truefoundry.secret_id
}

# Referenced from credential attributes as tfy-secret://<fqn>.
resource "truefoundry_hashicorp_account" "vault" {
  name = "vault"

  vaults = [
    {
      name          = "vault"
      vault_url     = "https://vault.example.com"
      kv_mount_path = "secret"
      approle = {
        role_id   = "tfy-secret://${truefoundry_secret.vault_role_id.fqn}"
        secret_id = "tfy-secret://${truefoundry_secret.vault_secret_id.fqn}"
      }
    },
  ]
}
