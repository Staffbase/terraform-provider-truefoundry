package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

// A remote server plus a virtual server exposing a subset of its tools,
// mirroring the aggregation setup on the live dev tenant.
const virtualMcpServerConfig = `
resource "truefoundry_mcp_server" "wiki" {
  name        = "spike-deepwiki-src"
  description = "AI-powered documentation for GitHub repositories"
  url         = "https://mcp.deepwiki.com/mcp"
}

resource "truefoundry_virtual_mcp_server" "docs" {
  name        = "spike-docs"
  description = "Curated documentation tools"

  servers = [
    {
      name          = truefoundry_mcp_server.wiki.name
      enabled_tools = ["ask_question"]
    },
  ]

  collaborators = [
    {
      subject = "team:everyone"
      role_id = "mcp-server-user"
    },
  ]
}
`

func TestAccVirtualMcpServer_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.McpServerCount(); n != 0 {
				return fmt.Errorf("%d MCP servers left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + virtualMcpServerConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_virtual_mcp_server.docs", "fqn", mockcp.Tenant+":mcp-server:spike-docs"),
					resource.TestCheckResourceAttrSet("truefoundry_virtual_mcp_server.docs", "id"),
					resource.TestCheckResourceAttr("truefoundry_virtual_mcp_server.docs", "servers.#", "1"),
					resource.TestCheckResourceAttr("truefoundry_virtual_mcp_server.docs", "servers.0.name", "spike-deepwiki-src"),
					resource.TestCheckTypeSetElemAttr("truefoundry_virtual_mcp_server.docs", "servers.0.enabled_tools.*", "ask_question"),
				),
			},
			// In-place update: expose all tools and add auth overrides.
			{
				Config: providerConfig(host) + `
resource "truefoundry_mcp_server" "wiki" {
  name        = "spike-deepwiki-src"
  description = "AI-powered documentation for GitHub repositories"
  url         = "https://mcp.deepwiki.com/mcp"
}

resource "truefoundry_virtual_mcp_server" "docs" {
  name        = "spike-docs"
  description = "All documentation tools"

  servers = [
    {
      name = truefoundry_mcp_server.wiki.name
      auth_headers = {
        "X-Team" = "docs"
      }
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_virtual_mcp_server.docs", "description", "All documentation tools"),
					resource.TestCheckNoResourceAttr("truefoundry_virtual_mcp_server.docs", "servers.0.enabled_tools"),
					resource.TestCheckResourceAttr("truefoundry_virtual_mcp_server.docs", "servers.0.auth_headers.X-Team", "docs"),
				),
			},
		},
	})
}

func TestAccVirtualMcpServer_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + virtualMcpServerConfig},
			{
				ResourceName:      "truefoundry_virtual_mcp_server.docs",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     mockcp.Tenant + ":mcp-server:spike-docs",
			},
		},
	})
}

// The control plane rejects virtual servers whose source servers do not
// exist; the mock mirrors that apply-time validation.
func TestAccVirtualMcpServer_unknownSourceRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + `
resource "truefoundry_virtual_mcp_server" "bad" {
  name        = "spike-bad"
  description = "references a server that does not exist"

  servers = [
    { name = "no-such-server" },
  ]
}
`,
				ExpectError: regexp.MustCompile(`(?s)Source server.*no-such-server.*not found`),
			},
		},
	})
}
