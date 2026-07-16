package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

// importNotFoundRegexp matches Terraform's error when the post-import Read
// finds no remote object.
var importNotFoundRegexp = regexp.MustCompile(`Cannot import non-existent remote object`)

// Models are named in alphabetical order on purpose: on import the provider
// has no prior state, so it appends remote models sorted by name, and
// ImportStateVerify compares attribute-for-attribute against the created
// state.
const importAccountConfig = `
resource "truefoundry_provider_account" "imp" {
  name           = "spike-import"
  provider_type  = "azure-openai"
  api_key        = "tenant:hashicorp:vault-secrets:secret-store:vault::models/import/value"
  azure_endpoint = "https://spike-import.openai.azure.com"
  models = [
    {
      name        = "embeddings"
      model_id    = "text-embedding-3-large-deployment"
      api_version = "2024-10-21"
      model_types = ["embedding"]
    },
    {
      name            = "gpt-4o"
      model_id        = "gpt-4o-deployment"
      api_version     = "2024-10-21"
      deployment_type = "datazone_eu"
      region          = "francecentral"
      model_types     = ["chat"]
      cost = {
        metric = "per_1000_tokens"
        input  = 0.0022
        output = 0.0088
      }
    },
  ]
}
`

func TestAccProviderAccount_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + importAccountConfig},
			{
				ResourceName:      "truefoundry_provider_account.imp",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs, ok := s.RootModule().Resources["truefoundry_provider_account.imp"]
					if !ok {
						return "", fmt.Errorf("resource not found in state")
					}
					return rs.Primary.Attributes["fqn"], nil
				},
			},
		},
	})
}

func TestAccProviderAccount_importAzureFoundry(t *testing.T) {
	_, host := startMockControlPlane(t)

	config := providerConfig(host) + `
resource "truefoundry_provider_account" "imp" {
  name          = "spike-import-foundry"
  provider_type = "azure-foundry"
  models = [
    {
      name           = "phi-4"
      model_id       = "phi-4-deployment"
      azure_endpoint = "https://phi-4.eastus.models.ai.azure.com"
      api_key        = "tenant:hashicorp:vault-secrets:secret-store:vault::models/phi/value"
      model_types    = ["chat"]
    },
  ]
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				ResourceName:      "truefoundry_provider_account.imp",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     mockcp.Tenant + ":azure-foundry:spike-import-foundry",
			},
		},
	})
}

func TestAccProviderAccount_importUnknownFQNFails(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + importAccountConfig},
			{
				ResourceName:  "truefoundry_provider_account.imp",
				ImportState:   true,
				ImportStateId: mockcp.Tenant + ":azure-openai:does-not-exist",
				ExpectError:   importNotFoundRegexp,
			},
		},
	})
}
