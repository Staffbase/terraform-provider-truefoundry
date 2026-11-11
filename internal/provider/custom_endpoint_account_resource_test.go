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

// Mirrors the live dev setup: weight-balanced Azure Speech endpoints across
// regions with header auth via Vault secret references and fallback codes.
const customEndpointAccountConfig = `
resource "truefoundry_custom_endpoint_account" "speech" {
  name          = "spike-azure-speech"
  endpoint_type = "azure-speech-service"
  routing_type  = "weight"
  slug          = "tts"

  collaborators = [
    {
      subject = "team:everyone"
      role_id = "provider-account-access"
    },
  ]

  endpoints = [
    {
      name     = "francecentral"
      base_url = "https://francecentral.tts.speech.microsoft.com/cognitiveservices/v1"
      auth_headers = {
        "Ocp-Apim-Subscription-Key" = "tenant:hashicorp:vault-secrets:secret-store:vault::speech/francecentral/value"
      }
      loadbalancing = {
        weight                = 50
        fallback_candidate    = true
        fallback_status_codes = ["429", "500", "503"]
      }
    },
    {
      name     = "swedencentral"
      base_url = "https://swedencentral.tts.speech.microsoft.com/cognitiveservices/v1"
      auth_headers = {
        "Ocp-Apim-Subscription-Key" = "tenant:hashicorp:vault-secrets:secret-store:vault::speech/swedencentral/value"
      }
      loadbalancing = {
        weight         = 50
        metadata_match = { tfy_gateway_cluster = "cluster-a" }
      }
    },
  ]
}
`

func TestAccCustomEndpointAccount_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.AccountCount(); n != 0 {
				return fmt.Errorf("%d accounts left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + customEndpointAccountConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_custom_endpoint_account.speech", "fqn", mockcp.Tenant+":custom-endpoint:spike-azure-speech"),
					resource.TestCheckResourceAttr("truefoundry_custom_endpoint_account.speech", "routing_type", "weight"),
					resource.TestCheckResourceAttr("truefoundry_custom_endpoint_account.speech", "endpoints.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_custom_endpoint_account.speech", "endpoints.*", map[string]string{
						"name":                                  "francecentral",
						"loadbalancing.weight":                  "50",
						"loadbalancing.fallback_status_codes.#": "3",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_custom_endpoint_account.speech", "endpoints.*.loadbalancing.fallback_status_codes.*", "429"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_custom_endpoint_account.speech", "endpoints.*", map[string]string{
						"name": "swedencentral",
						"loadbalancing.metadata_match.tfy_gateway_cluster": "cluster-a",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_custom_endpoint_account.speech", "collaborators.*", map[string]string{
						"subject": "team:everyone",
						"role_id": "provider-account-access",
					}),
				),
			},
			// In-place update: drop load balancing entirely, single endpoint
			// with plain headers, TLS settings, and authorized subjects.
			{
				Config: providerConfig(host) + `
resource "truefoundry_custom_endpoint_account" "speech" {
  name          = "spike-azure-speech"
  endpoint_type = "other"

  endpoints = [
    {
      name     = "docintel"
      base_url = "https://docintel.cognitiveservices.azure.com"
      auth_headers = {
        "Ocp-Apim-Subscription-Key" = "tenant:hashicorp:vault-secrets:secret-store:vault::docintel/value"
      }
      headers             = { "X-Custom-Header" = "value" }
      authorized_subjects = ["user:jane.doe@example.com"]
      tls = {
        reject_unauthorized = true
      }
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_custom_endpoint_account.speech", "endpoints.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_custom_endpoint_account.speech", "endpoints.*", map[string]string{
						"name":                    "docintel",
						"headers.X-Custom-Header": "value",
						"tls.reject_unauthorized": "true",
						"authorized_subjects.#":   "1",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_custom_endpoint_account.speech", "endpoints.*.authorized_subjects.*", "user:jane.doe@example.com"),
					resource.TestCheckNoResourceAttr("truefoundry_custom_endpoint_account.speech", "routing_type"),
				),
			},
		},
	})
}

func TestAccCustomEndpointAccount_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + customEndpointAccountConfig},
			{
				ResourceName:      "truefoundry_custom_endpoint_account.speech",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     mockcp.Tenant + ":custom-endpoint:spike-azure-speech",
			},
		},
	})
}

func TestAccCustomEndpointAccount_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "routing without slug",
			config: `
resource "truefoundry_custom_endpoint_account" "bad" {
  name         = "spike-bad"
  routing_type = "weight"
  endpoints = [
    {
      name          = "e1"
      base_url      = "https://example.com"
      loadbalancing = { weight = 100 }
    },
  ]
}
`,
			want: regexp.MustCompile("slug` is required"),
		},
		{
			name: "routing without loadbalancing",
			config: `
resource "truefoundry_custom_endpoint_account" "bad" {
  name         = "spike-bad"
  routing_type = "weight"
  slug         = "lb"
  endpoints = [
    {
      name     = "e1"
      base_url = "https://example.com"
    },
  ]
}
`,
			want: regexp.MustCompile("loadbalancing` is required"),
		},
		{
			name: "loadbalancing without routing",
			config: `
resource "truefoundry_custom_endpoint_account" "bad" {
  name = "spike-bad"
  endpoints = [
    {
      name          = "e1"
      base_url      = "https://example.com"
      loadbalancing = { weight = 100 }
    },
  ]
}
`,
			want: regexp.MustCompile("requires `routing_type`"),
		},
		{
			name: "priority on weight routing",
			config: `
resource "truefoundry_custom_endpoint_account" "bad" {
  name         = "spike-bad"
  routing_type = "weight"
  slug         = "lb"
  endpoints = [
    {
      name          = "e1"
      base_url      = "https://example.com"
      loadbalancing = { weight = 100, priority = 0 }
    },
  ]
}
`,
			want: regexp.MustCompile("priority` must not be set"),
		},
		{
			name: "invalid endpoint type",
			config: `
resource "truefoundry_custom_endpoint_account" "bad" {
  name          = "spike-bad"
  endpoint_type = "smtp"
  endpoints = [
    { name = "e1", base_url = "https://example.com" },
  ]
}
`,
			want: regexp.MustCompile(`value must be one of`),
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
