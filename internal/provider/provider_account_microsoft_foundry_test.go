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
