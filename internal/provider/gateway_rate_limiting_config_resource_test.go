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

// Two rules: a per-user request cap on one region's models, and a shared
// token cap for everything a team sends.
const gatewayRateLimitingConfigConfig = `
resource "truefoundry_gateway_rate_limiting_config" "limits" {
  name = "rate-limiting-config"

  rules = [
    {
      id = "francecentral-per-user"

      when = {
        models = [
          "azure-france/gpt-4.1",
          "azure-france/gpt-4o",
        ]
      }

      limit_to               = 60
      unit                   = "requests_per_minute"
      rate_limit_applies_per = ["user"]
    },
    {
      id = "everyone-token-cap"

      when = {
        subjects = ["team:everyone"]
      }

      limit_to = 1000000
      unit     = "tokens_per_day"
    },
  ]
}
`

// The same config with the rules and the model values within a rule listed in
// a different order. Both collections are sets, so this must plan to no
// changes at all.
const gatewayRateLimitingConfigConfigReordered = `
resource "truefoundry_gateway_rate_limiting_config" "limits" {
  name = "rate-limiting-config"

  rules = [
    {
      id = "everyone-token-cap"

      when = {
        subjects = ["team:everyone"]
      }

      limit_to = 1000000
      unit     = "tokens_per_day"
    },
    {
      id = "francecentral-per-user"

      when = {
        models = [
          "azure-france/gpt-4o",
          "azure-france/gpt-4.1",
        ]
      }

      limit_to               = 60
      unit                   = "requests_per_minute"
      rate_limit_applies_per = ["user"]
    },
  ]
}
`

func TestAccGatewayRateLimitingConfig_lifecycle(t *testing.T) {
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
				Config: providerConfig(host) + gatewayRateLimitingConfigConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_gateway_rate_limiting_config.limits", "id"),
					resource.TestCheckResourceAttr("truefoundry_gateway_rate_limiting_config.limits", "rules.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_rate_limiting_config.limits", "rules.*", map[string]string{
						"id":                       "francecentral-per-user",
						"limit_to":                 "60",
						"unit":                     "requests_per_minute",
						"when.models.#":            "2",
						"rate_limit_applies_per.#": "1",
						"rate_limit_applies_per.0": "user",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_rate_limiting_config.limits", "rules.*", map[string]string{
						"id":              "everyone-token-cap",
						"limit_to":        "1000000",
						"unit":            "tokens_per_day",
						"when.subjects.#": "1",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_rate_limiting_config.limits", "rules.*.when.models.*", "azure-france/gpt-4o"),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_rate_limiting_config.limits", "rules.*.when.subjects.*", "team:everyone"),
				),
			},
			// Regression: listing the same rules and models in a different
			// order must produce an empty plan — both collections are sets.
			{
				Config:   providerConfig(host) + gatewayRateLimitingConfigConfigReordered,
				PlanOnly: true,
			},
			// In-place update: metadata matching, a metadata partitioning, a
			// raised limit, and one rule dropped.
			{
				Config: providerConfig(host) + `
resource "truefoundry_gateway_rate_limiting_config" "limits" {
  name = "rate-limiting-config"

  rules = [
    {
      id = "per-app-cap"

      when = {
        metadata = { tfy_gateway_cluster = "eu-cluster" }
      }

      limit_to               = 120
      unit                   = "requests_per_hour"
      rate_limit_applies_per = ["model", "metadata.appId"]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_gateway_rate_limiting_config.limits", "rules.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_rate_limiting_config.limits", "rules.*", map[string]string{
						"id":                                "per-app-cap",
						"limit_to":                          "120",
						"unit":                              "requests_per_hour",
						"when.metadata.tfy_gateway_cluster": "eu-cluster",
						"rate_limit_applies_per.#":          "2",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_rate_limiting_config.limits", "rules.*.rate_limit_applies_per.*", "metadata.appId"),
				),
			},
			// An empty `when` block matches every request.
			{
				Config: providerConfig(host) + `
resource "truefoundry_gateway_rate_limiting_config" "limits" {
  name  = "rate-limiting-config"
  rules = [{ id = "tenant-wide", when = {}, limit_to = 10, unit = "requests_per_minute" }]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_gateway_rate_limiting_config.limits", "rules.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_rate_limiting_config.limits", "rules.*", map[string]string{
						"id":       "tenant-wide",
						"limit_to": "10",
					}),
				),
			},
		},
	})
}

func TestAccGatewayRateLimitingConfig_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + gatewayRateLimitingConfigConfig},
			{
				ResourceName:      "truefoundry_gateway_rate_limiting_config.limits",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "gateway-rate-limiting-config",
			},
		},
	})
}

func TestAccGatewayRateLimitingConfig_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "unknown unit",
			config: `
resource "truefoundry_gateway_rate_limiting_config" "bad" {
  name  = "rate-limiting-config"
  rules = [{ id = "r1", when = {}, limit_to = 10, unit = "requests_per_fortnight" }]
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "non-positive limit",
			config: `
resource "truefoundry_gateway_rate_limiting_config" "bad" {
  name  = "rate-limiting-config"
  rules = [{ id = "r1", when = {}, limit_to = 0, unit = "requests_per_minute" }]
}
`,
			want: regexp.MustCompile("`limit_to` must be greater than 0"),
		},
		{
			name: "budget-style applies per value",
			config: `
resource "truefoundry_gateway_rate_limiting_config" "bad" {
  name = "rate-limiting-config"
  rules = [{
    id                     = "r1"
    when                   = {}
    limit_to               = 10
    unit                   = "requests_per_minute"
    rate_limit_applies_per = ["per-user"]
  }]
}
`,
			want: regexp.MustCompile("must be one of `user`"),
		},
		{
			name: "more than two partitionings",
			config: `
resource "truefoundry_gateway_rate_limiting_config" "bad" {
  name = "rate-limiting-config"
  rules = [{
    id                     = "r1"
    when                   = {}
    limit_to               = 10
    unit                   = "requests_per_minute"
    rate_limit_applies_per = ["user", "model", "virtualaccount"]
  }]
}
`,
			want: regexp.MustCompile(`at most 2\s+elements`),
		},
		{
			name: "model without provider account",
			config: `
resource "truefoundry_gateway_rate_limiting_config" "bad" {
  name  = "rate-limiting-config"
  rules = [{ id = "r1", when = { models = ["gpt-4o"] }, limit_to = 10, unit = "requests_per_minute" }]
}
`,
			want: regexp.MustCompile("must be a `<provider-account>/<model>` name"),
		},
		{
			name: "rule id too short",
			config: `
resource "truefoundry_gateway_rate_limiting_config" "bad" {
  name  = "rate-limiting-config"
  rules = [{ id = "r", when = {}, limit_to = 10, unit = "requests_per_minute" }]
}
`,
			want: regexp.MustCompile(`must match`),
		},
		{
			name: "duplicate rule ids",
			config: `
resource "truefoundry_gateway_rate_limiting_config" "bad" {
  name = "rate-limiting-config"
  rules = [
    { id = "r1x", when = {}, limit_to = 10, unit = "requests_per_minute" },
    { id = "r1x", when = {}, limit_to = 20, unit = "requests_per_minute" },
  ]
}
`,
			want: regexp.MustCompile(`Duplicate rule id`),
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
