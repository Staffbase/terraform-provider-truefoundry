package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

// Mirrors the live dev setup: a blob storage container with connection-string
// auth via a Vault secret reference.
const azureAccountConfig = `
resource "truefoundry_azure_account" "storage" {
  name = "spike-blobstorage"

  blob_storages = [
    {
      name              = "devde1truefoundrydevelop"
      storage_root      = "https://devde1truefoundrydevelop.blob.core.windows.net/devde1truefoundrydevelop/tfy-managed-storage/staffbase"
      connection_string = "tenant:hashicorp:vault-secrets:secret-store:vault::blobstorage/devde1truefoundrydevelop/value"
    },
  ]
}
`

func TestAccAzureAccount_lifecycle(t *testing.T) {
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
				Config: providerConfig(host) + azureAccountConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_azure_account.storage", "fqn", mockcp.Tenant+":azure:spike-blobstorage"),
					resource.TestCheckResourceAttr("truefoundry_azure_account.storage", "blob_storages.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_azure_account.storage", "blob_storages.*", map[string]string{
						"name":              "devde1truefoundrydevelop",
						"connection_string": "tenant:hashicorp:vault-secrets:secret-store:vault::blobstorage/devde1truefoundrydevelop/value",
					}),
					resource.TestCheckNoResourceAttr("truefoundry_azure_account.storage", "subscription_id"),
				),
			},
			// In-place update: add subscription_id, a second container without
			// auth, and authorized subjects.
			{
				Config: providerConfig(host) + `
resource "truefoundry_azure_account" "storage" {
  name            = "spike-blobstorage"
  subscription_id = "00000000-1111-2222-3333-444444444444"

  blob_storages = [
    {
      name              = "devde1truefoundrydevelop"
      storage_root      = "https://devde1truefoundrydevelop.blob.core.windows.net/devde1truefoundrydevelop/tfy-managed-storage/staffbase"
      connection_string = "tenant:hashicorp:vault-secrets:secret-store:vault::blobstorage/devde1truefoundrydevelop/value"
    },
    {
      name                = "public-assets"
      storage_root        = "https://publicassets.blob.core.windows.net/assets"
      authorized_subjects = ["team:everyone"]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_azure_account.storage", "subscription_id", "00000000-1111-2222-3333-444444444444"),
					resource.TestCheckResourceAttr("truefoundry_azure_account.storage", "blob_storages.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_azure_account.storage", "blob_storages.*", map[string]string{
						"name":              "devde1truefoundrydevelop",
						"connection_string": "tenant:hashicorp:vault-secrets:secret-store:vault::blobstorage/devde1truefoundrydevelop/value",
					}),
					// The second container has authorized subjects but no
					// connection string (absence is not addressable in sets).
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_azure_account.storage", "blob_storages.*", map[string]string{
						"name":                  "public-assets",
						"storage_root":          "https://publicassets.blob.core.windows.net/assets",
						"authorized_subjects.#": "1",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_azure_account.storage", "blob_storages.*.authorized_subjects.*", "team:everyone"),
				),
			},
			// Renaming replaces the account.
			{
				Config: providerConfig(host) + `
resource "truefoundry_azure_account" "storage" {
  name = "spike-blobstorage-two"

  blob_storages = [
    {
      name         = "devde1truefoundrydevelop"
      storage_root = "https://devde1truefoundrydevelop.blob.core.windows.net/devde1truefoundrydevelop/tfy-managed-storage/staffbase"
    },
  ]
}
`,
				ConfigPlanChecks: replacePlanCheck("truefoundry_azure_account.storage"),
			},
		},
	})
}

func TestAccAzureAccount_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + azureAccountConfig},
			{
				ResourceName:      "truefoundry_azure_account.storage",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     mockcp.Tenant + ":azure:spike-blobstorage",
			},
		},
	})
}

func TestAccAzureAccount_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "no blob storages",
			config: `
resource "truefoundry_azure_account" "bad" {
  name          = "spike-bad"
  blob_storages = []
}
`,
			want: regexp.MustCompile(`at least 1 elements`),
		},
		{
			name: "missing storage root",
			config: `
resource "truefoundry_azure_account" "bad" {
  name = "spike-bad"
  blob_storages = [
    { name = "b1" },
  ]
}
`,
			want: regexp.MustCompile(`storage_root`),
		},
		{
			name: "invalid name",
			config: `
resource "truefoundry_azure_account" "bad" {
  name = "Spike_Bad"
  blob_storages = [
    { name = "b1", storage_root = "https://a.blob.core.windows.net/c" },
  ]
}
`,
			want: regexp.MustCompile(`lowercase alphanumeric`),
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
