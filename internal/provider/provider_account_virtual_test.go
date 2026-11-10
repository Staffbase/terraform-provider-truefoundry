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

func TestAccProviderAccount_virtualModelLifecycle(t *testing.T) {
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
			// Weight-based routing, mirroring the shape used on the dev tenant.
			{
				Config: providerConfig(host) + `
resource "truefoundry_provider_account" "virtual" {
  name          = "spike-virtual"
  provider_type = "virtual-model"
  models = [
    {
      name        = "gpt-5-mini"
      model_types = ["chat", "completion"]
      routing = {
        type = "weight-based-routing"
        targets = [
          {
            target         = "spike-azure-fc/gpt-5-mini"
            weight         = 1
            metadata_match = { tfy_gateway_cluster = "cluster-a" }
          },
          {
            target = "spike-azure-sc/gpt-5-mini"
            weight = 3
          },
        ]
      }
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_provider_account.virtual", "fqn", mockcp.Tenant+":virtual-model:spike-virtual"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.virtual", "models.*", map[string]string{
						"name":              "gpt-5-mini",
						"routing.type":      "weight-based-routing",
						"routing.targets.#": "2",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.virtual", "models.*.routing.targets.*", map[string]string{
						"target": "spike-azure-sc/gpt-5-mini",
						"weight": "3",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.virtual", "models.*.routing.targets.*", map[string]string{
						"target":                             "spike-azure-fc/gpt-5-mini",
						"weight":                             "1",
						"metadata_match.tfy_gateway_cluster": "cluster-a",
					}),
				),
			},
			// In-place update: shift weights, add a slug, and add a
			// priority-based model with fallback settings.
			{
				Config: providerConfig(host) + `
resource "truefoundry_provider_account" "virtual" {
  name          = "spike-virtual"
  provider_type = "virtual-model"
  models = [
    {
      name        = "gpt-5-mini"
      slug        = "gpt-5-mini-lb"
      model_types = ["chat", "completion"]
      routing = {
        type = "weight-based-routing"
        targets = [
          {
            target         = "spike-azure-fc/gpt-5-mini"
            weight         = 2
            metadata_match = { tfy_gateway_cluster = "cluster-a" }
          },
        ]
      }
    },
    {
      name        = "claude-failover"
      model_types = ["chat"]
      routing = {
        type = "priority-based-routing"
        targets = [
          {
            target                = "spike-claude-sweden/claude-sonnet-5"
            priority              = 0
            fallback_status_codes = ["429", "500"]
          },
          {
            target             = "spike-bedrock/claude-sonnet-5"
            priority           = 1
            fallback_candidate = true
          },
        ]
      }
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_provider_account.virtual", "models.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.virtual", "models.*", map[string]string{
						"name":              "gpt-5-mini",
						"slug":              "gpt-5-mini-lb",
						"routing.type":      "weight-based-routing",
						"routing.targets.#": "1",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.virtual", "models.*.routing.targets.*", map[string]string{
						"target": "spike-azure-fc/gpt-5-mini",
						"weight": "2",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.virtual", "models.*", map[string]string{
						"name":              "claude-failover",
						"routing.type":      "priority-based-routing",
						"routing.targets.#": "2",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.virtual", "models.*.routing.targets.*", map[string]string{
						"target":                  "spike-claude-sweden/claude-sonnet-5",
						"priority":                "0",
						"fallback_status_codes.#": "2",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_provider_account.virtual", "models.*.routing.targets.*.fallback_status_codes.*", "429"),
					resource.TestCheckTypeSetElemAttr("truefoundry_provider_account.virtual", "models.*.routing.targets.*.fallback_status_codes.*", "500"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.virtual", "models.*.routing.targets.*", map[string]string{
						"target":             "spike-bedrock/claude-sonnet-5",
						"priority":           "1",
						"fallback_candidate": "true",
					}),
				),
			},
			// Regression: listing the same models, targets, model_types, and
			// fallback_status_codes in a different order is a pure set
			// reordering and must produce an empty plan.
			{
				PlanOnly: true,
				Config: providerConfig(host) + `
resource "truefoundry_provider_account" "virtual" {
  name          = "spike-virtual"
  provider_type = "virtual-model"
  models = [
    {
      name        = "claude-failover"
      model_types = ["chat"]
      routing = {
        type = "priority-based-routing"
        targets = [
          {
            target             = "spike-bedrock/claude-sonnet-5"
            priority           = 1
            fallback_candidate = true
          },
          {
            target                = "spike-claude-sweden/claude-sonnet-5"
            priority              = 0
            fallback_status_codes = ["500", "429"]
          },
        ]
      }
    },
    {
      name        = "gpt-5-mini"
      slug        = "gpt-5-mini-lb"
      model_types = ["completion", "chat"]
      routing = {
        type = "weight-based-routing"
        targets = [
          {
            target         = "spike-azure-fc/gpt-5-mini"
            weight         = 2
            metadata_match = { tfy_gateway_cluster = "cluster-a" }
          },
        ]
      }
    },
  ]
}
`,
			},
		},
	})
}

