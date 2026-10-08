package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// mlRepoStorageConfig is the blob storage account backing the ML repos under
// test; the control plane rejects repos referencing unknown storage
// integrations.
const mlRepoStorageConfig = `
resource "truefoundry_azure_account" "storage" {
  name = "spike-blobstorage"

  blob_storages = [
    {
      name              = "artifacts"
      storage_root      = "https://artifacts.blob.core.windows.net/artifacts/tfy"
      connection_string = "tenant:hashicorp:vault-secrets:secret-store:vault::blobstorage/artifacts/value"
    },
  ]
}
`

const mlRepoConfig = mlRepoStorageConfig + `
resource "truefoundry_ml_repo" "artifacts" {
  name                    = "gateway-artifacts"
  description             = "Skills and prompts of the AI gateway"
  storage_integration_fqn = "${truefoundry_azure_account.storage.fqn}:blob-storage:artifacts"

  collaborators = [
    {
      subject = "team:everyone"
      role_id = "mlf-project-admin"
    },
  ]
}
`

func TestAccMlRepo_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.MlRepoCount(); n != 0 {
				return fmt.Errorf("%d ML repos left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + mlRepoConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_ml_repo.artifacts", "id"),
					resource.TestCheckResourceAttr("truefoundry_ml_repo.artifacts", "name", "gateway-artifacts"),
					resource.TestCheckResourceAttr("truefoundry_ml_repo.artifacts", "description", "Skills and prompts of the AI gateway"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_ml_repo.artifacts", "collaborators.*", map[string]string{
						"subject": "team:everyone",
						"role_id": "mlf-project-admin",
					}),
				),
			},
			// In-place update: change the description, drop the collaborators
			// (which leaves the grant on the control plane, untracked).
			{
				Config: providerConfig(host) + mlRepoStorageConfig + `
resource "truefoundry_ml_repo" "artifacts" {
  name                    = "gateway-artifacts"
  description             = "Gateway artifacts"
  storage_integration_fqn = "${truefoundry_azure_account.storage.fqn}:blob-storage:artifacts"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_ml_repo.artifacts", "description", "Gateway artifacts"),
					resource.TestCheckNoResourceAttr("truefoundry_ml_repo.artifacts", "collaborators"),
				),
			},
			// Out-of-band deletion is detected and the repo is recreated.
			{
				PreConfig: func() { mock.DeleteMlRepoByName("gateway-artifacts") },
				Config:    providerConfig(host) + mlRepoConfig,
				Check:     resource.TestCheckResourceAttr("truefoundry_ml_repo.artifacts", "name", "gateway-artifacts"),
			},
		},
	})
}

func TestAccMlRepo_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + mlRepoConfig},
			{
				ResourceName:      "truefoundry_ml_repo.artifacts",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "gateway-artifacts",
			},
		},
	})
}

func TestAccMlRepo_validation(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			// Upper case is legal in ML repo names (and only there), an
			// underscore is not.
			name: "invalid name",
			config: `
resource "truefoundry_ml_repo" "bad" {
  name                    = "Spike_Bad"
  storage_integration_fqn = "mock-tenant:azure:none:blob-storage:none"
}
`,
			want: regexp.MustCompile("must start with a letter"),
		},
		{
			// The control plane's referential check: the storage integration
			// must exist.
			name: "unknown storage integration",
			config: `
resource "truefoundry_ml_repo" "bad" {
  name                    = "spike-bad"
  storage_integration_fqn = "mock-tenant:azure:none:blob-storage:none"
}
`,
			want: regexp.MustCompile(`not\s+found`),
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
