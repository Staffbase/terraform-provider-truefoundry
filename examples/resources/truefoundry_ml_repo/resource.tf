# An ML repo is the blob-storage-backed container that agent skills and
# prompts live in.
resource "truefoundry_azure_account" "storage" {
  name = "gateway-storage"

  blob_storages = [
    {
      name              = "artifacts"
      storage_root      = "https://myaccount.blob.core.windows.net/artifacts/tfy-managed-storage"
      connection_string = "tfy-secret://my-tenant:gateway-secrets:artifacts-connection-string"
    },
  ]
}

resource "truefoundry_ml_repo" "gateway_artifacts" {
  name                    = "gateway-artifacts"
  description             = "Skills and prompts of the AI gateway"
  storage_integration_fqn = "${truefoundry_azure_account.storage.fqn}:blob-storage:artifacts"
}
