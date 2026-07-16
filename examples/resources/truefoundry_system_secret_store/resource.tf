# The built-in DB-backed secret store: secrets live in the control plane's
# own database and back references like tfy-secret://my-tenant:system-secret-store:secret-name.
resource "truefoundry_system_secret_store" "system" {
  name = "system-secret-store"

  secret_stores = [
    {
      name                = "system-secret-store"
      authorized_subjects = ["team:everyone"]
    },
  ]
}
