package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

func TestAccProviderAccount_azureOpenAILifecycle(t *testing.T) {
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
				Config: providerConfig(host) + `
resource "truefoundry_provider_account" "azure" {
  name           = "spike-azure-openai"
  provider_type  = "azure-openai"
  api_key        = "azure-account-key"
  azure_endpoint = "https://spike.openai.azure.com"
  models = [
    {
      name            = "gpt-4o"
      model_id        = "gpt-4o-deployment"
      api_version     = "2024-10-21"
      foundation_model = "gpt-4o-2024-11-20"
      deployment_type = "datazone_eu"
      region          = "francecentral"
      model_types     = ["chat", "responses"]
      cost = {
        metric = "per_1000_tokens"
        input  = 0.0022
        output = 0.0088
      }
      authorized_subjects = ["user:falk@staffbase.com"]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "fqn", mockcp.Tenant+":azure-openai:spike-azure-openai"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "models.0.model_id", "gpt-4o-deployment"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "models.0.deployment_type", "datazone_eu"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "models.0.region", "francecentral"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "models.0.cost.metric", "per_1000_tokens"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "models.0.cost.input", "0.0022"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "models.0.authorized_subjects.0", "user:falk@staffbase.com"),
				),
			},
			// In-place update: bump the API version, change the cost, and add
			// an embedding deployment without cost.
			{
				Config: providerConfig(host) + `
resource "truefoundry_provider_account" "azure" {
  name           = "spike-azure-openai"
  provider_type  = "azure-openai"
  api_key        = "azure-account-key"
  azure_endpoint = "https://spike.openai.azure.com"
  models = [
    {
      name            = "gpt-4o"
      model_id        = "gpt-4o-deployment"
      api_version     = "2025-01-01-preview"
      foundation_model = "gpt-4o-2024-11-20"
      deployment_type = "datazone_eu"
      region          = "francecentral"
      model_types     = ["chat", "responses"]
      cost = {
        metric = "per_1000_tokens"
        input  = 0.0025
        output = 0.01
      }
      authorized_subjects = ["user:falk@staffbase.com"]
    },
    {
      name        = "embeddings"
      model_id    = "text-embedding-3-large-deployment"
      api_version = "2024-10-21"
      model_types = ["embedding"]
      cost = {
        metric = "per_1000_embedding_tokens"
        value  = 0.000025
      }
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "models.#", "2"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "models.0.api_version", "2025-01-01-preview"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "models.0.cost.input", "0.0025"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "models.1.cost.metric", "per_1000_embedding_tokens"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "models.1.cost.value", "0.000025"),
				),
			},
		},
	})
}

func TestAccProviderAccount_azureFoundryLifecycle(t *testing.T) {
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
				// No account-level api_key: azure-foundry authenticates per model.
				Config: providerConfig(host) + `
resource "truefoundry_provider_account" "foundry" {
  name          = "spike-azure-foundry"
  provider_type = "azure-foundry"
  models = [
    {
      name           = "phi-4"
      model_id       = "phi-4-deployment"
      azure_endpoint = "https://phi-4.eastus.models.ai.azure.com"
      api_key        = "foundry-model-key"
      api_version    = "2024-05-01-preview"
      model_types    = ["chat"]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_provider_account.foundry", "fqn", mockcp.Tenant+":azure-foundry:spike-azure-foundry"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.foundry", "models.0.azure_endpoint", "https://phi-4.eastus.models.ai.azure.com"),
					resource.TestCheckResourceAttrSet("truefoundry_provider_account.foundry", "models.0.api_key"),
				),
			},
			// In-place update: add a second deployment with its own endpoint/key.
			{
				Config: providerConfig(host) + `
resource "truefoundry_provider_account" "foundry" {
  name          = "spike-azure-foundry"
  provider_type = "azure-foundry"
  models = [
    {
      name           = "phi-4"
      model_id       = "phi-4-deployment"
      azure_endpoint = "https://phi-4.eastus.models.ai.azure.com"
      api_key        = "foundry-model-key"
      api_version    = "2024-05-01-preview"
      model_types    = ["chat"]
    },
    {
      name           = "mistral-large"
      model_id       = "mistral-large-deployment"
      azure_endpoint = "https://mistral.westeu.models.ai.azure.com"
      api_key        = "foundry-mistral-key"
      model_types    = ["chat", "completion"]
    },
  ]
}
`,
				Check: resource.TestCheckResourceAttr("truefoundry_provider_account.foundry", "models.#", "2"),
			},
		},
	})
}

func TestAccProviderAccount_azureConfigValidation(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "azure-openai without endpoint",
			config: `
resource "truefoundry_provider_account" "bad" {
  name          = "spike-bad"
  provider_type = "azure-openai"
  api_key       = "key"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o-deployment"
      api_version = "2024-10-21"
      model_types = ["chat"]
    },
  ]
}
`,
			want: regexp.MustCompile("`azure_endpoint` is required"),
		},
		{
			name: "azure-openai model without api_version",
			config: `
resource "truefoundry_provider_account" "bad" {
  name           = "spike-bad"
  provider_type  = "azure-openai"
  api_key        = "key"
  azure_endpoint = "https://spike.openai.azure.com"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o-deployment"
      model_types = ["chat"]
    },
  ]
}
`,
			want: regexp.MustCompile("api_version` is required"),
		},
		{
			name: "azure-foundry with account api_key",
			config: `
resource "truefoundry_provider_account" "bad" {
  name          = "spike-bad"
  provider_type = "azure-foundry"
  api_key       = "account-key"
  models = [
    {
      name           = "phi-4"
      model_id       = "phi-4-deployment"
      azure_endpoint = "https://phi-4.eastus.models.ai.azure.com"
      api_key        = "model-key"
      model_types    = ["chat"]
    },
  ]
}
`,
			want: regexp.MustCompile("`api_key` must not be set"),
		},
		{
			name: "azure-foundry model without api_key",
			config: `
resource "truefoundry_provider_account" "bad" {
  name          = "spike-bad"
  provider_type = "azure-foundry"
  models = [
    {
      name           = "phi-4"
      model_id       = "phi-4-deployment"
      azure_endpoint = "https://phi-4.eastus.models.ai.azure.com"
      model_types    = ["chat"]
    },
  ]
}
`,
			want: regexp.MustCompile("api_key` is required"),
		},
		{
			name: "openai with azure fields",
			config: `
resource "truefoundry_provider_account" "bad" {
  name    = "spike-bad"
  api_key = "sk"
  models = [
    {
      name            = "gpt-4o"
      model_id        = "gpt-4o"
      deployment_type = "global"
      model_types     = ["chat"]
    },
  ]
}
`,
			want: regexp.MustCompile("deployment_type` must not be set"),
		},
		{
			name: "cost with both value and input",
			config: `
resource "truefoundry_provider_account" "bad" {
  name    = "spike-bad"
  api_key = "sk"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat"]
      cost = {
        metric = "per_1000_tokens"
        value  = 0.001
        input  = 0.001
      }
    },
  ]
}
`,
			want: regexp.MustCompile("Exactly one of `value` .* or `input`"),
		},
		{
			name: "openai with region",
			config: `
resource "truefoundry_provider_account" "bad" {
  name    = "spike-bad"
  api_key = "sk"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      region      = "francecentral"
      model_types = ["chat"]
    },
  ]
}
`,
			want: regexp.MustCompile("region` must not be set"),
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
