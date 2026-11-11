// Copyright 2026, Staffbase SE and contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
//
// You may obtain a copy of the License at
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
      id = "guardrails-france"

      when = {
        target = {
          model = {
            condition = "in"
            values = [
              "azure-france/gpt-4.1",
              "azure-france/gpt-4o",
            ]
          }
        }
      }

      llm_input_guardrails  = ["guardrails-france/content-safety-france"]
      llm_output_guardrails = ["guardrails-france/content-safety-france"]
    },
    {
      id = "guardrails-sweden"

      when = {
        target = {
          model = {
            condition = "in"
            values = [
              "azure-sweden/gpt-4.1",
              "azure-sweden/gpt-4o",
            ]
          }
        }
      }

      llm_input_guardrails  = ["guardrails-sweden/content-safety-sweden"]
      llm_output_guardrails = ["guardrails-sweden/content-safety-sweden"]
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
      id = "guardrails-sweden"

      when = {
        target = {
          model = {
            condition = "in"
            values = [
              "azure-sweden/gpt-4o",
              "azure-sweden/gpt-4.1",
            ]
          }
        }
      }

      llm_input_guardrails  = ["guardrails-sweden/content-safety-sweden"]
      llm_output_guardrails = ["guardrails-sweden/content-safety-sweden"]
    },
    {
      id = "guardrails-france"

      when = {
        target = {
          model = {
            condition = "in"
            values = [
              "azure-france/gpt-4o",
              "azure-france/gpt-4.1",
            ]
          }
        }
      }

      llm_input_guardrails  = ["guardrails-france/content-safety-france"]
      llm_output_guardrails = ["guardrails-france/content-safety-france"]
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
						"id":                          "guardrails-france",
						"when.target.model.condition": "in",
						"when.target.model.values.#":  "2",
						"llm_input_guardrails.#":      "1",
						"llm_input_guardrails.0":      "guardrails-france/content-safety-france",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.*.when.target.model.values.*", "azure-france/gpt-4o"),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_guardrails_config.guardrails", "rules.*.when.target.model.values.*", "azure-sweden/gpt-4.1"),
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
        metadata = { tfy_gateway_cluster = "eu-cluster" }
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
						"when.metadata.tfy_gateway_cluster": "eu-cluster",
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

func TestAccGatewayGuardrailsConfig_retiredRefreshPreservesState(t *testing.T) {
	mock, host := startMockControlPlane(t)
	const addr = "truefoundry_gateway_guardrails_config.guardrails"
	var originalID string
	config := providerConfig(host) + gatewayGuardrailsConfigConfig
	retiredError := regexp.MustCompile(`(?s)endpoint is retired.*truefoundry_gateway_guardrails_policy.*named-guardrails-policies`)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.GatewayConfigCount(); n != 0 {
				return fmt.Errorf("%d folded configs left after destroy", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config, Check: func(s *terraform.State) error {
				originalID = s.RootModule().Resources[addr].Primary.ID
				return nil
			}},
			{PreConfig: func() { mock.SetFoldedGuardrailsRetired(true) }, Config: config, PlanOnly: true, ExpectError: retiredError},
			// Import also reads the endpoint and must stop with migration
			// guidance, not report a nonexistent resource or adopt zero values.
			{ResourceName: addr, ImportState: true, ImportStateId: "gateway-guardrails-config", ExpectError: retiredError},
			{PreConfig: func() { mock.SetFoldedGuardrailsRetired(false) }, Config: config, PlanOnly: true, ExpectNonEmptyPlan: false,
				Check: func(s *terraform.State) error {
					rs := s.RootModule().Resources[addr]
					if rs == nil || rs.Primary.ID != originalID {
						return fmt.Errorf("refresh discarded or replaced folded state: %+v", rs)
					}
					return nil
				}},
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
