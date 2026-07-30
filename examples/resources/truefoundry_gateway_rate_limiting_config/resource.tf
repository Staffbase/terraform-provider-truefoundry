# The tenant-wide AI gateway rate limiting config (singleton — define at most
# one): how many requests or tokens matching requests may spend per period.
resource "truefoundry_gateway_rate_limiting_config" "limits" {
  name = "rate-limiting-config"

  rules = [
    # 60 requests per minute per user, on the models of one region.
    {
      id = "region-per-user"

      when = {
        models = ["my-azure-openai/gpt-4o", "my-azure-openai/gpt-4.1"]
      }

      limit_to = 60
      unit     = "requests_per_minute"

      # Buckets the counter per entity. Note these values differ from the
      # per-user/per-model spelling used by the budget resources.
      rate_limit_applies_per = ["user"]
    },

    # One shared daily token budget for everything a team sends.
    {
      id = "team-token-cap"

      when = {
        subjects = ["team:everyone"]
      }

      limit_to = 1000000
      unit     = "tokens_per_day"
    },

    # A separate hourly cap per app, keyed by a metadata value from the
    # X-TFY-METADATA header. At most two partitionings per rule.
    {
      id = "per-app-hourly"

      when = {
        metadata = { environment = "production" }
      }

      limit_to               = 5000
      unit                   = "requests_per_hour"
      rate_limit_applies_per = ["model", "metadata.appId"]
    },
  ]
}
