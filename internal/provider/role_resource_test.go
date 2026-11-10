// Copyright 2026, Staffbase SE and contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
//
// You may obtain a copy of the License at
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const roleConfig = `
resource "truefoundry_role" "ops" {
  name          = "gateway-operator"
  display_name  = "Gateway Operator"
  description   = "Reads gateway objects."
  resource_type = "tenant"
  permissions = [
    "provider-account:ReadProviderAccount",
    "mcp-server:ReadMcpServer",
  ]
}
`

// The same role with a wider permission set and reworded metadata — every
// change is in place, since only name is RequiresReplace.
const roleConfigUpdated = `
resource "truefoundry_role" "ops" {
  name          = "gateway-operator"
  display_name  = "Gateway Operator (read/write)"
  resource_type = "tenant"
  permissions = [
    "provider-account:ReadProviderAccount",
    "provider-account:ManageProviderAccount",
    "mcp-server:ReadMcpServer",
  ]
}
`

const roleConfigRenamed = `
resource "truefoundry_role" "ops" {
  name          = "gateway-operator-v2"
  display_name  = "Gateway Operator"
  resource_type = "tenant"
  permissions   = ["provider-account:ReadProviderAccount"]
}
`

// member is seeded by the mock as a TrueFoundry-provided default role, the
// way the Core control plane ships it.
const roleConfigBuiltinDefault = `
resource "truefoundry_role" "member" {
  name          = "member"
  display_name  = "Member"
  description   = "Role grants member access to the entities in the account."
  resource_type = "tenant"
  permissions   = ["user:ListUsers", "team:ReadTeam"]
}
`

// tenant-admin is seeded as a reserved role, which Terraform must refuse.
const roleConfigBuiltinReserved = `
resource "truefoundry_role" "admin" {
  name          = "tenant-admin"
  display_name  = "Tenant Admin"
  resource_type = "tenant"
  permissions   = ["tenant:AssignRole"]
}
`

func TestAccRole_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.RoleCount(); n != 0 {
				return fmt.Errorf("%d roles left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + roleConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_role.ops", "id"),
					resource.TestCheckResourceAttr("truefoundry_role.ops", "name", "gateway-operator"),
					resource.TestCheckResourceAttr("truefoundry_role.ops", "display_name", "Gateway Operator"),
					resource.TestCheckResourceAttr("truefoundry_role.ops", "description", "Reads gateway objects."),
					resource.TestCheckResourceAttr("truefoundry_role.ops", "resource_type", "tenant"),
					resource.TestCheckResourceAttr("truefoundry_role.ops", "permissions.#", "2"),
					resource.TestCheckTypeSetElemAttr("truefoundry_role.ops", "permissions.*", "mcp-server:ReadMcpServer"),
					resource.TestCheckResourceAttr("truefoundry_role.ops", "is_editable", "true"),
					resource.TestCheckResourceAttr("truefoundry_role.ops", "is_default", "false"),
					resource.TestCheckResourceAttr("truefoundry_role.ops", "is_reserved", "false"),
				),
			},
			{
				// In place: dropping description and widening permissions.
				Config: providerConfig(host) + roleConfigUpdated,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_role.ops", "display_name", "Gateway Operator (read/write)"),
					resource.TestCheckNoResourceAttr("truefoundry_role.ops", "description"),
					resource.TestCheckResourceAttr("truefoundry_role.ops", "permissions.#", "3"),
					resource.TestCheckTypeSetElemAttr("truefoundry_role.ops", "permissions.*", "provider-account:ManageProviderAccount"),
				),
			},
		},
	})
}

func TestAccRole_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + roleConfig},
			{
				ResourceName:      "truefoundry_role.ops",
				ImportState:       true,
				ImportStateId:     "gateway-operator",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccRole_replaceOnRename(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + roleConfig},
			{
				Config:           providerConfig(host) + roleConfigRenamed,
				ConfigPlanChecks: replacePlanCheck("truefoundry_role.ops"),
				Check:            resource.TestCheckResourceAttr("truefoundry_role.ops", "name", "gateway-operator-v2"),
			},
		},
	})
}

// A role deleted outside Terraform is recreated rather than erroring.
func TestAccRole_driftRecreated(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + roleConfig},
			{
				PreConfig: func() { mock.DeleteRoleByName("gateway-operator") },
				Config:    providerConfig(host) + roleConfig,
				Check:     resource.TestCheckResourceAttrSet("truefoundry_role.ops", "id"),
			},
		},
	})
}

// Reserved roles cannot be adopted: the apply is rejected by the control
// plane, so a configuration naming one never reaches state.
func TestAccRole_reservedRoleNotAdopted(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config:      providerConfig(host) + roleConfigBuiltinReserved,
				ExpectError: regexp.MustCompile(`not editable`),
			},
		},
	})
}

// A TrueFoundry-provided default role is updated in place, and destroying the
// resource must leave it on the control plane.
func TestAccRole_defaultRoleSurvivesDestroy(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if !mock.RoleExists("member") {
				return fmt.Errorf("built-in role member was deleted on destroy")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + roleConfigBuiltinDefault,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_role.member", "is_default", "true"),
					resource.TestCheckResourceAttr("truefoundry_role.member", "is_editable", "true"),
				),
			},
		},
	})
}

func TestAccRole_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			// "account" is what the tfy-configuration YAML writes; the
			// control plane stores it as "tenant", so accepting it here
			// would leave the configuration permanently out of sync.
			name: "legacy account scope",
			config: `
resource "truefoundry_role" "bad" {
  name          = "ops"
  display_name  = "Ops"
  resource_type = "account"
  permissions   = ["user:ListUsers"]
}`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "unsupported scope",
			config: `
resource "truefoundry_role" "bad" {
  name          = "ops"
  display_name  = "Ops"
  resource_type = "workspace"
  permissions   = ["user:ListUsers"]
}`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "no permissions",
			config: `
resource "truefoundry_role" "bad" {
  name          = "ops"
  display_name  = "Ops"
  resource_type = "tenant"
  permissions   = []
}`,
			want: regexp.MustCompile(`set must contain at least 1 elements`),
		},
		{
			// The correct spelling is repository:ReadRepository — ML repos
			// are "repository" in the permission vocabulary.
			name: "misspelled permission",
			config: `
resource "truefoundry_role" "bad" {
  name          = "ops"
  display_name  = "Ops"
  resource_type = "tenant"
  permissions   = ["ml-repo:read"]
}`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "missing display name",
			config: `
resource "truefoundry_role" "bad" {
  name          = "ops"
  resource_type = "tenant"
  permissions   = ["user:ListUsers"]
}`,
			want: regexp.MustCompile(`The argument "display_name" is required`),
		},
		{
			name: "invalid name",
			config: `
resource "truefoundry_role" "bad" {
  name          = "Bad_Name"
  display_name  = "Ops"
  resource_type = "tenant"
  permissions   = ["user:ListUsers"]
}`,
			want: regexp.MustCompile(`must start with a lowercase letter`),
		},
		{
			// The control plane caps role names at 35 characters.
			name: "name too long",
			config: `
resource "truefoundry_role" "bad" {
  name          = "abbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbc"
  display_name  = "Ops"
  resource_type = "tenant"
  permissions   = ["user:ListUsers"]
}`,
			want: regexp.MustCompile(`string length must be between 3 and 35`),
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
