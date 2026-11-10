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

// Mirrors the live dev setup: the built-in DB-backed system secret store.
const systemSecretStoreConfig = `
resource "truefoundry_system_secret_store" "system" {
  name = "spike-secret-store"

  secret_stores = [
    { name = "spike-secret-store" },
  ]
}
`

func TestAccSystemSecretStore_lifecycle(t *testing.T) {
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
				Config: providerConfig(host) + systemSecretStoreConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_system_secret_store.system", "fqn", mockcp.Tenant+":truefoundry:spike-secret-store"),
					resource.TestCheckResourceAttr("truefoundry_system_secret_store.system", "secret_stores.#", "1"),
				),
			},
			// In-place update: restrict access and add a second store.
			{
				Config: providerConfig(host) + `
resource "truefoundry_system_secret_store" "system" {
  name = "spike-secret-store"

  secret_stores = [
    {
      name                = "spike-secret-store"
      authorized_subjects = ["team:everyone"]
    },
    { name = "ci-secrets" },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_system_secret_store.system", "secret_stores.#", "2"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_system_secret_store.system", "secret_stores.*", map[string]string{
						"name":                  "spike-secret-store",
						"authorized_subjects.#": "1",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_system_secret_store.system", "secret_stores.*.authorized_subjects.*", "team:everyone"),
				),
			},
		},
	})
}

func TestAccSystemSecretStore_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + systemSecretStoreConfig},
			{
				ResourceName:      "truefoundry_system_secret_store.system",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     mockcp.Tenant + ":truefoundry:spike-secret-store",
			},
		},
	})
}

func TestAccSystemSecretStore_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "no secret stores",
			config: `
resource "truefoundry_system_secret_store" "bad" {
  name          = "spike-bad"
  secret_stores = []
}
`,
			want: regexp.MustCompile(`at least 1 elements`),
		},
		{
			name: "invalid name",
			config: `
resource "truefoundry_system_secret_store" "bad" {
  name = "Spike_Bad"
  secret_stores = [
    { name = "s1" },
  ]
}
`,
			want: regexp.MustCompile(`must start with a lowercase letter`),
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
