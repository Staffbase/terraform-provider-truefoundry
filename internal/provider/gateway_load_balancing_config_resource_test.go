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

// Two rules: weighted spread across two regions for one model, and a
// priority-ordered fallback chain for a team.
const gatewayLoadBalancingConfigConfig = `
resource "truefoundry_gateway_load_balancing_config" "routing" {
  name = "load-balancing-config"

  rules = [
    {
      id   = "gpt-4o-regions"
      type = "weight-based-routing"

      when = {
        models = ["azure-france/gpt-4o"]
      }

      load_balance_targets = [
        {
          target = "azure-france/gpt-4o"
          weight = 70
        },
        {
          target                = "azure-sweden/gpt-4o"
          weight                = 30
          fallback_status_codes = ["429", "503"]
          fallback_candidate    = true
        },
      ]
    },
    {
      id   = "team-fallback-chain"
      type = "priority-based-routing"

      when = {
        subjects = ["team:everyone"]
      }

      load_balance_targets = [
        {
          target   = "azure-france/gpt-4.1"
          priority = 0
        },
        {
          target   = "azure-sweden/gpt-4.1"
          priority = 1
        },
      ]
    },
  ]
}
`

// The same config with the rules and the targets within a rule listed in a
// different order. Both collections are sets, so this must plan to no changes
// at all.
const gatewayLoadBalancingConfigConfigReordered = `
resource "truefoundry_gateway_load_balancing_config" "routing" {
  name = "load-balancing-config"

  rules = [
    {
      id   = "team-fallback-chain"
      type = "priority-based-routing"

      when = {
        subjects = ["team:everyone"]
      }

      load_balance_targets = [
        {
          target   = "azure-sweden/gpt-4.1"
          priority = 1
        },
        {
          target   = "azure-france/gpt-4.1"
          priority = 0
        },
      ]
    },
    {
      id   = "gpt-4o-regions"
      type = "weight-based-routing"

      when = {
        models = ["azure-france/gpt-4o"]
      }

      load_balance_targets = [
        {
          target                = "azure-sweden/gpt-4o"
          weight                = 30
          fallback_status_codes = ["503", "429"]
          fallback_candidate    = true
        },
        {
          target = "azure-france/gpt-4o"
          weight = 70
        },
      ]
    },
  ]
}
`

