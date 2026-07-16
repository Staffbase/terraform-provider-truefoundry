# The tenant-wide AI gateway guardrails config (singleton — define at most one):
# which guardrails run on which requests.
resource "truefoundry_gateway_guardrails_config" "guardrails" {
  name = "guardrails-config"

  rules = [
    # Content safety on both directions for the models of one region.
    {
      id = "region-content-safety"

      when = {
        target = {
          model = {
            condition = "in"
            values    = ["my-azure-openai/gpt-4o", "my-azure-openai/gpt-4.1"]
          }
        }
      }

      # References are <guardrail-group>/<guardrail-config> names,
      # e.g. from a truefoundry_guardrail_group.
      llm_input_guardrails  = ["region-guardrails/content-safety"]
      llm_output_guardrails = ["region-guardrails/content-safety"]
    },

    # Prompt shield on the input of everything a team sends.
    {
      id = "team-prompt-shield"

      when = {
        subjects = {
          operator = "or"
          in       = ["team:everyone"]
        }
      }

      llm_input_guardrails = ["region-guardrails/prompt-shield"]
      custom_error_message = "Request blocked by prompt shield."
    },
  ]
}
