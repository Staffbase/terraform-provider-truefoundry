package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

// Mirrors the live dev setup: a Vault KV store with AppRole auth, the role
// credentials themselves referencing TrueFoundry system secrets.
const hashicorpAccountConfig = `
resource "truefoundry_hashicorp_account" "secrets" {
  name = "spike-vault-secrets"

  vaults = [
    {
      name          = "vault"
      vault_url     = "https://vault.staffbase.com"
      kv_mount_path = "de1/dev"
      root_path     = "tfy-control-plane"
      approle = {
        role_id   = "tfy-secret://tenant:system:vault-role-id"
        secret_id = "tfy-secret://tenant:system:vault-secret-id"
      }
    },
  ]
}
`

func TestAccHashicorpAccount_lifecycle(t *testing.T) {
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
				Config: providerConfig(host) + hashicorpAccountConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_hashicorp_account.secrets", "fqn", mockcp.Tenant+":hashicorp:spike-vault-secrets"),
					resource.TestCheckResourceAttr("truefoundry_hashicorp_account.secrets", "vaults.#", "1"),
					resource.TestCheckResourceAttr("truefoundry_hashicorp_account.secrets", "vaults.0.approle.role_id", "tfy-secret://tenant:system:vault-role-id"),
					resource.TestCheckNoResourceAttr("truefoundry_hashicorp_account.secrets", "vaults.0.token"),
				),
			},
			// In-place update: switch to token auth, add namespace and a
			// second store with authorized subjects.
			{
				Config: providerConfig(host) + `
resource "truefoundry_hashicorp_account" "secrets" {
  name = "spike-vault-secrets"

  vaults = [
    {
      name          = "vault"
      vault_url     = "https://vault.staffbase.com"
      kv_mount_path = "de1/dev"
      root_path     = "tfy-control-plane"
      namespace     = "team-a"
      token         = "hvs.XXXXXXXXXXXXXXXXXXXX"
    },
    {
      name                = "vault-eu"
      vault_url           = "https://vault-eu.staffbase.com"
      kv_mount_path       = "eu1/dev"
      token               = "hvs.YYYYYYYYYYYYYYYYYYYY"
      authorized_subjects = ["team:everyone"]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_hashicorp_account.secrets", "vaults.#", "2"),
					resource.TestCheckResourceAttr("truefoundry_hashicorp_account.secrets", "vaults.0.namespace", "team-a"),
					resource.TestCheckNoResourceAttr("truefoundry_hashicorp_account.secrets", "vaults.0.approle"),
					resource.TestCheckResourceAttr("truefoundry_hashicorp_account.secrets", "vaults.1.authorized_subjects.0", "team:everyone"),
				),
			},
		},
	})
}

func TestAccHashicorpAccount_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + hashicorpAccountConfig},
			{
				ResourceName:      "truefoundry_hashicorp_account.secrets",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     mockcp.Tenant + ":hashicorp:spike-vault-secrets",
			},
		},
	})
}

func TestAccHashicorpAccount_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "no auth",
			config: `
resource "truefoundry_hashicorp_account" "bad" {
  name = "spike-bad"
  vaults = [
    {
      name          = "v1"
      vault_url     = "https://vault.example.com"
      kv_mount_path = "secret"
    },
  ]
}
`,
			want: regexp.MustCompile("Exactly one of `token` and `approle`"),
		},
		{
			name: "both auth methods",
			config: `
resource "truefoundry_hashicorp_account" "bad" {
  name = "spike-bad"
  vaults = [
    {
      name          = "v1"
      vault_url     = "https://vault.example.com"
      kv_mount_path = "secret"
      token         = "hvs.XXXX"
      approle = {
        role_id   = "role"
        secret_id = "secret"
      }
    },
  ]
}
`,
			want: regexp.MustCompile("Exactly one of `token` and `approle`"),
		},
		{
			name: "missing kv mount path",
			config: `
resource "truefoundry_hashicorp_account" "bad" {
  name = "spike-bad"
  vaults = [
    {
      name      = "v1"
      vault_url = "https://vault.example.com"
      token     = "hvs.XXXX"
    },
  ]
}
`,
			want: regexp.MustCompile(`kv_mount_path`),
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
