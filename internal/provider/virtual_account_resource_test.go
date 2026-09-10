package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccVirtualAccount_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.VirtualAccountCount(); n != 0 {
				return fmt.Errorf("%d virtual accounts left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + `
resource "truefoundry_virtual_account" "ci" {
  name = "spike-ci-bot"
  permissions = [
    {
      resource_fqn  = "mock-tenant:azure-openai:spike-azure"
      resource_type = "provider-account"
      role_id       = "provider-account-access"
    },
  ]
  token_type = "jwt"
  tags       = { env = "dev" }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_virtual_account.ci", "id"),
					resource.TestCheckResourceAttrSet("truefoundry_virtual_account.ci", "token"),
					resource.TestCheckResourceAttr("truefoundry_virtual_account.ci", "permissions.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_virtual_account.ci", "permissions.*", map[string]string{
						"resource_fqn": "mock-tenant:azure-openai:spike-azure",
						"role_id":      "provider-account-access",
					}),
					resource.TestCheckResourceAttr("truefoundry_virtual_account.ci", "tags.env", "dev"),
				),
			},
			// In-place update: extra permission, expiration date, and a
			// secret store sync. The token must stay stable.
			{
				Config: providerConfig(host) + `
resource "truefoundry_virtual_account" "ci" {
  name = "spike-ci-bot"
  permissions = [
    {
      resource_fqn  = "mock-tenant:azure-openai:spike-azure"
      resource_type = "provider-account"
      role_id       = "provider-account-access"
    },
    {
      resource_fqn  = "mock-tenant:virtual-model:spike-virtual"
      resource_type = "provider-account"
      role_id       = "provider-account-access"
    },
  ]
  expiration_date = "2027-01-31"
  token_type      = "jwt"
  tags            = { env = "dev" }
  secret_store_config = {
    integration_fqn = "mock-tenant:hashicorp:vault-secrets:secret-store:vault"
    secret_path     = "tfy/virtualaccounts/spike-ci-bot/value"
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_virtual_account.ci", "permissions.#", "2"),
					resource.TestCheckResourceAttr("truefoundry_virtual_account.ci", "expiration_date", "2027-01-31"),
					resource.TestCheckResourceAttr("truefoundry_virtual_account.ci", "secret_store_config.secret_path", "tfy/virtualaccounts/spike-ci-bot/value"),
					resource.TestCheckResourceAttr("truefoundry_virtual_account.ci", "token", "mock-token-va-1"),
				),
			},
			// Renaming replaces the account (and thus the token).
			{
				Config: providerConfig(host) + `
resource "truefoundry_virtual_account" "ci" {
  name = "spike-ci-bot-two"
  permissions = [
    {
      resource_fqn  = "mock-tenant:azure-openai:spike-azure"
      resource_type = "provider-account"
      role_id       = "provider-account-access"
    },
  ]
}
`,
				ConfigPlanChecks: replacePlanCheck("truefoundry_virtual_account.ci"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_virtual_account.ci", "token", "mock-token-va-2"),
					func(*terraform.State) error {
						if n := mock.VirtualAccountCount(); n != 1 {
							return fmt.Errorf("control plane has %d virtual accounts, want 1", n)
						}
						return nil
					},
				),
			},
		},
	})
}

// TestAccVirtualAccount_tokenNotRefetchedWithoutAutoRotate asserts that the
// token endpoint is only hit on read when auto_rotate is set (or the token
// is unknown, e.g. import), so a plan over many virtual accounts does not
// double its request count.
func TestAccVirtualAccount_tokenNotRefetchedWithoutAutoRotate(t *testing.T) {
	mock, host := startMockControlPlane(t)

	config := providerConfig(host) + `
resource "truefoundry_virtual_account" "ci" {
  name = "spike-token-gate-bot"
  permissions = [
    {
      resource_fqn  = "mock-tenant:azure-openai:spike-azure"
      resource_type = "provider-account"
      role_id       = "provider-account-access"
    },
  ]
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				// Create issues one token read as a fallback path only if the
				// apply response omits the token; the mock always includes
				// it, so no token-endpoint request is expected yet.
				Config: config,
			},
			{
				// A second plan/apply with no config change still refreshes
				// via Read. Without auto_rotate, the token endpoint must stay
				// untouched.
				Config: config,
				Check: func(*terraform.State) error {
					if n := mock.VirtualAccountTokenReadCount(); n != 0 {
						return fmt.Errorf("token endpoint read %d times without auto_rotate, want 0", n)
					}
					return nil
				},
			},
		},
	})
}

