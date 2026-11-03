# One named policy per target region; policies coexist on the control plane.
# Create the referenced integrations with truefoundry_guardrail_group first.
resource "truefoundry_gateway_guardrails_policy" "region" {
  name = "dev-de1-001-gr-frc"

  when = {
    target = {
      model = {
        condition = "in"
        values    = ["dev-de1-001-francecentral/gpt-5-mini"]
      }
    }
  }

  llm_input_guardrails  = ["dev-de1-001-gr-frc/dev-de1-001-cs-frc"]
  llm_output_guardrails = ["dev-de1-001-gr-frc/dev-de1-001-cs-frc"]
  custom_error_message  = "Request blocked by content safety."

  # Omit unused hook lists rather than assigning []. For MCP targets use
  # mcp_tool_pre_invoke_guardrails and/or mcp_tool_post_invoke_guardrails.
}
