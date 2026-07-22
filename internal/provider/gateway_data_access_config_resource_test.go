package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Mirrors the live dev setup: the default own/team/admin rules.
const gatewayDataAccessConfigConfig = `
resource "truefoundry_gateway_data_access_config" "access" {
  name = "data-access"

  rules = [
    {
      id          = "default-everyone-own-data"
      scope       = "own_data"
      enabled     = true
      subjects    = ["team:everyone"]
      data_types  = ["traces", "metrics"]
      description = "Users can access their own data"
    },
    {
      id         = "default-everyone-team-data"
      scope      = "team_data"
      enabled    = true
      subjects   = ["team:everyone"]
      data_types = ["traces", "metrics"]
    },
    {
      id         = "default-tenant-admin-all-data"
      scope      = "all_data"
      enabled    = true
      subjects   = ["role:tenant-admin"]
      data_types = ["traces", "metrics"]
    },
  ]
}
`

func TestAccGatewayDataAccessConfig_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.GatewayConfigCount(); n != 0 {
				return fmt.Errorf("%d gateway configs left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + gatewayDataAccessConfigConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_gateway_data_access_config.access", "id"),
					resource.TestCheckResourceAttr("truefoundry_gateway_data_access_config.access", "rules.#", "3"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_data_access_config.access", "rules.*", map[string]string{
						"id":          "default-everyone-own-data",
						"scope":       "own_data",
						"description": "Users can access their own data",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_data_access_config.access", "rules.*", map[string]string{
						"id":         "default-tenant-admin-all-data",
						"scope":      "all_data",
						"subjects.#": "1",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_data_access_config.access", "rules.*.subjects.*", "role:tenant-admin"),
				),
			},
			// In-place update: filtered all_data rule, disabled rule.
			{
				Config: providerConfig(host) + `
resource "truefoundry_gateway_data_access_config" "access" {
  name = "data-access"

  rules = [
    {
      id         = "default-everyone-own-data"
      scope      = "own_data"
      enabled    = false
      subjects   = ["team:everyone"]
      data_types = ["traces"]
    },
    {
      id         = "cluster-scoped-all-data"
      scope      = "all_data"
      subjects   = ["team:platform"]
      data_types = ["traces", "metrics"]
      filters = [
        {
          field  = "metadata"
          key    = "tfy_gateway_cluster"
          values = ["devde1truefoundrydevelop"]
        },
        {
          field  = "created_by"
          values = ["virtualaccount:continuous-delivery"]
        },
      ]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_gateway_data_access_config.access", "rules.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_data_access_config.access", "rules.*", map[string]string{
						"id":      "default-everyone-own-data",
						"enabled": "false",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_data_access_config.access", "rules.*", map[string]string{
						"id":        "cluster-scoped-all-data",
						"filters.#": "2",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_data_access_config.access", "rules.*.filters.*", map[string]string{
						"field": "metadata",
						"key":   "tfy_gateway_cluster",
					}),
					// The created_by filter carries no key; identified by its value.
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_data_access_config.access", "rules.*.filters.*", map[string]string{
						"field":    "created_by",
						"values.#": "1",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_data_access_config.access", "rules.*.filters.*.values.*", "virtualaccount:continuous-delivery"),
				),
			},
		},
	})
}

func TestAccGatewayDataAccessConfig_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + gatewayDataAccessConfigConfig},
			{
				ResourceName:      "truefoundry_gateway_data_access_config.access",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "gateway-data-access-config",
			},
		},
	})
}

func TestAccGatewayDataAccessConfig_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "filters on own_data rule",
			config: `
resource "truefoundry_gateway_data_access_config" "bad" {
  name = "data-access"
  rules = [
    {
      id         = "r1"
      scope      = "own_data"
      subjects   = ["team:everyone"]
      data_types = ["traces"]
      filters = [
        { field = "created_by", values = ["user:x"] },
      ]
    },
  ]
}
`,
			want: regexp.MustCompile("filters` are only allowed on all_data"),
		},
		{
			name: "metadata filter without key",
			config: `
resource "truefoundry_gateway_data_access_config" "bad" {
  name = "data-access"
  rules = [
    {
      id         = "r1"
      scope      = "all_data"
      subjects   = ["team:everyone"]
      data_types = ["traces"]
      filters = [
        { field = "metadata", values = ["x"] },
      ]
    },
  ]
}
`,
			want: regexp.MustCompile("key` is required on metadata filters"),
		},
		{
			name: "key on created_by filter",
			config: `
resource "truefoundry_gateway_data_access_config" "bad" {
  name = "data-access"
  rules = [
    {
      id         = "r1"
      scope      = "all_data"
      subjects   = ["team:everyone"]
      data_types = ["traces"]
      filters = [
        { field = "created_by", key = "x", values = ["user:x"] },
      ]
    },
  ]
}
`,
			want: regexp.MustCompile("key` is only valid on metadata filters"),
		},
		{
			name: "invalid data type",
			config: `
resource "truefoundry_gateway_data_access_config" "bad" {
  name = "data-access"
  rules = [
    {
      id         = "r1"
      scope      = "own_data"
      subjects   = ["team:everyone"]
      data_types = ["logs"]
    },
  ]
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "duplicate rule ids",
			config: `
resource "truefoundry_gateway_data_access_config" "bad" {
  name = "data-access"
  rules = [
    { id = "r1", scope = "own_data", subjects = ["team:everyone"], data_types = ["traces"] },
    { id = "r1", scope = "team_data", subjects = ["team:everyone"], data_types = ["traces"] },
  ]
}
`,
			want: regexp.MustCompile(`Duplicate rule id`),
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
