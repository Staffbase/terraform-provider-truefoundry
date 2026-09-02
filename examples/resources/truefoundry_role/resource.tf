# A custom tenant-scoped role granting read-only access to the AI gateway.
resource "truefoundry_role" "gateway_reader" {
  name          = "gateway-reader"
  display_name  = "Gateway Reader"
  description   = "Read provider accounts, models and MCP servers, use nothing."
  resource_type = "tenant"

  permissions = [
    "provider-account:ReadProviderAccount",
    "mcp-server:ReadMcpServer",
    "user:ListUsers",
  ]
}

# A team-scoped role. Team roles are assignable within a team rather than
# across the whole tenant.
resource "truefoundry_role" "team_gateway_user" {
  name          = "team-gateway-user"
  display_name  = "Team Gateway User"
  resource_type = "team"

  permissions = [
    "provider-account:UseIntegrations",
    "provider-integration:UseIntegration",
    "mcp-server:UseMcpServer",
  ]
}

# The `member` role every tenant ships with. TrueFoundry creates it at
# onboarding, so import it before the first apply — see import.sh — and note
# that `terraform destroy` only drops it from state, leaving the control plane
# role with the permissions of the last apply.
resource "truefoundry_role" "member" {
  name          = "member"
  display_name  = "Member"
  description   = "Role grants member access to the entities in the account."
  resource_type = "tenant"

  permissions = [
    "cluster:ReadCluster",
    "environment:ListEnvironments",
    "user:ListUsers",
    "team:ReadTeam",
    "provider-account:UseIntegrations",
  ]
}
