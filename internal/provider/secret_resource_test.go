package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

// secretConfig puts two secrets into one group. Terraform creates them in
// parallel, so this also exercises the per-group write lock: without it, the
// second write would drop the first secret.
func secretConfig(roleID, secretID string) string {
	return fmt.Sprintf(`
resource "truefoundry_system_secret_store" "system" {
  name = "system-secret-store"

  secret_stores = [
    { name = "system-secret-store" },
  ]
}

resource "truefoundry_secret_group" "system" {
  name            = "system"
  integration_fqn = "${truefoundry_system_secret_store.system.fqn}:secret-store:system-secret-store"
}

resource "truefoundry_secret" "role_id" {
  secret_group_id = truefoundry_secret_group.system.id
  key             = "vault-role-id"
  value           = %q
}

resource "truefoundry_secret" "secret_id" {
  secret_group_id = truefoundry_secret_group.system.id
  key             = "vault-secret-id"
  value           = %q
}
`, roleID, secretID)
}

// checkSecret asserts a secret's value and version on the mock.
func checkSecret(mock *mockcp.Server, key, wantValue string, wantVersion int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		value, version, ok := mock.SecretValue("system", key)
		if !ok {
			return fmt.Errorf("secret %q missing on the control plane", key)
		}
		if value != wantValue || version != wantVersion {
			return fmt.Errorf("secret %q = %q (version %d), want %q (version %d)", key, value, version, wantValue, wantVersion)
		}
		return nil
	}
}

func TestAccSecret_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.SecretCount(); n != 0 {
				return fmt.Errorf("%d secrets left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + secretConfig("role-1", "secret-1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_secret.role_id", "id"),
					resource.TestCheckResourceAttr("truefoundry_secret.role_id", "fqn", mockcp.Tenant+":system:vault-role-id"),
					resource.TestCheckResourceAttr("truefoundry_secret.secret_id", "fqn", mockcp.Tenant+":system:vault-secret-id"),
					resource.TestCheckResourceAttrPair("truefoundry_secret.role_id", "secret_group_id", "truefoundry_secret_group.system", "id"),
					checkSecret(mock, "vault-role-id", "role-1", 1),
					checkSecret(mock, "vault-secret-id", "secret-1", 1),
				),
			},
			{
				// Rotating one value updates it in place and leaves the other
				// secret's version alone.
				Config: providerConfig(host) + secretConfig("role-1", "secret-2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("truefoundry_secret.secret_id", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("truefoundry_secret.role_id", plancheck.ResourceActionNoop),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					checkSecret(mock, "vault-role-id", "role-1", 1),
					checkSecret(mock, "vault-secret-id", "secret-2", 2),
				),
			},
		},
	})
}

// Secrets created outside Terraform share the group and must survive every
// write, since the control plane deletes keys omitted from an update.
func TestAccSecret_keepsOutOfBandSecrets(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + secretConfig("role-1", "secret-1")},
			{
				PreConfig: func() { mock.AddSecret("system", "from-the-ui", "keep-me") },
				Config:    providerConfig(host) + secretConfig("role-2", "secret-1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkSecret(mock, "from-the-ui", "keep-me", 1),
					checkSecret(mock, "vault-role-id", "role-2", 2),
				),
			},
		},
	})
}

func TestAccSecret_valueDriftReverted(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + secretConfig("role-1", "secret-1")},
			{
				PreConfig: func() { mock.SetSecretValue("system", "vault-role-id", "changed-in-the-ui") },
				Config:    providerConfig(host) + secretConfig("role-1", "secret-1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("truefoundry_secret.role_id", plancheck.ResourceActionUpdate),
					},
				},
				Check: checkSecret(mock, "vault-role-id", "role-1", 3),
			},
		},
	})
}

// With secret values hidden, an out-of-band change cannot be seen and the
// plan stays empty.
func TestAccSecret_hiddenValuesKeepState(t *testing.T) {
	mock, host := startMockControlPlane(t)
	mock.HideSecretValues()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + secretConfig("role-1", "secret-1")},
			{
				PreConfig: func() { mock.SetSecretValue("system", "vault-role-id", "changed-in-the-ui") },
				Config:    providerConfig(host) + secretConfig("role-1", "secret-1"),
				PlanOnly:  true,
			},
		},
	})
}

func TestAccSecret_deletedOutOfBandRecreated(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + secretConfig("role-1", "secret-1")},
			{
				PreConfig: func() { mock.DeleteSecretByKey("system", "vault-role-id") },
				Config:    providerConfig(host) + secretConfig("role-1", "secret-1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkSecret(mock, "vault-role-id", "role-1", 1),
					checkSecret(mock, "vault-secret-id", "secret-1", 1),
				),
			},
		},
	})
}

// Deleting the group takes its secrets with it; both come back on the next
// apply.
func TestAccSecret_groupDeletedRecreated(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + secretConfig("role-1", "secret-1")},
			{
				PreConfig: func() { mock.DeleteSecretGroupByName("system") },
				Config:    providerConfig(host) + secretConfig("role-1", "secret-1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkSecret(mock, "vault-role-id", "role-1", 1),
					checkSecret(mock, "vault-secret-id", "secret-1", 1),
				),
			},
		},
	})
}

func TestAccSecret_replaceOnKeyChange(t *testing.T) {
	mock, host := startMockControlPlane(t)

	renamed := regexp.MustCompile(`"vault-role-id"`).ReplaceAllString(secretConfig("role-1", "secret-1"), `"role-id"`)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + secretConfig("role-1", "secret-1")},
			{
				Config:           providerConfig(host) + renamed,
				ConfigPlanChecks: replacePlanCheck("truefoundry_secret.role_id"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_secret.role_id", "fqn", mockcp.Tenant+":system:role-id"),
					checkSecret(mock, "role-id", "role-1", 1),
					func(*terraform.State) error {
						if _, _, ok := mock.SecretValue("system", "vault-role-id"); ok {
							return fmt.Errorf("old secret vault-role-id still exists after replacement")
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccSecret_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + secretConfig("role-1", "secret-1")},
			{
				ResourceName:      "truefoundry_secret.role_id",
				ImportState:       true,
				ImportStateId:     mockcp.Tenant + ":system:vault-role-id",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccSecret_importHiddenValue(t *testing.T) {
	mock, host := startMockControlPlane(t)
	mock.HideSecretValues()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + secretConfig("role-1", "secret-1")},
			{
				ResourceName:            "truefoundry_secret.role_id",
				ImportState:             true,
				ImportStateId:           mockcp.Tenant + ":system:vault-role-id",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"value"},
			},
		},
	})
}

func TestAccSecret_invalidImportID(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := map[string]*regexp.Regexp{
		"system":                         regexp.MustCompile(`expected the secret FQN <tenant>:<group>:<key>`),
		mockcp.Tenant + ":system:":       regexp.MustCompile(`expected the secret FQN <tenant>:<group>:<key>`),
		mockcp.Tenant + ":nope:some-key": regexp.MustCompile(`no secret group with FQN "mock-tenant:nope"`),
	}
	for id, want := range cases {
		t.Run(id, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6Factories,
				Steps: []resource.TestStep{
					{Config: providerConfig(host) + secretConfig("role-1", "secret-1")},
					{
						ResourceName:  "truefoundry_secret.role_id",
						ImportState:   true,
						ImportStateId: id,
						ExpectError:   want,
					},
				},
			})
		})
	}
}
