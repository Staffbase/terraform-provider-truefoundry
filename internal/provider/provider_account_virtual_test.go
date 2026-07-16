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
					resource.TestCheckResourceAttr("truefoundry_provider_account.virtual", "models.0.routing.type", "weight-based-routing"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.virtual", "models.0.routing.targets.#", "2"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.virtual", "models.0.routing.targets.1.weight", "3"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.virtual", "models.0.routing.targets.0.metadata_match.tfy_gateway_cluster", "cluster-a"),
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
					resource.TestCheckResourceAttr("truefoundry_provider_account.virtual", "models.0.slug", "gpt-5-mini-lb"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.virtual", "models.0.routing.targets.0.weight", "2"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.virtual", "models.1.routing.type", "priority-based-routing"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.virtual", "models.1.routing.targets.0.priority", "0"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.virtual", "models.1.routing.targets.0.fallback_status_codes.0", "429"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.virtual", "models.1.routing.targets.1.fallback_candidate", "true"),
				),
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
			want: regexp.MustCompile("routing` is required"),
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
			want: regexp.MustCompile("model_id` must not be set"),
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
			want: regexp.MustCompile("priority` must not be set"),
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
			want: regexp.MustCompile("weight` is required"),
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
			want: regexp.MustCompile("routing` must not be set"),
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
