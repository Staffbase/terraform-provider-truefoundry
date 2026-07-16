# Vault KV secret store with AppRole auth. Secret stores back TrueFoundry
# secret references like my-tenant:hashicorp:vault-secrets:secret-store:vault::path/value.
resource "truefoundry_hashicorp_account" "secrets" {
  name = "vault-secrets"

  vaults = [
    {
      name          = "vault"
      vault_url     = "https://vault.example.com"
      kv_mount_path = "de1/dev"
      root_path     = "tfy-control-plane"

      approle = {
        role_id   = "tfy-secret://my-tenant:system:vault-role-id"
        secret_id = "tfy-secret://my-tenant:system:vault-secret-id"
      }
    },
  ]
}
