# Secrets are imported by their FQN, <tenant>:<group>:<key>. If the control
# plane hides secret values, the value is unknown after import and the next
# apply writes the configured value as a new secret version.
terraform import truefoundry_secret.vault_role_id my-tenant:system:vault-role-id
