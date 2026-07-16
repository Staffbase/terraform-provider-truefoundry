# The tenant-wide AI gateway data access config (singleton — define at most one).
resource "truefoundry_gateway_data_access_config" "access" {
  name = "data-access"

  rules = [
    # Everyone sees their own requests.
    {
      id         = "everyone-own-data"
      scope      = "own_data"
      subjects   = ["team:everyone"]
      data_types = ["traces", "metrics"]
    },

    # Team members see their team's requests.
    {
      id         = "everyone-team-data"
      scope      = "team_data"
      subjects   = ["team:everyone"]
      data_types = ["traces", "metrics"]
    },

    # Tenant admins see everything.
    {
      id         = "tenant-admin-all-data"
      scope      = "all_data"
      subjects   = ["role:tenant-admin"]
      data_types = ["traces", "metrics"]
    },

    # The platform team sees all traces of one gateway cluster.
    {
      id         = "platform-cluster-traces"
      scope      = "all_data"
      subjects   = ["team:platform"]
      data_types = ["traces"]

      filters = [
        {
          field  = "metadata"
          key    = "tfy_gateway_cluster"
          values = ["my-cluster"]
        },
      ]
    },
  ]
}
