package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Mirrors the live dev setup: content-safety guardrails on the models of one
// region, matched by model name.
const gatewayGuardrailsConfigConfig = `
resource "truefoundry_gateway_guardrails_config" "guardrails" {
  name = "guardrails-config"

  rules = [
    {
      id = "dev-de1-001-gr-francecentral"

      when = {
        target = {
          model = {
            condition = "in"
            values = [
              "dev-de1-001-francecentral/gpt-4.1",
              "dev-de1-001-francecentral/gpt-4o",
            ]
          }
        }
      }

      llm_input_guardrails  = ["dev-de1-001-gr-francecentral/dev-de1-001-cs-francecentral"]
      llm_output_guardrails = ["dev-de1-001-gr-francecentral/dev-de1-001-cs-francecentral"]
    },
  ]
}
`

func TestAccGatewayGuardrailsConfig_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.GatewayConfigCount(); n != 0 {
				return fmt.Errorf("%d gateway configs left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + gatewayGuardrailsConfigConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_gateway_guardrails_config.guardrails", "id"),
					resource.TestCheckResourceAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.#", "1"),
					resource.TestCheckResourceAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.0.when.target.model.condition", "in"),
					resource.TestCheckResourceAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.0.llm_input_guardrails.0", "dev-de1-001-gr-francecentral/dev-de1-001-cs-francecentral"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.0.mcp_tool_pre_invoke_guardrails"),
				),
			},
			// In-place update: subject and metadata matching, MCP tool
			// guardrails, custom error message.
			{
				Config: providerConfig(host) + `
resource "truefoundry_gateway_guardrails_config" "guardrails" {
  name = "guardrails-config"

  rules = [
    {
      id = "everyone-prompt-shield"

      when = {
        subjects = {
          operator = "or"
          in       = ["team:everyone"]
        }
        metadata = { tfy_gateway_cluster = "devde1truefoundrydevelop" }
      }

      llm_input_guardrails = ["region-guardrails/prompt-shield"]
      custom_error_message = "Request blocked by prompt shield."
    },
    {
      id = "mcp-tools"

      when = {
        target = {
          mcp_tools = {
            condition = "in"
            values = [
              {
                name             = "github-mcp"
                enable_all_tools = false
                enabled_tools    = ["create_pr"]
              },
            ]
          }
        }
      }

      mcp_tool_pre_invoke_guardrails  = ["region-guardrails/prompt-shield"]
      mcp_tool_post_invoke_guardrails = ["region-guardrails/content-safety"]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.#", "2"),
					resource.TestCheckResourceAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.0.when.subjects.in.0", "team:everyone"),
					resource.TestCheckResourceAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.0.custom_error_message", "Request blocked by prompt shield."),
					resource.TestCheckResourceAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.1.when.target.mcp_tools.values.0.enabled_tools.0", "create_pr"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.1.llm_input_guardrails"),
				),
			},
		},
	})
}

func TestAccGatewayGuardrailsConfig_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + gatewayGuardrailsConfigConfig},
			{
				ResourceName:      "truefoundry_gateway_guardrails_config.guardrails",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "gateway-guardrails-config",
			},
		},
	})
}

func TestAccGatewayGuardrailsConfig_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "empty target",
			config: `
resource "truefoundry_gateway_guardrails_config" "bad" {
  name = "guardrails-config"
  rules = [
    {
      id                   = "r1"
      when                 = { target = {} }
      llm_input_guardrails = ["g/c"]
    },
  ]
}
`,
			want: regexp.MustCompile("target` needs at least one of"),
		},
		{
			name: "empty subjects",
			config: `
resource "truefoundry_gateway_guardrails_config" "bad" {
  name = "guardrails-config"
  rules = [
    {
      id                   = "r1"
      when                 = { subjects = { operator = "or" } }
      llm_input_guardrails = ["g/c"]
    },
  ]
}
`,
			want: regexp.MustCompile("subjects` needs at least one of"),
		},
		{
			name: "invalid condition",
			config: `
resource "truefoundry_gateway_guardrails_config" "bad" {
  name = "guardrails-config"
  rules = [
    {
      id = "r1"
      when = {
        target = {
          model = { condition = "matches", values = ["a/b"] }
        }
      }
      llm_input_guardrails = ["g/c"]
    },
  ]
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "duplicate rule ids",
			config: `
resource "truefoundry_gateway_guardrails_config" "bad" {
  name = "guardrails-config"
  rules = [
    { id = "r1", when = {}, llm_input_guardrails = ["g/c"] },
    { id = "r1", when = {}, llm_output_guardrails = ["g/c"] },
  ]
}
`,
			want: regexp.MustCompile(`Duplicate rule id`),
		},
		{
			name: "empty guardrail list",
			config: `
resource "truefoundry_gateway_guardrails_config" "bad" {
  name = "guardrails-config"
  rules = [
    { id = "r1", when = {}, llm_input_guardrails = [] },
  ]
}
`,
			want: regexp.MustCompile(`at least 1\s+elements`),
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
