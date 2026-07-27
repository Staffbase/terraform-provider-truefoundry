package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

const agentSkillConfig = mlRepoConfig + `
resource "truefoundry_agent_skill" "writing" {
  name    = "ticket-writing"
  ml_repo = truefoundry_ml_repo.artifacts.name

  source = {
    type     = "inline"
    skill_md = <<-EOT
      ---
      name: ticket-writing
      description: Write actionable support tickets
      ---
      # Ticket writing

      Summarize the problem in one sentence.
    EOT
  }
}
`

func TestAccAgentSkill_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.AgentSkillCount(); n != 0 {
				return fmt.Errorf("%d agent skills left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + agentSkillConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_agent_skill.writing", "id"),
					resource.TestCheckResourceAttr("truefoundry_agent_skill.writing", "fqn",
						"agent-skill:"+mockcp.Tenant+"/gateway-artifacts/ticket-writing"),
					resource.TestCheckResourceAttr("truefoundry_agent_skill.writing", "source.type", "inline"),
					// The control plane rewrites the stored source to managed
					// blob storage; the configured inline source must survive
					// the refresh untouched.
					resource.TestCheckResourceAttrWith("truefoundry_agent_skill.writing", "source.skill_md", func(v string) error {
						if !regexp.MustCompile(`(?s)^---\nname: ticket-writing\n.*one sentence`).MatchString(v) {
							return fmt.Errorf("skill_md not preserved as configured: %q", v)
						}
						return nil
					}),
				),
			},
			// In-place update: switch to a blob-storage source (a new version).
			{
				Config: providerConfig(host) + mlRepoConfig + `
resource "truefoundry_agent_skill" "writing" {
  name    = "ticket-writing"
  ml_repo = truefoundry_ml_repo.artifacts.name

  source = {
    type        = "blob-storage"
    uri         = "wasbs://artifacts@artifacts.blob.core.windows.net/skills/ticket-writing/"
    description = "Write actionable support tickets"
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_agent_skill.writing", "source.type", "blob-storage"),
					resource.TestCheckResourceAttr("truefoundry_agent_skill.writing", "source.uri",
						"wasbs://artifacts@artifacts.blob.core.windows.net/skills/ticket-writing/"),
					resource.TestCheckNoResourceAttr("truefoundry_agent_skill.writing", "source.skill_md"),
				),
			},
			// Out-of-band deletion is detected and the skill is recreated.
			{
				PreConfig: func() { mock.DeleteAgentSkillByName("ticket-writing") },
				Config:    providerConfig(host) + agentSkillConfig,
				Check: resource.TestCheckResourceAttr("truefoundry_agent_skill.writing", "fqn",
					"agent-skill:"+mockcp.Tenant+"/gateway-artifacts/ticket-writing"),
			},
		},
	})
}

func TestAccAgentSkill_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + agentSkillConfig},
			{
				ResourceName:      "truefoundry_agent_skill.writing",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "ticket-writing",
				// The stored source is the control-plane-managed blob-storage
				// rewrite, not the configured inline source.
				ImportStateVerifyIgnore: []string{"source"},
			},
		},
	})
}

func TestAccAgentSkill_validation(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "inline without frontmatter",
			config: `
resource "truefoundry_agent_skill" "bad" {
  name    = "spike-bad"
  ml_repo = "gateway-artifacts"
  source = {
    type     = "inline"
    skill_md = "# No frontmatter"
  }
}
`,
			want: regexp.MustCompile("must start with YAML frontmatter"),
		},
		{
			name: "frontmatter name mismatch",
			config: `
resource "truefoundry_agent_skill" "bad" {
  name    = "spike-bad"
  ml_repo = "gateway-artifacts"
  source = {
    type     = "inline"
    skill_md = "---\nname: other-name\ndescription: x\n---\n# Body"
  }
}
`,
			want: regexp.MustCompile(`frontmatter name "other-name" must match`),
		},
		{
			name: "inline with blob-storage fields",
			config: `
resource "truefoundry_agent_skill" "bad" {
  name    = "spike-bad"
  ml_repo = "gateway-artifacts"
  source = {
    type     = "inline"
    skill_md = "---\nname: spike-bad\n---\n# Body"
    uri      = "wasbs://x@y/z"
  }
}
`,
			want: regexp.MustCompile("`uri` must not be set"),
		},
		{
			name: "blob-storage without description",
			config: `
resource "truefoundry_agent_skill" "bad" {
  name    = "spike-bad"
  ml_repo = "gateway-artifacts"
  source = {
    type = "blob-storage"
    uri  = "wasbs://x@y/z"
  }
}
`,
			want: regexp.MustCompile("`description` is required"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6Factories,
				Steps: []resource.TestStep{
					{Config: providerConfig(host) + tc.config, ExpectError: tc.want},
				},
			})
		})
	}
}

// TestAccAgentSkill_unknownMlRepoRejected exercises the control plane's
// referential check: skills may only live in existing ML repos.
func TestAccAgentSkill_unknownMlRepoRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + `
resource "truefoundry_agent_skill" "bad" {
  name    = "spike-bad"
  ml_repo = "does-not-exist"
  source = {
    type     = "inline"
    skill_md = "---\nname: spike-bad\ndescription: x\n---\n# Body"
  }
}
`,
				ExpectError: regexp.MustCompile(`(?s)ML Repo\s+\\"does-not-exist\\" not\s+found`),
			},
		},
	})
}
