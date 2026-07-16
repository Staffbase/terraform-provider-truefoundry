# The tenant-wide AI gateway data routing config (singleton — define at most one):
# where the gateway's observability data (traces, metrics) is stored.
resource "truefoundry_gateway_data_routing_config" "routing" {
  name = "datarouting"

  # Everything not matched below goes to control-plane-managed storage.
  default = {
    storage = {
      type = "controlplane-managed"
    }
  }

  destinations = [
    # Data of one gateway cluster lands in our own blob storage.
    {
      name    = "my-cluster"
      enabled = true

      when = [
        {
          field  = "metadata"
          key    = "tfy_gateway_cluster"
          values = ["my-cluster"]
        },
      ]

      storage = {
        type                    = "customer-managed"
        storage_integration_fqn = "my-tenant:azure:my-blobstorage:blob-storage:my-container"
      }

      traces_retention_period_days = 30
    },
  ]
}
