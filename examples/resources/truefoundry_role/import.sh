# Roles are imported by their name.
terraform import truefoundry_role.gateway_reader gateway-reader

# TrueFoundry-provided default roles (is_default = true, e.g. `member`) exist
# before Terraform does. Import them rather than creating them, so the first
# plan shows the real diff against their current permissions.
terraform import truefoundry_role.member member

# Roles TrueFoundry reserves (is_editable = false, e.g. `tenant-admin` or
# `secret-group-viewer`) cannot be imported at all — reference them by name
# from a `role_id` attribute instead.
