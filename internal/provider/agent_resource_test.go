package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

// agentMcpServerConfig is the MCP server the agent under test references;
// the control plane rejects agents referencing unknown MCP servers.
const agentMcpServerConfig = `
resource "truefoundry_mcp_server" "exa" {
  name        = "exa"
  description = "Exa web search"
  url         = "https://mcp.exa.ai/mcp"
}
`

const agentConfig = agentMcpServerConfig + `
resource "truefoundry_agent" "triage" {
  name        = "support-triage"
  description = "Triages inbound support tickets"

  model = {
    name = "azure-main/gpt-5-mini"
    params = {
      max_tokens       = 4096
      reasoning_effort = "medium"
      temperature      = 0.2
    }
  }

  instructions = "Triage the ticket. The product area is {{product}}."

  skills = [
    {
      fqn     = "agent-skill:mock-tenant/ticket-writing"
      preload = true
    },
  ]

  mcp_servers = [
    {
      name                       = truefoundry_mcp_server.exa.name
      preload                    = false
      enable_tools               = ["@all"]
      require_approval_for_tools = ["@write", "@destructive"]
    },
  ]

  variables = {
    product = {
      default_value = "app"
      description   = "Product area of the ticket"
    }
  }

  sample_inputs = [
    {
      text      = "Ticket: login is broken"
      variables = { product = "web" }
    },
  ]

  response_format = {
    type = "json_schema"
    json_schema = {
      name   = "triage-result"
      schema = jsonencode({ type = "object", properties = { severity = { type = "string" } } })
      strict = true
    }
  }

  config = {
    iteration_limit = 50
    sandbox = {
      enabled        = true
      file_downloads = false
    }
    dynamic_sub_agents = { enabled = false }
    context_management = {
      compaction = {
        enabled                     = true
        compaction_threshold_tokens = 50000
      }
      large_tool_response = { enabled = true }
    }
    generative_ui      = { enabled = false }
    ask_user_questions = { enabled = true }
  }

  collaborators = [
    {
      subject = "team:everyone"
      role_id = "agent-access"
    },
  ]

  tags = {
    team = "support"
  }
}
`

func TestAccAgent_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	minimalConfig := providerConfig(host) + `
resource "truefoundry_agent" "triage" {
  name        = "support-triage"
  description = "Answers support questions directly"

  model = {
    name = "azure-main/gpt-5-mini"
  }

  instructions = "Answer the question."
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.AgentCount(); n != 0 {
				return fmt.Errorf("%d agents left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + agentConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_agent.triage", "fqn", mockcp.Tenant+":agent:support-triage"),
					resource.TestCheckResourceAttrSet("truefoundry_agent.triage", "id"),
					resource.TestCheckResourceAttr("truefoundry_agent.triage", "model.name", "azure-main/gpt-5-mini"),
					resource.TestCheckResourceAttr("truefoundry_agent.triage", "model.params.max_tokens", "4096"),
					resource.TestCheckResourceAttr("truefoundry_agent.triage", "skills.0.preload", "true"),
					resource.TestCheckResourceAttr("truefoundry_agent.triage", "mcp_servers.0.name", "exa"),
					resource.TestCheckResourceAttr("truefoundry_agent.triage", "mcp_servers.0.require_approval_for_tools.#", "2"),
					resource.TestCheckResourceAttr("truefoundry_agent.triage", "variables.product.default_value", "app"),
					resource.TestCheckResourceAttr("truefoundry_agent.triage", "response_format.json_schema.name", "triage-result"),
					resource.TestCheckResourceAttr("truefoundry_agent.triage", "config.iteration_limit", "50"),
					resource.TestCheckResourceAttr("truefoundry_agent.triage", "config.context_management.compaction.compaction_threshold_tokens", "50000"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_agent.triage", "collaborators.*", map[string]string{
						"subject": "team:everyone",
						"role_id": "agent-access",
					}),
				),
			},
			// In-place update: minimal manifest without tools, format, or config.
			{
				Config: minimalConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_agent.triage", "description", "Answers support questions directly"),
					resource.TestCheckNoResourceAttr("truefoundry_agent.triage", "mcp_servers"),
					resource.TestCheckNoResourceAttr("truefoundry_agent.triage", "response_format"),
					resource.TestCheckNoResourceAttr("truefoundry_agent.triage", "config"),
				),
			},
			// Out-of-band deletion is detected and the agent is recreated.
			{
				PreConfig: func() { mock.DeleteAgentByName("support-triage") },
				Config:    minimalConfig,
				Check:     resource.TestCheckResourceAttr("truefoundry_agent.triage", "fqn", mockcp.Tenant+":agent:support-triage"),
			},
		},
	})
}

func TestAccAgent_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + agentConfig},
			{
				ResourceName:      "truefoundry_agent.triage",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "support-triage",
			},
		},
	})
}

func TestAccAgent_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "json_schema format without schema block",
			config: `
resource "truefoundry_agent" "bad" {
  name        = "spike-bad"
  description = "bad"
  model       = { name = "azure-main/gpt-5-mini" }
  response_format = {
    type = "json_schema"
  }
}
`,
			want: regexp.MustCompile("`json_schema` is required"),
		},
		{
			name: "text format with schema block",
			config: `
resource "truefoundry_agent" "bad" {
  name        = "spike-bad"
  description = "bad"
  model       = { name = "azure-main/gpt-5-mini" }
  response_format = {
    type = "text"
    json_schema = {
      name = "result"
    }
  }
}
`,
			want: regexp.MustCompile("`json_schema` must not be set"),
		},
		{
			name: "name with double hyphen",
			config: `
resource "truefoundry_agent" "bad" {
  name        = "spike--bad"
  description = "bad"
  model       = { name = "azure-main/gpt-5-mini" }
}
`,
			want: regexp.MustCompile("must be lowercase alphanumeric with single hyphens"),
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

// TestAccAgent_unknownMcpServerRejected exercises the control plane's
// referential check: agents may only reference existing MCP servers.
func TestAccAgent_unknownMcpServerRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + `
resource "truefoundry_agent" "bad" {
  name        = "spike-bad"
  description = "bad"
  model       = { name = "azure-main/gpt-5-mini" }
  mcp_servers = [{ name = "does-not-exist" }]
}
`,
				ExpectError: regexp.MustCompile(`MCP server\(s\) not\s+found`),
			},
		},
	})
}
