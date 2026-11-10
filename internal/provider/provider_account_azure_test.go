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
      authorized_subjects = ["user:jane.doe@example.com"]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_provider_account.azure", "fqn", mockcp.Tenant+":azure-openai:spike-azure-openai"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.azure", "models.*", map[string]string{
						"name":                  "gpt-4o",
						"model_id":              "gpt-4o-deployment",
						"deployment_type":       "datazone_eu",
						"region":                "francecentral",
						"cost.metric":           "per_1000_tokens",
						"cost.input":            "0.0022",
						"model_types.#":         "2",
						"authorized_subjects.#": "1",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_provider_account.azure", "models.*.authorized_subjects.*", "user:jane.doe@example.com"),
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
      authorized_subjects = ["user:jane.doe@example.com"]
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
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.azure", "models.*", map[string]string{
						"name":        "gpt-4o",
						"api_version": "2025-01-01-preview",
						"cost.input":  "0.0025",
						"cost.output": "0.01",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.azure", "models.*", map[string]string{
						"name":        "embeddings",
						"cost.metric": "per_1000_embedding_tokens",
						"cost.value":  "0.000025",
					}),
				),
			},
			// Regression: listing the same models, model_types, and
			// authorized_subjects in a different order is a pure set
			// reordering and must produce an empty plan.
			{
				PlanOnly: true,
				Config: providerConfig(host) + `
resource "truefoundry_provider_account" "azure" {
  name           = "spike-azure-openai"
  provider_type  = "azure-openai"
  api_key        = "azure-account-key"
  azure_endpoint = "https://spike.openai.azure.com"
  models = [
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
    {
      name            = "gpt-4o"
      model_id        = "gpt-4o-deployment"
      api_version     = "2025-01-01-preview"
      foundation_model = "gpt-4o-2024-11-20"
      deployment_type = "datazone_eu"
      region          = "francecentral"
      model_types     = ["responses", "chat"]
      cost = {
        metric = "per_1000_tokens"
        input  = 0.0025
        output = 0.01
      }
      authorized_subjects = ["user:jane.doe@example.com"]
    },
  ]
}
`,
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
      api_key        = "tfy-secret://tenant:secrets:foundry-model-key"
      api_version    = "2024-05-01-preview"
      model_types    = ["chat"]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_provider_account.foundry", "fqn", mockcp.Tenant+":azure-foundry:spike-azure-foundry"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.foundry", "models.*", map[string]string{
						"name":           "phi-4",
						"azure_endpoint": "https://phi-4.eastus.models.ai.azure.com",
						"api_key":        "tfy-secret://tenant:secrets:foundry-model-key",
					}),
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
      api_key        = "tfy-secret://tenant:secrets:foundry-model-key"
      api_version    = "2024-05-01-preview"
      model_types    = ["chat"]
    },
    {
      name           = "mistral-large"
      model_id       = "mistral-large-deployment"
      azure_endpoint = "https://mistral.westeu.models.ai.azure.com"
      api_key        = "tfy-secret://tenant:secrets:foundry-mistral-key"
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
			want: regexp.MustCompile("`api_version` of model \"gpt-4o\" is required"),
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
			name: "azure-foundry model with raw api_key",
			config: `
resource "truefoundry_provider_account" "bad" {
  name          = "spike-bad"
  provider_type = "azure-foundry"
  models = [
    {
      name           = "phi-4"
      model_id       = "phi-4-deployment"
      azure_endpoint = "https://phi-4.eastus.models.ai.azure.com"
      api_key        = "raw-secret-key"
      model_types    = ["chat"]
    },
  ]
}
`,
			want: regexp.MustCompile("`api_key` of model \"phi-4\" must be a TrueFoundry secret reference"),
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
			want: regexp.MustCompile("`api_key` of model \"phi-4\" is required"),
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
			want: regexp.MustCompile("`deployment_type` of model \"gpt-4o\" must not be set"),
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
			want: regexp.MustCompile("Model \"gpt-4o\": exactly one of `value` \\(scalar metrics\\) or `input`"),
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
			want: regexp.MustCompile("`region` of model \"gpt-4o\" must not be set"),
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