func TestAccProviderAccount_virtualModelImport(t *testing.T) {
	_, host := startMockControlPlane(t)

	config := providerConfig(host) + `
resource "truefoundry_provider_account" "virtual" {
  name          = "spike-virtual-import"
  provider_type = "virtual-model"
  models = [
    {
      name        = "gpt-5-mini"
      model_types = ["chat"]
      routing = {
        type = "weight-based-routing"
        targets = [
          {
            target         = "spike-azure-fc/gpt-5-mini"
            weight         = 1
            metadata_match = { tfy_gateway_cluster = "cluster-a" }
          },
        ]
      }
    },
  ]
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				ResourceName:      "truefoundry_provider_account.virtual",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     mockcp.Tenant + ":virtual-model:spike-virtual-import",
			},
		},
	})
}

func TestAccProviderAccount_virtualModelValidation(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "missing routing",
			config: `
resource "truefoundry_provider_account" "bad" {
  name          = "spike-bad"
  provider_type = "virtual-model"
  models = [
    {
      name        = "gpt-5-mini"
      model_types = ["chat"]
    },
  ]
}
`,
			want: regexp.MustCompile("`routing` of model \"gpt-5-mini\" is required"),
		},
		{
			name: "model_id on virtual model",
			config: `
resource "truefoundry_provider_account" "bad" {
  name          = "spike-bad"
  provider_type = "virtual-model"
  models = [
    {
      name        = "gpt-5-mini"
      model_id    = "gpt-5-mini"
      model_types = ["chat"]
      routing = {
        type    = "weight-based-routing"
        targets = [{ target = "acc/model", weight = 1 }]
      }
    },
  ]
}
`,
			want: regexp.MustCompile("`model_id` of model \"gpt-5-mini\" must not be set"),
		},
		{
			name: "priority on weight-based target",
			config: `
resource "truefoundry_provider_account" "bad" {
  name          = "spike-bad"
  provider_type = "virtual-model"
  models = [
    {
      name        = "gpt-5-mini"
      model_types = ["chat"]
      routing = {
        type    = "weight-based-routing"
        targets = [{ target = "acc/model", weight = 1, priority = 0 }]
      }
    },
  ]
}
`,
			want: regexp.MustCompile("Model \"gpt-5-mini\", target \"acc/model\": `priority` must not be set"),
		},
		{
			name: "weight missing on weight-based target",
			config: `
resource "truefoundry_provider_account" "bad" {
  name          = "spike-bad"
  provider_type = "virtual-model"
  models = [
    {
      name        = "gpt-5-mini"
      model_types = ["chat"]
      routing = {
        type    = "weight-based-routing"
        targets = [{ target = "acc/model" }]
      }
    },
  ]
}
`,
			want: regexp.MustCompile("Model \"gpt-5-mini\", target \"acc/model\": `weight` is required"),
		},
		{
			name: "routing on openai model",
			config: `
resource "truefoundry_provider_account" "bad" {
  name    = "spike-bad"
  api_key = "sk"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat"]
      routing = {
        type    = "weight-based-routing"
        targets = [{ target = "acc/model", weight = 1 }]
      }
    },
  ]
}
`,
			want: regexp.MustCompile("`routing` of model \"gpt-4o\" must not be set"),
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
