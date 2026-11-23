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

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

const microsoftFoundryConfig = `
resource "truefoundry_provider_account" "msf" {
  name             = "spike-ms-foundry"
  provider_type    = "microsoft-foundry"
  api_key          = "tenant:hashicorp:vault-secrets:secret-store:vault::models/foundry/value"
  project_endpoint = "https://spike.services.ai.azure.com/api/projects/spike"
  models = [
    {
      name        = "decision"
      model_id    = "decision-1"
      publisher   = "Other"
      model_types = ["decision"]
    },
    {
      name            = "gpt-4o"
      model_id        = "gpt-4o-deployment"
      publisher       = "OpenAI"
      deployment_type = "datazone_eu"
      model_types     = ["chat", "responses"]
    },
  ]
}
`

func TestAccProviderAccount_microsoftFoundryLifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.AccountCount(); n != 0 {
				return fmt.Errorf("%d provider accounts left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + microsoftFoundryConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_provider_account.msf", "fqn", mockcp.Tenant+":microsoft-foundry:spike-ms-foundry"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.msf", "project_endpoint", "https://spike.services.ai.azure.com/api/projects/spike"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.msf", "models.*", map[string]string{
						"name":          "decision",
						"model_id":      "decision-1",
						"publisher":     "Other",
						"model_types.#": "1",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.msf", "models.*", map[string]string{
						"name":            "gpt-4o",
						"publisher":       "OpenAI",
						"deployment_type": "datazone_eu",
					}),
				),
			},
			// Re-reading the account must not produce a diff.
			{PlanOnly: true, Config: providerConfig(host) + microsoftFoundryConfig},
			{
				ResourceName:      "truefoundry_provider_account.msf",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs, ok := s.RootModule().Resources["truefoundry_provider_account.msf"]
					if !ok {
						return "", fmt.Errorf("resource not found in state")
					}
					return rs.Primary.Attributes["fqn"], nil
				},
			},
		},
	})
}

func TestAccProviderAccount_microsoftFoundryValidation(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "without api_key",
			config: `
resource "truefoundry_provider_account" "bad" {
  name             = "spike-bad"
  provider_type    = "microsoft-foundry"
  project_endpoint = "https://spike.services.ai.azure.com/api/projects/spike"
  models = [{ name = "decision", model_id = "decision-1", publisher = "Other", model_types = ["decision"] }]
}
`,
			want: regexp.MustCompile("`api_key` is required"),
		},
		{
			name: "without endpoint",
			config: `
resource "truefoundry_provider_account" "bad" {
  name          = "spike-bad"
  provider_type = "microsoft-foundry"
  api_key       = "key"
  models = [{ name = "decision", model_id = "decision-1", publisher = "Other", model_types = ["decision"] }]
}
`,
			want: regexp.MustCompile("`openai_endpoint` or `project_endpoint` is required"),
		},
		{
			name: "model without publisher",
			config: `
resource "truefoundry_provider_account" "bad" {
  name             = "spike-bad"
  provider_type    = "microsoft-foundry"
  api_key          = "key"
  project_endpoint = "https://spike.services.ai.azure.com/api/projects/spike"
  models = [{ name = "decision", model_id = "decision-1", model_types = ["decision"] }]
}
`,
			want: regexp.MustCompile("`publisher` of model \"decision\" is required"),
		},
		{
			name: "per-model endpoint",
			config: `
resource "truefoundry_provider_account" "bad" {
  name             = "spike-bad"
  provider_type    = "microsoft-foundry"
  api_key          = "key"
  project_endpoint = "https://spike.services.ai.azure.com/api/projects/spike"
  models = [{
    name           = "decision"
    model_id       = "decision-1"
    publisher      = "Other"
    azure_endpoint = "https://spike.services.ai.azure.com"
    model_types    = ["decision"]
  }]
}
`,
			want: regexp.MustCompile("`azure_endpoint` of model \"decision\" must not be set"),
		},
		{
			name: "publisher on another provider type",
			config: `
resource "truefoundry_provider_account" "bad" {
  name    = "spike-bad"
  api_key = "sk"
  models = [{ name = "gpt-4o", model_id = "gpt-4o", publisher = "OpenAI", model_types = ["chat"] }]
}
`,
			want: regexp.MustCompile("`publisher` of model \"gpt-4o\" must not be set"),
		},
		{
			name: "project_endpoint on another provider type",
			config: `
resource "truefoundry_provider_account" "bad" {
  name             = "spike-bad"
  api_key          = "sk"
  project_endpoint = "https://spike.services.ai.azure.com/api/projects/spike"
  models = [{ name = "gpt-4o", model_id = "gpt-4o", model_types = ["chat"] }]
}
`,
			want: regexp.MustCompile("`project_endpoint` must not be set"),
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

// microsoftFoundryCostConfig is a one-model account whose cost block is cost.
func microsoftFoundryCostConfig(cost string) string {
	return `
resource "truefoundry_provider_account" "cost" {
  name            = "spike-ms-cost"
  provider_type   = "microsoft-foundry"
  api_key         = "tenant:hashicorp:vault-secrets:secret-store:vault::models/foundry/value"
  openai_endpoint = "https://spike.openai.azure.com"
  models = [
    {
      name            = "gpt-5.6-luna"
      model_id        = "gpt-5.6-luna"
      publisher       = "OpenAI"
      deployment_type = "datazone_eu"
      model_types     = ["chat"]
      cost            = ` + cost + `
    },
  ]
}
`
}

const (
	privateLunaCost = `{
        metric     = "per_1000_tokens"
        input      = 0.00022
        output     = 0.00132
        cache_read = 0.000022
      }`
	publicCost = `{ metric = "public_cost" }`
)

var costRateAttrRegexp = regexp.MustCompile(`^models\.[^.]+\.cost\.(value|input|output|cache_read|cache_write)$`)

// checkNoCostRates fails if any model of the account has a cost rate in state.
func checkNoCostRates(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("%s not found in state", name)
		}
		for k, v := range rs.Primary.Attributes {
			if costRateAttrRegexp.MatchString(k) {
				return fmt.Errorf("%s = %q in state, want unset with public_cost", k, v)
			}
		}
		return nil
	}
}

