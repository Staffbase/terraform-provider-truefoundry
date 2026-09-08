package provider_test

import (
	"fmt"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
	"github.com/Staffbase/terraform-provider-truefoundry/internal/provider"
)

const testAPIKey = "tfy-test-token"

// replacePlanCheck asserts that the plan replaces the given resource.
func replacePlanCheck(addr string) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(addr, plancheck.ResourceActionReplace),
		},
	}
}

var protoV6Factories = map[string]func() (tfprotov6.ProviderServer, error){
	"truefoundry": providerserver.NewProtocol6WithError(provider.New("test")()),
}

// startMockControlPlane starts an in-memory TrueFoundry control plane and
// returns it with its base URL.
func startMockControlPlane(t *testing.T) (*mockcp.Server, string) {
	t.Helper()
	mock := mockcp.New(testAPIKey)
	srv := httptest.NewServer(mock.Handler())
	t.Cleanup(srv.Close)
	return mock, srv.URL
}

func providerConfig(host string) string {
	return fmt.Sprintf(`
provider "truefoundry" {
  host    = %q
  api_key = %q
}
`, host, testAPIKey)
}

func TestAccProviderAccount_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	checkDestroyed := func(*terraform.State) error {
		if n := mock.AccountCount(); n != 0 {
			return fmt.Errorf("%d provider accounts left on control plane after destroy, want 0", n)
		}
		return nil
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy:             checkDestroyed,
		Steps: []resource.TestStep{
			// Create with a single model.
			{
				Config: providerConfig(host) + `
resource "truefoundry_provider_account" "openai" {
  name    = "spike-openai"
  api_key = "sk-upstream-secret"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat"]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_provider_account.openai", "id"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.openai", "fqn", mockcp.Tenant+":openai:spike-openai"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.openai", "provider_type", "openai"),
					resource.TestCheckResourceAttr("truefoundry_provider_account.openai", "models.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.openai", "models.*", map[string]string{
						"name":          "gpt-4o",
						"model_id":      "gpt-4o",
						"model_types.#": "1",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_provider_account.openai", "models.*.model_types.*", "chat"),
				),
			},
			// In-place update: add a second model and a base URL.
			{
				Config: providerConfig(host) + `
resource "truefoundry_provider_account" "openai" {
  name     = "spike-openai"
  api_key  = "sk-upstream-secret"
  base_url = "https://api.openai.example.com"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat", "responses"]
    },
    {
      name        = "text-embedding-3-small"
      model_id    = "text-embedding-3-small"
      model_types = ["embedding"]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_provider_account.openai", "models.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.openai", "models.*", map[string]string{
						"name":          "gpt-4o",
						"model_id":      "gpt-4o",
						"model_types.#": "2",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_provider_account.openai", "models.*", map[string]string{
						"name":          "text-embedding-3-small",
						"model_id":      "text-embedding-3-small",
						"model_types.#": "1",
					}),
					resource.TestCheckResourceAttr("truefoundry_provider_account.openai", "base_url", "https://api.openai.example.com"),
					func(*terraform.State) error {
						if n := mock.AccountCount(); n != 1 {
							return fmt.Errorf("control plane has %d accounts, want 1", n)
						}
						return nil
					},
				),
			},
			// Renaming the account forces replacement.
			{
				Config: providerConfig(host) + `
resource "truefoundry_provider_account" "openai" {
  name    = "spike-openai-two"
  api_key = "sk-upstream-secret"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat"]
    },
  ]
}
`,
				ConfigPlanChecks: replacePlanCheck("truefoundry_provider_account.openai"),
				Check: resource.TestCheckResourceAttr(
					"truefoundry_provider_account.openai", "fqn", mockcp.Tenant+":openai:spike-openai-two",
				),
			},
		},
	})
}

func TestAccProviderAccount_recreatedAfterExternalDeletion(t *testing.T) {
	mock, host := startMockControlPlane(t)
	config := providerConfig(host) + `
resource "truefoundry_provider_account" "openai" {
  name    = "spike-drift"
  api_key = "sk-upstream-secret"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat"]
    },
  ]
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: config},
			// Delete the account behind Terraform's back: refresh must detect
			// the missing resource and the plan must recreate it.
			{
				PreConfig: func() { mock.DeleteAccount("spike-drift") },
				Config:    config,
				Check: func(*terraform.State) error {
					if n := mock.AccountCount(); n != 1 {
						return fmt.Errorf("account was not recreated, control plane has %d accounts", n)
					}
					return nil
				},
			},
		},
	})
}

func TestAccProviderAccount_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "no models",
			config: `
resource "truefoundry_provider_account" "bad" {
  name    = "spike-bad"
  api_key = "sk"
  models  = []
}
`,
			want: regexp.MustCompile(`at least 1 elements`),
		},
		{
			name: "invalid model type",
			config: `
resource "truefoundry_provider_account" "bad" {
  name    = "spike-bad"
  api_key = "sk"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["telepathy"]
    },
  ]
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "uppercase name",
			config: `
resource "truefoundry_provider_account" "bad" {
  name    = "Spike-Bad"
  api_key = "sk"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat"]
    },
  ]
}
`,
			want: regexp.MustCompile(`must start with a lowercase letter`),
		},
		{
			// The control plane caps non-virtual-model account names at 32.
			name: "name too long for provider type",
			config: `
resource "truefoundry_provider_account" "bad" {
  name    = "abbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbc"
  api_key = "sk"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat"]
    },
  ]
}
`,
			want: regexp.MustCompile(`at most 32 characters`),
		},
		{
			// virtual-model allows 64, but not 65.
			name: "virtual model name too long",
			config: `
resource "truefoundry_provider_account" "bad" {
  provider_type = "virtual-model"
  name          = "abbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbc"
  models = [
    {
      name        = "router"
      model_types = ["chat"]
      routing = {
        type    = "weight-based-routing"
        targets = [{ target = "openai-main/gpt-4o", weight = 100 }]
      }
    },
  ]
}
`,
			want: regexp.MustCompile(`string length must be between 3 and 64`),
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

func TestAccProvider_envVarAuthentication(t *testing.T) {
	_, host := startMockControlPlane(t)
	t.Setenv("TFY_HOST", host)
	t.Setenv("TFY_API_KEY", testAPIKey)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				// No host/api_key in the provider block: taken from TFY_* env.
				Config: `
provider "truefoundry" {}

resource "truefoundry_provider_account" "openai" {
  name    = "spike-env"
  api_key = "sk-upstream-secret"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat"]
    },
  ]
}
`,
				Check: resource.TestCheckResourceAttrSet("truefoundry_provider_account.openai", "id"),
			},
		},
	})
}

func TestAccProvider_wrongTokenFails(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
provider "truefoundry" {
  host    = %q
  api_key = "wrong-token"
}

resource "truefoundry_provider_account" "openai" {
  name    = "spike-unauth"
  api_key = "sk"
  models = [
    {
      name        = "gpt-4o"
      model_id    = "gpt-4o"
      model_types = ["chat"]
    },
  ]
}
`, host),
				ExpectError: regexp.MustCompile(`HTTP 401`),
			},
		},
	})
}
