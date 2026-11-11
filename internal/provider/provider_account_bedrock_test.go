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

// Mirrors the live dev account: access-key auth via Vault secret references,
// a rerank model, account-level region.
const bedrockAccountConfig = `
resource "truefoundry_provider_account" "bedrock" {
  name              = "spike-bedrock"
  provider_type     = "aws-bedrock"
  region            = "eu-central-1"
  access_key_id     = "tenant:hashicorp:vault-secrets:secret-store:vault::bedrock/access_key_id"
  secret_access_key = "tenant:hashicorp:vault-secrets:secret-store:vault::bedrock/secret_access_key"

  models = [
    {
      name        = "cohere-rerank-v3-5"
      model_id    = "cohere.rerank-v3-5:0"
      model_types = ["rerank"]
    },
  ]
}
`

func TestAccProviderAccount_bedrockLifecycle(t *testing.T) {
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
				Config: providerConfig(host) + bedrockAccountConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_provider_account.bedrock", "fqn", mockcp.Tenant+":aws-bedrock:spike-bedrock"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.bedrock", "region", "eu-central-1"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.bedrock", "models.*", map[string]string{
						"name":     "cohere-rerank-v3-5",
						"model_id": "cohere.rerank-v3-5:0",
					}),
				),
			},
			// In-place update: add a Claude inference profile with a
			// per-model region override and cost.
			{
				Config: providerConfig(host) + `
resource "truefoundry_provider_account" "bedrock" {
  name              = "spike-bedrock"
  provider_type     = "aws-bedrock"
  region            = "eu-central-1"
  access_key_id     = "tenant:hashicorp:vault-secrets:secret-store:vault::bedrock/access_key_id"
  secret_access_key = "tenant:hashicorp:vault-secrets:secret-store:vault::bedrock/secret_access_key"

  models = [
    {
      name        = "cohere-rerank-v3-5"
      model_id    = "cohere.rerank-v3-5:0"
      model_types = ["rerank"]
    },
    {
      name        = "claude-sonnet"
      model_id    = "eu.anthropic.claude-sonnet-5-20260203-v1:0"
      region      = "eu-west-1"
      model_types = ["chat", "completion"]
      cost = {
        metric = "per_1000_tokens"
        input  = 0.003
        output = 0.015
      }
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_provider_account.bedrock", "models.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.bedrock", "models.*", map[string]string{
						"name":        "claude-sonnet",
						"model_id":    "eu.anthropic.claude-sonnet-5-20260203-v1:0",
						"region":      "eu-west-1",
						"cost.metric": "per_1000_tokens",
						"cost.input":  "0.003",
						"cost.output": "0.015",
					}),
				),
			},
		},
	})
}

func TestAccProviderAccount_bedrockImport(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + bedrockAccountConfig},
			{
				ResourceName:      "truefoundry_provider_account.bedrock",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     mockcp.Tenant + ":aws-bedrock:spike-bedrock",
			},
		},
	})
}

func TestAccProviderAccount_bedrockValidation(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "missing region",
			config: `
resource "truefoundry_provider_account" "bad" {
  name          = "spike-bad"
  provider_type = "aws-bedrock"
  api_key       = "bedrock-api-key"
  models = [
    { name = "m", model_id = "cohere.rerank-v3-5:0", model_types = ["rerank"] },
  ]
}
`,
			want: regexp.MustCompile("region` is required"),
		},
		{
			name: "no auth at all",
			config: `
resource "truefoundry_provider_account" "bad" {
  name          = "spike-bad"
  provider_type = "aws-bedrock"
  region        = "eu-central-1"
  models = [
    { name = "m", model_id = "cohere.rerank-v3-5:0", model_types = ["rerank"] },
  ]
}
`,
			want: regexp.MustCompile("exactly one of"),
		},
		{
			name: "both auth kinds",
			config: `
resource "truefoundry_provider_account" "bad" {
  name              = "spike-bad"
  provider_type     = "aws-bedrock"
  region            = "eu-central-1"
  api_key           = "bedrock-api-key"
  access_key_id     = "AKIA123"
  secret_access_key = "secret"
  models = [
    { name = "m", model_id = "cohere.rerank-v3-5:0", model_types = ["rerank"] },
  ]
}
`,
			want: regexp.MustCompile("exactly one of"),
		},
		{
			name: "access key without secret",
			config: `
resource "truefoundry_provider_account" "bad" {
  name          = "spike-bad"
  provider_type = "aws-bedrock"
  region        = "eu-central-1"
  access_key_id = "AKIA123"
  models = [
    { name = "m", model_id = "cohere.rerank-v3-5:0", model_types = ["rerank"] },
  ]
}
`,
			want: regexp.MustCompile("must be set together"),
		},
		{
			name: "access keys on openai account",
			config: `
resource "truefoundry_provider_account" "bad" {
  name              = "spike-bad"
  api_key           = "sk"
  access_key_id     = "AKIA123"
  secret_access_key = "secret"
  models = [
    { name = "gpt-4o", model_id = "gpt-4o", model_types = ["chat"] },
  ]
}
`,
			want: regexp.MustCompile("access_key_id` must not be set"),
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
