# An agent skill is a versioned SKILL.md artifact in an ML repo that agents
# reference by FQN. The frontmatter name must match the skill name.
resource "truefoundry_agent_skill" "ticket_writing" {
  name    = "ticket-writing"
  ml_repo = truefoundry_ml_repo.gateway_artifacts.name

  source = {
    type     = "inline"
    skill_md = <<-EOT
      ---
      name: ticket-writing
      description: Write actionable support tickets
      ---
      # Ticket writing

      Summarize the problem in one sentence, then list reproduction steps.
    EOT
  }
}

# Attach a specific skill version to an agent by its versioned FQN.
resource "truefoundry_agent" "support_triage" {
  name        = "support-triage"
  description = "Triages inbound support tickets"

  model = {
    name = "my-openai-account/gpt-5-mini"
  }

  skills = [
    {
      fqn     = "${truefoundry_agent_skill.ticket_writing.fqn}:1"
      preload = true
    },
  ]
}
