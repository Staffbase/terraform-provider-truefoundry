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
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
)

const guardrailsPolicyGroupConfig = `
resource "truefoundry_guardrail_group" "policy" {
  name = "policy-guardrails"
  guardrails = [{
    type = "azure-prompt-shield"
    name = "shield"
    api_key = "tenant:vault:key"
    resource_name = "content-safety"
    api_version = "2024-09-01"
    enforcing_strategy = "enforce"
  }]
}
`

func guardrailsPolicyConfig(name, message string) string {
	return guardrailsPolicyGroupConfig + fmt.Sprintf(`
resource "truefoundry_gateway_guardrails_policy" "test" {
  name = %q
  when = {
    target = { model = { condition = "in", values = ["models/gpt-5-mini", "*"] } }
    subjects = { operator = "and", in = ["team:everyone"], not_in = ["user:excluded"] }
    metadata = { environment = "dev" }
  }
  llm_input_guardrails = ["${truefoundry_guardrail_group.policy.name}/shield"]
  custom_error_message = %q
}
`, name, message)
}

func TestAccGatewayGuardrailsPolicy_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)
	c, err := client.New(host, testAPIKey, "test")
	if err != nil {
		t.Fatal(err)
	}
	const addr = "truefoundry_gateway_guardrails_policy.test"
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.GuardrailsPolicyCount(); n != 0 {
				return fmt.Errorf("%d named policies left after destroy", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + guardrailsPolicyConfig("Policy-1", "Blocked"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet(addr, "id"),
				resource.TestCheckResourceAttr(addr, "name", "Policy-1"),
				resource.TestCheckResourceAttr(addr, "llm_input_guardrails.0", "policy-guardrails/shield"),
				resource.TestCheckResourceAttr(addr, "when.subjects.in.#", "1"),
				resource.TestCheckResourceAttr(addr, "when.metadata.environment", "dev"),
			)},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateId: "Policy-1"},
			// Refresh must round-trip every configured optional value without
			// changing it to null and proposing an update.
			{Config: providerConfig(host) + guardrailsPolicyConfig("Policy-1", "Blocked"), PlanOnly: true, ExpectNonEmptyPlan: false},
			{Config: providerConfig(host) + guardrailsPolicyConfig("Policy-1", "New message"), Check: resource.TestCheckResourceAttr(addr, "custom_error_message", "New message")},
			{Config: providerConfig(host) + guardrailsPolicyConfig("Policy-1", "New message"), PlanOnly: true, ExpectNonEmptyPlan: false},
			// MCP hooks share the folded rule conversion, including per-tool matching.
			{Config: providerConfig(host) + guardrailsPolicyGroupConfig + `
resource "truefoundry_gateway_guardrails_policy" "test" {
  name = "Policy-1"
  when = { target = {
    mcp_servers = { condition = "not_in", values = ["excluded"] }
    mcp_tools = { condition = "in", values = [{ name = "github", enable_all_tools = false, enabled_tools = ["create_pr"] }] }
  } }
  mcp_tool_pre_invoke_guardrails = ["${truefoundry_guardrail_group.policy.name}/shield"]
  mcp_tool_post_invoke_guardrails = ["${truefoundry_guardrail_group.policy.name}/shield"]
}
`, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(addr, "mcp_tool_pre_invoke_guardrails.0", "policy-guardrails/shield"),
				resource.TestCheckResourceAttr(addr, "when.target.mcp_tools.values.#", "1"),
			)},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateId: "Policy-1"},
			// A name change replaces the instance, rather than orphaning the old name.
			{Config: providerConfig(host) + guardrailsPolicyConfig("Policy-2", "Renamed"), Check: func(*terraform.State) error {
				if n := mock.GuardrailsPolicyCount(); n != 1 {
					return fmt.Errorf("got %d policies after replacement", n)
				}
				return nil
			}},
			// Refresh must remove a missing instance and Terraform must recreate it.
			{PreConfig: func() {
				if err := c.DeleteGuardrailsPolicy(context.Background(), "Policy-2"); err != nil {
					t.Fatal(err)
				}
			}, Config: providerConfig(host) + guardrailsPolicyConfig("Policy-2", "Renamed")},
		},
	})
}

