# Azure account with a blob storage container, connection string in Vault.
resource "truefoundry_azure_account" "storage" {
  name            = "my-blobstorage"
  subscription_id = "00000000-1111-2222-3333-444444444444"

  blob_storages = [
    {
      name              = "assets"
      storage_root      = "https://myaccount.blob.core.windows.net/assets/some/prefix"
      connection_string = "my-tenant:hashicorp:vault-secrets:secret-store:vault::blobstorage/myaccount/value"

      authorized_subjects = ["team:everyone"]
    },
  ]
}
