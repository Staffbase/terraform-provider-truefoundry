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

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

// A secret group on the built-in system secret store, wired by interpolation
// the way the docs recommend — which also exercises the control plane's
// integration existence check.
const secretGroupConfig = `
resource "truefoundry_system_secret_store" "system" {
  name = "system-secret-store"

  secret_stores = [
    { name = "system-secret-store" },
  ]
}

resource "truefoundry_secret_group" "system" {
  name            = "system"
  integration_fqn = "${truefoundry_system_secret_store.system.fqn}:secret-store:system-secret-store"

  collaborators = [
    { subject = "user:jane@example.com", role_id = "secret-group-viewer" },
  ]
}
`

// Same group, collaborators swapped — an in-place update.
const secretGroupConfigUpdated = `
resource "truefoundry_system_secret_store" "system" {
  name = "system-secret-store"

  secret_stores = [
    { name = "system-secret-store" },
  ]
}

resource "truefoundry_secret_group" "system" {
  name            = "system"
  integration_fqn = "${truefoundry_system_secret_store.system.fqn}:secret-store:system-secret-store"

  collaborators = [
    { subject = "user:sam@example.com", role_id = "secret-group-editor" },
    { subject = "team:everyone", role_id = "secret-group-viewer" },
  ]
}
`

const secretGroupConfigRenamed = `
resource "truefoundry_system_secret_store" "system" {
  name = "system-secret-store"

  secret_stores = [
    { name = "system-secret-store" },
  ]
}

resource "truefoundry_secret_group" "system" {
  name            = "shared"
  integration_fqn = "${truefoundry_system_secret_store.system.fqn}:secret-store:system-secret-store"
}
`

func TestAccSecretGroup_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.SecretGroupCount(); n != 0 {
				return fmt.Errorf("%d secret groups left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + secretGroupConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_secret_group.system", "id"),
					resource.TestCheckResourceAttr("truefoundry_secret_group.system", "fqn", mockcp.Tenant+":system"),
					resource.TestCheckResourceAttr("truefoundry_secret_group.system", "name", "system"),
					resource.TestCheckResourceAttr("truefoundry_secret_group.system", "integration_fqn",
						mockcp.Tenant+":truefoundry:system-secret-store:secret-store:system-secret-store"),
					resource.TestCheckResourceAttr("truefoundry_secret_group.system", "collaborators.#", "1"),
				),
			},
			{
				Config: providerConfig(host) + secretGroupConfigUpdated,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_secret_group.system", "collaborators.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_secret_group.system", "collaborators.*",
						map[string]string{"subject": "team:everyone", "role_id": "secret-group-viewer"}),
				),
			},
		},
	})
}

func TestAccSecretGroup_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + secretGroupConfig},
			{
				ResourceName:      "truefoundry_secret_group.system",
				ImportState:       true,
				ImportStateId:     "system",
				ImportStateVerify: true,
			},
		},
	})
}

// Renaming a group replaces it — and takes its secrets with it, which is why
// the schema tells practitioners to guard live groups with prevent_destroy.
func TestAccSecretGroup_replaceOnRename(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + secretGroupConfig},
			{
				Config:           providerConfig(host) + secretGroupConfigRenamed,
				ConfigPlanChecks: replacePlanCheck("truefoundry_secret_group.system"),
				Check:            resource.TestCheckResourceAttr("truefoundry_secret_group.system", "fqn", mockcp.Tenant+":shared"),
			},
		},
	})
}

func TestAccSecretGroup_driftRecreated(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + secretGroupConfig},
			{
				PreConfig: func() { mock.DeleteSecretGroupByName("system") },
				Config:    providerConfig(host) + secretGroupConfig,
				Check:     resource.TestCheckResourceAttrSet("truefoundry_secret_group.system", "id"),
			},
		},
	})
}

func TestAccSecretGroup_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			// A blob-storage integration is well-formed but the wrong kind.
			name: "blob storage integration",
			config: `
resource "truefoundry_secret_group" "bad" {
  name            = "system"
  integration_fqn = "mock-tenant:azure:my-account:blob-storage:my-storage"
}`,
			want: regexp.MustCompile(`must be a secret-store integration FQN`),
		},
		{
			name: "not an fqn",
			config: `
resource "truefoundry_secret_group" "bad" {
  name            = "system"
  integration_fqn = "system-secret-store"
}`,
			want: regexp.MustCompile(`must be a secret-store integration FQN`),
		},
		{
			// Well-formed, right kind, but no such store on the tenant —
			// only the control plane can catch this one.
			name: "unknown secret store",
			config: `
resource "truefoundry_secret_group" "bad" {
  name            = "system"
  integration_fqn = "mock-tenant:truefoundry:nope:secret-store:nope"
}`,
			want: regexp.MustCompile(`mock-tenant:truefoundry:nope:secret-store:nope not found`),
		},
		{
			name: "invalid name",
			config: `
resource "truefoundry_secret_group" "bad" {
  name            = "Bad_Name"
  integration_fqn = "mock-tenant:truefoundry:store:secret-store:store"
}`,
			want: regexp.MustCompile(`must start with a lowercase letter`),
		},
		{
			// Secret group names are the only ones with a minimum above 3.
			name: "name too short",
			config: `
resource "truefoundry_secret_group" "bad" {
  name            = "abcd"
  integration_fqn = "mock-tenant:truefoundry:store:secret-store:store"
}`,
			want: regexp.MustCompile(`string length must be between 5 and 50`),
		},
		{
			// ... and the only ones rejecting consecutive hyphens.
			name: "consecutive hyphens in name",
			config: `
resource "truefoundry_secret_group" "bad" {
  name            = "group--one"
  integration_fqn = "mock-tenant:truefoundry:store:secret-store:store"
}`,
			want: regexp.MustCompile(`single hyphens`),
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
