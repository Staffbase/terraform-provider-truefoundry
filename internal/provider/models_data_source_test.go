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
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

func TestAccModelsDataSource(t *testing.T) {
	_, host := startMockControlPlane(t)

	config := providerConfig(host) + `
resource "truefoundry_provider_account" "openai" {
  name    = "spike-openai"
  api_key = "sk-upstream-secret"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat"]
    },
    {
      name        = "text-embedding-3-small"
      model_id    = "text-embedding-3-small"
      model_types = ["embedding"]
    },
  ]
}

data "truefoundry_models" "all" {
  depends_on = [truefoundry_provider_account.openai]
}

data "truefoundry_models" "filtered" {
  provider_account_fqn = truefoundry_provider_account.openai.fqn
  depends_on           = [truefoundry_provider_account.openai]
}

data "truefoundry_models" "other_account" {
  provider_account_fqn = "` + mockcp.Tenant + `:openai:does-not-exist"
  depends_on           = [truefoundry_provider_account.openai]
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.truefoundry_models.all", "models.#", "2"),
					resource.TestCheckResourceAttr("data.truefoundry_models.all", "models.0.model_id", "gpt-4o"),
					resource.TestCheckResourceAttr("data.truefoundry_models.all", "models.0.fqn", mockcp.Tenant+":openai:spike-openai:model:gpt-4o"),
					resource.TestCheckResourceAttr("data.truefoundry_models.all", "models.0.provider_account_fqn", mockcp.Tenant+":openai:spike-openai"),
					resource.TestCheckResourceAttr("data.truefoundry_models.filtered", "models.#", "2"),
					resource.TestCheckResourceAttr("data.truefoundry_models.other_account", "models.#", "0"),
				),
			},
		},
	})
}