func TestAccGatewayGuardrailsPolicy_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)
	cases := []struct{ name, body, want string }{
		{"missing target", `when = {}`, "target.*is required"},
		{"empty target", `when = { target = {} }`, "needs at least one"},
		{"empty subjects", `when = { target = { model = { condition = "in", values = ["*"] } }, subjects = { operator = "or" } }
llm_input_guardrails = ["g/c"]`, "subjects.*needs at least one"},
		{"model without hook", `when = { target = { model = { condition = "in", values = ["*"] } } }`, "Model targets require"},
		{"mcp without hook", `when = { target = { mcp_servers = { condition = "in", values = ["*"] } } }`, "MCP targets require"},
		{"both target kinds need hooks", `when = { target = { model = { condition = "in", values = ["*"] }, mcp_servers = { condition = "in", values = ["*"] } } }
llm_input_guardrails = ["g/c"]`, "MCP targets require"},
		{"empty hook", `when = { target = { model = { condition = "in", values = ["*"] } } }
llm_input_guardrails = []`, `at least 1\s+elements`},
		{"invalid condition", `when = { target = { model = { condition = "matches", values = ["*"] } } }
llm_input_guardrails = ["g/c"]`, "value must be one of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: protoV6Factories, Steps: []resource.TestStep{{
				Config:   providerConfig(host) + `resource "truefoundry_gateway_guardrails_policy" "bad" {` + "\nname = \"policy-1\"\n" + tc.body + "\n}",
				PlanOnly: true, ExpectError: regexp.MustCompile(tc.want),
			}}})
		})
	}
}

func TestAccGatewayGuardrailsPolicy_unknownGuardrail(t *testing.T) {
	_, host := startMockControlPlane(t)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: protoV6Factories, Steps: []resource.TestStep{{
		Config: providerConfig(host) + `
resource "truefoundry_gateway_guardrails_policy" "bad" {
  name = "policy-1"
  when = { target = { model = { condition = "in", values = ["*"] } } }
  llm_input_guardrails = ["nope/nope"]
}
`, ExpectError: regexp.MustCompile(`(?s)HTTP 400.*Guardrail\s+Integration nope/nope.*does not exist`),
	}}})
}

func TestAccGatewayGuardrailsPolicy_invalidName(t *testing.T) {
	_, host := startMockControlPlane(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{{
			Config:      providerConfig(host) + guardrailsPolicyConfig("bad_name", "Blocked"),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile("must contain only letters, digits, and hyphens"),
		}},
	})
}

func TestAccGuardrails_emptyValuesRejected(t *testing.T) {
	_, host := startMockControlPlane(t)
	const modelTarget = `target = { model = { condition = "in", values = ["*"] } }`
	cases := []struct{ name, when, extra string }{
		{"custom error message", modelTarget, `custom_error_message = ""`},
		{"metadata", modelTarget + `, metadata = {}`, ""},
		{"subjects in", modelTarget + `, subjects = { operator = "and", in = [], not_in = ["user:excluded"] }`, ""},
		{"subjects not_in", modelTarget + `, subjects = { operator = "and", in = ["team:everyone"], not_in = [] }`, ""},
		{"model values", `target = { model = { condition = "in", values = [] } }`, ""},
		{"mcp server values", `target = { mcp_servers = { condition = "in", values = [] } }`, ""},
		{"mcp tool values", `target = { mcp_tools = { condition = "in", values = [] } }`, ""},
		{"enabled tools", `target = { mcp_tools = { condition = "in", values = [{ name = "github", enable_all_tools = false, enabled_tools = [] }] } }`, ""},
	}
	// These validators live in the shared rule schema, so both resources
	// must reject configurations that normalize empty collections to null.
	for _, typ := range []string{"gateway_guardrails_policy", "gateway_guardrails_config"} {
		for _, tc := range cases {
			t.Run(typ+"/"+tc.name, func(t *testing.T) {
				body := fmt.Sprintf("when = { %s }\nllm_input_guardrails = [\"g/c\"]\nmcp_tool_pre_invoke_guardrails = [\"g/c\"]\n%s", tc.when, tc.extra)
				if typ == "gateway_guardrails_config" {
					body = "rules = [{ id = \"rule-1\"\n" + body + "\n}]"
				}
				resource.Test(t, resource.TestCase{
					ProtoV6ProviderFactories: protoV6Factories,
					Steps: []resource.TestStep{{
						Config:      providerConfig(host) + fmt.Sprintf("resource \"truefoundry_%s\" \"bad\" {\nname = \"policy-1\"\n%s\n}", typ, body),
						PlanOnly:    true,
						ExpectError: regexp.MustCompile("at least 1"),
					}},
				})
			})
		}
	}
}
