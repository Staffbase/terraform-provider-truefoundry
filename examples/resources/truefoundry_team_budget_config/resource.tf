# A budget owned by one team. Team managers and tenant admins can manage it;
# it applies on top of any tenant budget that also matches the request.
resource "truefoundry_team_budget_config" "platform_per_user" {
  name      = "platform-per-user"
  team_name = "platform"
  mode      = "enforce"

  limits = {
    cost_per_day   = 1
    cost_per_month = 20
  }

  applies_to = {
    type = "per-user"

    overrides = [
      {
        users  = ["ada@example.com"]
        limits = { cost_per_day = 5 }
      },
    ]
  }

  when = {
    models = { in = ["gpt-4o"] }
  }
}