// TestAccVirtualAccount_autoRotateForcesTokenRefresh asserts that auto_rotate
// always refetches the token on read.
func TestAccVirtualAccount_autoRotateForcesTokenRefresh(t *testing.T) {
	mock, host := startMockControlPlane(t)

	config := providerConfig(host) + `
resource "truefoundry_virtual_account" "ci" {
  name = "spike-auto-rotate-bot"
  permissions = [
    {
      resource_fqn  = "mock-tenant:azure-openai:spike-azure"
      resource_type = "provider-account"
      role_id       = "provider-account-access"
    },
  ]
  auto_rotate = {
    interval_days     = 90
    grace_period_days = 15
  }
}
`
	var afterCreate int
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_virtual_account.ci", "auto_rotate.interval_days", "90"),
					resource.TestCheckResourceAttr("truefoundry_virtual_account.ci", "auto_rotate.grace_period_days", "15"),
					func(*terraform.State) error {
						afterCreate = mock.VirtualAccountTokenReadCount()
						return nil
					},
				),
			},
			{
				// A second plan/apply with no config change still refreshes
				// via Read. auto_rotate must force the token endpoint to be
				// hit even though nothing else changed — assert the counter
				// increases from its post-create value, not just that it's
				// nonzero (Create can itself fall back to the token endpoint
				// when the apply response omits the token).
				Config: config,
				Check: func(*terraform.State) error {
					if n := mock.VirtualAccountTokenReadCount(); n <= afterCreate {
						return fmt.Errorf("token endpoint read count %d did not increase from %d with auto_rotate set", n, afterCreate)
					}
					return nil
				},
			},
		},
	})
}

func TestAccVirtualAccount_recreatedAfterExternalDeletion(t *testing.T) {
	mock, host := startMockControlPlane(t)
	config := providerConfig(host) + `
resource "truefoundry_virtual_account" "ci" {
  name = "spike-drift-bot"
  permissions = [
    {
      resource_fqn  = "mock-tenant:azure-openai:spike-azure"
      resource_type = "provider-account"
      role_id       = "provider-account-access"
    },
  ]
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() { mock.DeleteVirtualAccountByName("spike-drift-bot") },
				Config:    config,
				Check: func(*terraform.State) error {
					if n := mock.VirtualAccountCount(); n != 1 {
						return fmt.Errorf("virtual account was not recreated, control plane has %d", n)
					}
					return nil
				},
			},
		},
	})
}

func TestAccVirtualAccount_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	config := providerConfig(host) + `
resource "truefoundry_virtual_account" "ci" {
  name = "spike-import-bot"
  permissions = [
    {
      resource_fqn  = "mock-tenant:azure-openai:spike-azure"
      resource_type = "provider-account"
      role_id       = "provider-account-access"
    },
  ]
  token_type = "opaque"
  tags       = { env = "dev" }
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				ResourceName:      "truefoundry_virtual_account.ci",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs, ok := s.RootModule().Resources["truefoundry_virtual_account.ci"]
					if !ok {
						return "", fmt.Errorf("resource not found in state")
					}
					return rs.Primary.Attributes["id"], nil
				},
			},
		},
	})
}

func TestAccVirtualAccount_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "no permissions",
			config: `
resource "truefoundry_virtual_account" "bad" {
  name        = "spike-bad-bot"
  permissions = []
}
`,
			want: regexp.MustCompile(`at least 1 elements`),
		},
		{
			name: "invalid token type",
			config: `
resource "truefoundry_virtual_account" "bad" {
  name = "spike-bad-bot"
  permissions = [
    { resource_fqn = "a", resource_type = "provider-account", role_id = "r" },
  ]
  token_type = "paseto"
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "invalid expiration date",
			config: `
resource "truefoundry_virtual_account" "bad" {
  name = "spike-bad-bot"
  permissions = [
    { resource_fqn = "a", resource_type = "provider-account", role_id = "r" },
  ]
  expiration_date = "31.01.2027"
}
`,
			want: regexp.MustCompile(`yyyy-mm-dd`),
		},
		{
			name: "auto_rotate interval too short",
			config: `
resource "truefoundry_virtual_account" "bad" {
  name = "spike-bad-bot"
  permissions = [
    { resource_fqn = "a", resource_type = "provider-account", role_id = "r" },
  ]
  auto_rotate = {
    interval_days     = 10
    grace_period_days = 5
  }
}
`,
			want: regexp.MustCompile(`at least 30`),
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
