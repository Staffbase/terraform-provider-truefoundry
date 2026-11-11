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

// checkLegacyGrants asserts how many legacy grants the control plane holds
// for a resource; want < 0 asserts the field is absent altogether.
func checkLegacyGrants(mock *mockcp.Server, kind, name string, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		grants, present := mock.LegacyGrants(kind, name)
		switch {
		case want < 0 && present:
			return fmt.Errorf("%s %q carries %d legacy grants, want the field absent", kind, name, len(grants))
		case want >= 0 && !present:
			return fmt.Errorf("%s %q carries no legacy grants field, want %d grants", kind, name, want)
		case want >= 0 && len(grants) != want:
			return fmt.Errorf("%s %q carries %d legacy grants, want %d", kind, name, len(grants), want)
		}
		return nil
	}
}

const legacyGrantsMlRepo = mlRepoStorageConfig + `
resource "truefoundry_ml_repo" "artifacts" {
  name                    = "gateway-artifacts"
  storage_integration_fqn = "${truefoundry_azure_account.storage.fqn}:blob-storage:artifacts"
  %s
}
`

// TestAccLegacyGrants_collaboratorsMigration walks the migration path of the
// "Migrating to role bindings" guide: removing collaborators leaves the
// grants on the control plane without showing drift, and an explicit empty
// set clears them.
func TestAccLegacyGrants_collaboratorsMigration(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + fmt.Sprintf(legacyGrantsMlRepo,
					`collaborators = [{ subject = "team:everyone", role_id = "mlf-project-admin" }]`),
				Check: checkLegacyGrants(mock, "ml-repo", "gateway-artifacts", 1),
			},
			// Removing the attribute omits the field: the grant stays, and
			// the post-apply refresh (a non-empty plan fails the step) does
			// not report it as drift.
			{
				Config: providerConfig(host) + fmt.Sprintf(legacyGrantsMlRepo, "") + roleBindingConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("truefoundry_ml_repo.artifacts", "collaborators"),
					checkLegacyGrants(mock, "ml-repo", "gateway-artifacts", 1),
				),
			},
			// An explicit empty set clears the legacy grants.
			{
				Config: providerConfig(host) + fmt.Sprintf(legacyGrantsMlRepo, "collaborators = []") + roleBindingConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_ml_repo.artifacts", "collaborators.#", "0"),
					checkLegacyGrants(mock, "ml-repo", "gateway-artifacts", 0),
				),
			},
		},
	})
}

// TestAccLegacyGrants_virtualAccountPermissionsMigration removes the
// deprecated permissions of a virtual account in favour of a role binding.
func TestAccLegacyGrants_virtualAccountPermissionsMigration(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + `
resource "truefoundry_virtual_account" "ci" {
  name = "ci-bot"
  permissions = [
    {
      resource_fqn  = "mock-tenant:azure-openai:spike-azure"
      resource_type = "provider-account"
      role_id       = "provider-account-access"
    },
  ]
}
`,
				Check: checkLegacyGrants(mock, "virtual-account", "ci-bot", 1),
			},
			{
				Config: providerConfig(host) + `
resource "truefoundry_virtual_account" "ci" {
  name = "ci-bot"
}
` + roleBindingConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("truefoundry_virtual_account.ci", "permissions"),
					checkLegacyGrants(mock, "virtual-account", "ci-bot", 1),
					resource.TestCheckResourceAttrSet("truefoundry_role_binding.ci", "id"),
				),
			},
		},
	})
}

// TestAccLegacyGrants_retired applies configurations without any legacy
// grants against a control plane that rejects them, as every control plane
// does after 15 Nov 2026. This guards against a resource sending an empty
// collaborators or permissions list on its own.
func TestAccLegacyGrants_retired(t *testing.T) {
	mock, host := startMockControlPlane(t)
	mock.RetireLegacyGrants()

	withoutGrants := fmt.Sprintf(legacyGrantsMlRepo, "") + `
resource "truefoundry_system_secret_store" "system" {
  name          = "system-secret-store"
  secret_stores = [{ name = "system-secret-store" }]
}

resource "truefoundry_secret_group" "system" {
  name            = "system"
  integration_fqn = "${truefoundry_system_secret_store.system.fqn}:secret-store:system-secret-store"
}

resource "truefoundry_mcp_server" "wiki" {
  name        = "spike-deepwiki"
  description = "AI-powered documentation for GitHub repositories"
  url         = "https://mcp.deepwiki.com/mcp"
}

resource "truefoundry_virtual_mcp_server" "bundle" {
  name        = "spike-bundle"
  description = "Bundle of MCP servers"
  servers     = [{ name = truefoundry_mcp_server.wiki.name }]
}

resource "truefoundry_remote_agent" "billing" {
  name        = "billing-agent"
  description = "Self-hosted billing agent"
  url         = "https://agents.example.com/billing"
  framework   = { type = "custom" }
}

resource "truefoundry_guardrail_group" "region" {
  name = "spike-guardrails"
  guardrails = [
    {
      type               = "azure-prompt-shield"
      name               = "prompt-shield"
      api_key            = "tenant:hashicorp:vault-secrets:secret-store:vault::guardrails/key"
      enforcing_strategy = "enforce"
      resource_name      = "my-content-safety"
      api_version        = "2024-09-01"
    },
  ]
}

resource "truefoundry_virtual_account" "ci" {
  name = "ci-bot"
}
` + roleBindingConfig

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + withoutGrants,
				Check: resource.ComposeAggregateTestCheckFunc(
					checkLegacyGrants(mock, "ml-repo", "gateway-artifacts", -1),
					checkLegacyGrants(mock, "secret-group", "system", -1),
					checkLegacyGrants(mock, "mcp-server", "spike-deepwiki", -1),
					checkLegacyGrants(mock, "mcp-server", "spike-bundle", -1),
					checkLegacyGrants(mock, "agent", "billing-agent", -1),
					checkLegacyGrants(mock, "provider-account", "spike-guardrails", -1),
					checkLegacyGrants(mock, "virtual-account", "ci-bot", -1),
				),
			},
			// Configurations still carrying them fail with the control
			// plane's error.
			{
				Config: providerConfig(host) + fmt.Sprintf(legacyGrantsMlRepo,
					`collaborators = [{ subject = "team:everyone", role_id = "mlf-project-admin" }]`),
				ExpectError: regexp.MustCompile(`collaborators is no longer supported`),
			},
		},
	})
}
