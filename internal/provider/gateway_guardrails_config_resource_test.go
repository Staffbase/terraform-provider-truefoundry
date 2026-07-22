package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Mirrors the live dev setup: content-safety guardrails on the models of two
// regions, matched by model name.
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
    {
      id = "dev-de1-001-gr-swedencentral"

      when = {
        target = {
          model = {
            condition = "in"
            values = [
              "dev-de1-001-swedencentral/gpt-4.1",
              "dev-de1-001-swedencentral/gpt-4o",
            ]
          }
        }
      }

      llm_input_guardrails  = ["dev-de1-001-gr-swedencentral/dev-de1-001-cs-swedencentral"]
      llm_output_guardrails = ["dev-de1-001-gr-swedencentral/dev-de1-001-cs-swedencentral"]
    },
  ]
}
`

// The same config with the rules and the model values within each rule listed
// in a different order. Rules and model values are sets, so this must plan to
// no changes at all.
const gatewayGuardrailsConfigConfigReordered = `
resource "truefoundry_gateway_guardrails_config" "guardrails" {
  name = "guardrails-config"

  rules = [
    {
      id = "dev-de1-001-gr-swedencentral"

      when = {
        target = {
          model = {
            condition = "in"
            values = [
              "dev-de1-001-swedencentral/gpt-4o",
              "dev-de1-001-swedencentral/gpt-4.1",
            ]
          }
        }
      }

      llm_input_guardrails  = ["dev-de1-001-gr-swedencentral/dev-de1-001-cs-swedencentral"]
      llm_output_guardrails = ["dev-de1-001-gr-swedencentral/dev-de1-001-cs-swedencentral"]
    },
    {
      id = "dev-de1-001-gr-francecentral"

      when = {
        target = {
          model = {
            condition = "in"
            values = [
              "dev-de1-001-francecentral/gpt-4o",
              "dev-de1-001-francecentral/gpt-4.1",
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
					resource.TestCheckResourceAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_guardrails_config.guardrails", "rules.*", map[string]string{
						"id":                          "dev-de1-001-gr-francecentral",
						"when.target.model.condition": "in",
						"when.target.model.values.#":  "2",
						"llm_input_guardrails.#":      "1",
						"llm_input_guardrails.0":      "dev-de1-001-gr-francecentral/dev-de1-001-cs-francecentral",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.*.when.target.model.values.*", "dev-de1-001-francecentral/gpt-4o"),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.*.when.target.model.values.*", "dev-de1-001-swedencentral/gpt-4.1"),
				),
			},
			// Regression: listing the same rules and model values in a
			// different order must produce an empty plan — both collections
			// are sets.
			{
				Config:   providerConfig(host) + gatewayGuardrailsConfigConfigReordered,
				PlanOnly: true,
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
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_guardrails_config.guardrails", "rules.*", map[string]string{
						"id":                                "everyone-prompt-shield",
						"custom_error_message":              "Request blocked by prompt shield.",
						"when.subjects.in.#":                "1",
						"when.metadata.tfy_gateway_cluster": "devde1truefoundrydevelop",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.*.when.subjects.in.*", "team:everyone"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_guardrails_config.guardrails", "rules.*", map[string]string{
						"id":                                "mcp-tools",
						"mcp_tool_pre_invoke_guardrails.0":  "region-guardrails/prompt-shield",
						"mcp_tool_post_invoke_guardrails.0": "region-guardrails/content-safety",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_guardrails_config.guardrails", "rules.*.when.target.mcp_tools.values.*", map[string]string{
						"name":             "github-mcp",
						"enable_all_tools": "false",
						"enabled_tools.#":  "1",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.*.when.target.mcp_tools.values.*.enabled_tools.*", "create_pr"),
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
