package provider_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// One subject, many permissions: the shape a migrated virtual account takes.
const roleBindingConfig = `
resource "truefoundry_role_binding" "ci" {
  name = "va-ci-bot"

  subjects = [
    { type = "virtualaccount", name = "ci-bot" },
  ]

  permissions = [
    {
      resource_type = "provider-account"
      resource_fqn  = "mock-tenant:azure-openai:spike-azure"
      role          = "provider-account-access"
    },
    {
      resource_type = "mcp-server"
      resource_fqn  = "mock-tenant:mcp-server:spike-deepwiki"
      role          = "mcp-server-user"
    },
  ]
}
`

func TestAccRoleBinding_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.RoleBindingCount(); n != 0 {
				return fmt.Errorf("%d role bindings left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + roleBindingConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_role_binding.ci", "id"),
					resource.TestCheckResourceAttr("truefoundry_role_binding.ci", "subjects.#", "1"),
					resource.TestCheckResourceAttr("truefoundry_role_binding.ci", "permissions.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_role_binding.ci", "permissions.*", map[string]string{
						"resource_type": "mcp-server",
						"resource_fqn":  "mock-tenant:mcp-server:spike-deepwiki",
						"role":          "mcp-server-user",
					}),
				),
			},
			// In place: switch to many subjects with one permission.
			{
				Config: providerConfig(host) + `
resource "truefoundry_role_binding" "ci" {
  name = "va-ci-bot"

  subjects = [
    { type = "virtualaccount", name = "ci-bot" },
    { type = "user", name = "jane@example.com" },
    { type = "team", name = "everyone" },
  ]

  permissions = [
    {
      resource_type = "provider-account"
      resource_fqn  = "mock-tenant:azure-openai:spike-azure"
      role          = "provider-account-access"
    },
  ]
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("truefoundry_role_binding.ci", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_role_binding.ci", "subjects.#", "3"),
					resource.TestCheckResourceAttr("truefoundry_role_binding.ci", "permissions.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_role_binding.ci", "subjects.*", map[string]string{
						"type": "user",
						"name": "jane@example.com",
					}),
				),
			},
			// Renaming replaces the binding.
			{
				Config:           providerConfig(host) + strings.Replace(roleBindingConfig, `"va-ci-bot"`, `"va-ci-bot-two"`, 1),
				ConfigPlanChecks: replacePlanCheck("truefoundry_role_binding.ci"),
				Check: func(*terraform.State) error {
					if n := mock.RoleBindingCount(); n != 1 {
						return fmt.Errorf("control plane has %d role bindings, want 1", n)
					}
					return nil
				},
			},
			// Out-of-band deletion is detected and the binding is recreated.
			{
				PreConfig: func() { mock.DeleteRoleBindingByName("va-ci-bot-two") },
				Config:    providerConfig(host) + strings.Replace(roleBindingConfig, `"va-ci-bot"`, `"va-ci-bot-two"`, 1),
				Check:     resource.TestCheckResourceAttr("truefoundry_role_binding.ci", "name", "va-ci-bot-two"),
			},
		},
	})
}

func TestAccRoleBinding_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + roleBindingConfig},
			{
				ResourceName:      "truefoundry_role_binding.ci",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "va-ci-bot",
			},
		},
	})
}

func TestAccRoleBinding_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	permission := `{
      resource_type = "provider-account"
      resource_fqn  = "mock-tenant:azure-openai:spike-azure"
      role          = "provider-account-access"
    }`
	otherPermission := `{
      resource_type = "provider-account"
      resource_fqn  = "mock-tenant:azure-openai:spike-azure"
      role          = "provider-account-manager"
    }`

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "many subjects and many permissions",
			config: `
resource "truefoundry_role_binding" "bad" {
  name        = "spike-bad"
  subjects    = [{ type = "user", name = "jane@example.com" }, { type = "team", name = "everyone" }]
  permissions = [` + permission + `, ` + otherPermission + `]
}
`,
			want: regexp.MustCompile(`one subject with many permissions or many\s+subjects with one permission`),
		},
		{
			name: "unknown subject type",
			config: `
resource "truefoundry_role_binding" "bad" {
  name        = "spike-bad"
  subjects    = [{ type = "serviceaccount", name = "ci-bot" }]
  permissions = [` + permission + `]
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "no subjects",
			config: `
resource "truefoundry_role_binding" "bad" {
  name        = "spike-bad"
  subjects    = []
  permissions = [` + permission + `]
}
`,
			want: regexp.MustCompile(`at least 1`),
		},
		{
			name: "upper case name",
			config: `
resource "truefoundry_role_binding" "bad" {
  name        = "Spike-Bad"
  subjects    = [{ type = "team", name = "everyone" }]
  permissions = [` + permission + `]
}
`,
			want: regexp.MustCompile(`must start with a lowercase letter`),
		},
		{
			name: "name too long",
			config: `
resource "truefoundry_role_binding" "bad" {
  name        = "` + "a" + strings.Repeat("b", 66) + `"
  subjects    = [{ type = "team", name = "everyone" }]
  permissions = [` + permission + `]
}
`,
			want: regexp.MustCompile(`length must be between 3 and 66`),
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