// checkMockCost compares the cost block the mock stored for the luna model.
func checkMockCost(mock *mockcp.Server, want func(map[string]any) error) resource.TestCheckFunc {
	return func(*terraform.State) error {
		return want(mock.IntegrationCost("spike-ms-cost", "gpt-5.6-luna"))
	}
}

func TestAccProviderAccount_publicCost(t *testing.T) {
	mock, host := startMockControlPlane(t)
	const addr = "truefoundry_provider_account.cost"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.AccountCount(); n != 0 {
				return fmt.Errorf("%d provider accounts left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + microsoftFoundryCostConfig(privateLunaCost),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckTypeSetElemNestedAttrs(addr, "models.*", map[string]string{
						"cost.metric":     "per_1000_tokens",
						"cost.input":      "0.00022",
						"cost.output":     "0.00132",
						"cost.cache_read": "0.000022",
					}),
					checkMockCost(mock, func(cost map[string]any) error {
						if _, ok := cost["value"]; !ok {
							return fmt.Errorf("stored cost %v has no value, want private rates", cost)
						}
						return nil
					}),
				),
			},
			// Switching to public pricing sends the metric alone.
			{
				Config: providerConfig(host) + microsoftFoundryCostConfig(publicCost),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckTypeSetElemNestedAttrs(addr, "models.*", map[string]string{
						"cost.metric": "public_cost",
					}),
					checkNoCostRates(addr),
					checkMockCost(mock, func(cost map[string]any) error {
						if len(cost) != 1 || cost["metric"] != "public_cost" {
							return fmt.Errorf("stored cost = %v, want only metric public_cost", cost)
						}
						return nil
					}),
				),
			},
			// Rates returned with public_cost (e.g. resolved from the catalog,
			// in a shape the private cost decoder rejects) are not drift.
			{
				PreConfig: func() {
					mock.SetIntegrationCostValue("spike-ms-cost", "gpt-5.6-luna", map[string]any{"input": 0.00022, "output": "1.32"})
				},
				PlanOnly: true,
				Config:   providerConfig(host) + microsoftFoundryCostConfig(publicCost),
			},
			{
				ResourceName:      addr,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs, ok := s.RootModule().Resources[addr]
					if !ok {
						return "", fmt.Errorf("resource not found in state")
					}
					return rs.Primary.Attributes["fqn"], nil
				},
			},
			// And back to private rates.
			{
				Config: providerConfig(host) + microsoftFoundryCostConfig(privateLunaCost),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckTypeSetElemNestedAttrs(addr, "models.*", map[string]string{
						"cost.metric":     "per_1000_tokens",
						"cost.input":      "0.00022",
						"cost.cache_read": "0.000022",
					}),
					checkMockCost(mock, func(cost map[string]any) error {
						value, _ := cost["value"].(map[string]any)
						if value["input"] != 0.00022 {
							return fmt.Errorf("stored cost = %v, want input 0.00022", cost)
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccProviderAccount_publicCostRejectsRates(t *testing.T) {
	_, host := startMockControlPlane(t)

	for _, attr := range []string{"value", "input", "output", "cache_read", "cache_write"} {
		t.Run(attr, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6Factories,
				Steps: []resource.TestStep{{
					Config:      providerConfig(host) + microsoftFoundryCostConfig(`{ metric = "public_cost", `+attr+` = 0.001 }`),
					ExpectError: regexp.MustCompile(`must\s+not\s+be\s+set\s+with\s+metric\s+"public_cost"`),
				}},
			})
		})
	}
}
