# The tenant-wide AI gateway load balancing config (singleton — define at most
# one): which target models matching requests are spread over, and how.
#
# Unlike the `routing` block of a virtual model, these rules route requests
# that address a real model, without a virtual model in front of it.
resource "truefoundry_gateway_load_balancing_config" "routing" {
  name = "load-balancing-config"

  rules = [
    # 70/30 split of one model across two regions, with the second region also
    # taking over when the first rate-limits.
    {
      id   = "gpt-4o-regions"
      type = "weight-based-routing"

      when = {
        models = ["my-azure-openai-francecentral/gpt-4o"]
      }

      load_balance_targets = [
        {
          target = "my-azure-openai-francecentral/gpt-4o"
          weight = 70
        },
        {
          target                = "my-azure-openai-swedencentral/gpt-4o"
          weight                = 30
          fallback_status_codes = ["429", "503"]
          fallback_candidate    = true
        },
      ]
    },

    # An ordered fallback chain for one team: priority 0 is tried first.
    {
      id   = "team-fallback-chain"
      type = "priority-based-routing"

      when = {
        subjects = ["team:everyone"]
      }

      load_balance_targets = [
        { target = "my-azure-openai-francecentral/gpt-4.1", priority = 0 },
        { target = "my-azure-openai-swedencentral/gpt-4.1", priority = 1 },
      ]
    },

    # Everything else goes to whichever target is currently fastest.
    {
      id   = "fastest-wins"
      type = "latency-based-routing"
      when = {}

      load_balance_targets = [
        {
          target = "my-azure-openai-francecentral/gpt-4o-mini"

          headers_override = {
            set = { "x-routed-by" = "terraform" }
          }
        },
        { target = "my-azure-openai-swedencentral/gpt-4o-mini" },
      ]
    },
  ]
}