func TestAccGatewayLoadBalancingConfig_lifecycle(t *testing.T) {
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
				Config: providerConfig(host) + gatewayLoadBalancingConfigConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_gateway_load_balancing_config.routing", "id"),
					resource.TestCheckResourceAttr("truefoundry_gateway_load_balancing_config.routing", "rules.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_load_balancing_config.routing", "rules.*", map[string]string{
						"id":                     "gpt-4o-regions",
						"type":                   "weight-based-routing",
						"when.models.#":          "1",
						"load_balance_targets.#": "2",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_load_balancing_config.routing", "rules.*.load_balance_targets.*", map[string]string{
						"target":                  "azure-sweden/gpt-4o",
						"weight":                  "30",
						"fallback_candidate":      "true",
						"fallback_status_codes.#": "2",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_load_balancing_config.routing", "rules.*", map[string]string{
						"id":              "team-fallback-chain",
						"type":            "priority-based-routing",
						"when.subjects.#": "1",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_load_balancing_config.routing", "rules.*.load_balance_targets.*", map[string]string{
						"target":   "azure-france/gpt-4.1",
						"priority": "0",
					}),
				),
			},
			// Regression: listing the same rules and targets in a different
			// order must produce an empty plan — both collections are sets.
			{
				Config:   providerConfig(host) + gatewayLoadBalancingConfigConfigReordered,
				PlanOnly: true,
			},
			// In-place update: latency-based routing, an empty `when`, header
			// overrides, and metadata matching on a target.
			{
				Config: providerConfig(host) + `
resource "truefoundry_gateway_load_balancing_config" "routing" {
  name = "load-balancing-config"

  rules = [
    {
      id   = "fastest-wins"
      type = "latency-based-routing"
      when = {}

      load_balance_targets = [
        {
          target = "azure-france/gpt-4o"

          headers_override = {
            remove = ["x-internal-trace"]
            set    = { "x-routed-by" = "terraform" }
          }
        },
        {
          target         = "azure-sweden/gpt-4o"
          metadata_match = { region = "eu-north" }
        },
      ]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_gateway_load_balancing_config.routing", "rules.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_load_balancing_config.routing", "rules.*", map[string]string{
						"id":                     "fastest-wins",
						"type":                   "latency-based-routing",
						"load_balance_targets.#": "2",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_load_balancing_config.routing", "rules.*.load_balance_targets.*", map[string]string{
						"target":                           "azure-france/gpt-4o",
						"headers_override.remove.#":        "1",
						"headers_override.set.x-routed-by": "terraform",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_load_balancing_config.routing", "rules.*.load_balance_targets.*", map[string]string{
						"target":                "azure-sweden/gpt-4o",
						"metadata_match.region": "eu-north",
					}),
				),
			},
		},
	})
}

func TestAccGatewayLoadBalancingConfig_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + gatewayLoadBalancingConfigConfig},
			{
				ResourceName:      "truefoundry_gateway_load_balancing_config.routing",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "gateway-load-balancing-config",
			},
		},
	})
}

func TestAccGatewayLoadBalancingConfig_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "unknown routing type",
			config: `
resource "truefoundry_gateway_load_balancing_config" "bad" {
  name = "load-balancing-config"
  rules = [{
    id                   = "r1"
    type                 = "round-robin"
    when                 = {}
    load_balance_targets = [{ target = "acct/model", weight = 100 }]
  }]
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "weight-based target without weight",
			config: `
resource "truefoundry_gateway_load_balancing_config" "bad" {
  name = "load-balancing-config"
  rules = [{
    id                   = "r1"
    type                 = "weight-based-routing"
    when                 = {}
    load_balance_targets = [{ target = "acct/model" }]
  }]
}
`,
			want: regexp.MustCompile("`weight` is required for\\s+weight-based-routing\\s+targets"),
		},
		{
			name: "priority-based target with weight",
			config: `
resource "truefoundry_gateway_load_balancing_config" "bad" {
  name = "load-balancing-config"
  rules = [{
    id                   = "r1"
    type                 = "priority-based-routing"
    when                 = {}
    load_balance_targets = [{ target = "acct/model", priority = 0, weight = 100 }]
  }]
}
`,
			want: regexp.MustCompile("`weight` must not be set for\\s+priority-based-routing targets"),
		},
		{
			name: "latency-based target with priority",
			config: `
resource "truefoundry_gateway_load_balancing_config" "bad" {
  name = "load-balancing-config"
  rules = [{
    id                   = "r1"
    type                 = "latency-based-routing"
    when                 = {}
    load_balance_targets = [{ target = "acct/model", priority = 0 }]
  }]
}
`,
			want: regexp.MustCompile("`priority` must not be set for\\s+latency-based-routing targets"),
		},
		{
			name: "target without provider account",
			config: `
resource "truefoundry_gateway_load_balancing_config" "bad" {
  name = "load-balancing-config"
  rules = [{
    id                   = "r1"
    type                 = "latency-based-routing"
    when                 = {}
    load_balance_targets = [{ target = "gpt-4o" }]
  }]
}
`,
			want: regexp.MustCompile("must be a `<provider-account>/<model>` name"),
		},
		{
			name: "rule id with underscore",
			config: `
resource "truefoundry_gateway_load_balancing_config" "bad" {
  name = "load-balancing-config"
  rules = [{
    id                   = "rule_one"
    type                 = "latency-based-routing"
    when                 = {}
    load_balance_targets = [{ target = "acct/model" }]
  }]
}
`,
			want: regexp.MustCompile(`must match`),
		},
		{
			name: "duplicate rule ids",
			config: `
resource "truefoundry_gateway_load_balancing_config" "bad" {
  name = "load-balancing-config"
  rules = [
    { id = "r1x", type = "latency-based-routing", when = {}, load_balance_targets = [{ target = "acct/a" }] },
    { id = "r1x", type = "latency-based-routing", when = {}, load_balance_targets = [{ target = "acct/b" }] },
  ]
}
`,
			want: regexp.MustCompile(`Duplicate rule id`),
		},
		{
			name: "no targets",
			config: `
resource "truefoundry_gateway_load_balancing_config" "bad" {
  name = "load-balancing-config"
  rules = [{
    id                   = "r1"
    type                 = "latency-based-routing"
    when                 = {}
    load_balance_targets = []
  }]
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
