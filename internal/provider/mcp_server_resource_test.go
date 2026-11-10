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

const mcpServerConfig = `
resource "truefoundry_mcp_server" "wiki" {
  name        = "spike-deepwiki"
  description = "AI-powered documentation for GitHub repositories"
  url         = "https://mcp.deepwiki.com/mcp"

  auth = {
    type       = "header"
    auth_level = "global"
    headers = {
      "Authorization" = "tenant:hashicorp:vault-secrets:secret-store:vault::mcp/deepwiki/token"
    }
  }

  tool_settings = [
    {
      name        = "ask_question"
      description = "Ask any question about a GitHub repository"
      annotations = {
        read_only_hint = true
      }
    },
    {
      name     = "read_wiki_structure"
      disabled = true
    },
  ]

  collaborators = [
    {
      subject = "team:everyone"
      role_id = "mcp-server-user"
    },
  ]

  tags = {
    team = "ai-foundation"
  }
}
`

func TestAccMcpServer_lifecycle(t *testing.T) {
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
				Config: providerConfig(host) + mcpServerConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_mcp_server.wiki", "fqn", mockcp.Tenant+":mcp-server:spike-deepwiki"),
					resource.TestCheckResourceAttrSet("truefoundry_mcp_server.wiki", "id"),
					resource.TestCheckResourceAttr("truefoundry_mcp_server.wiki", "auth.type", "header"),
					resource.TestCheckResourceAttr("truefoundry_mcp_server.wiki", "tool_settings.#", "2"),
					resource.TestCheckResourceAttr("truefoundry_mcp_server.wiki", "tool_settings.0.annotations.read_only_hint", "true"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_mcp_server.wiki", "collaborators.*", map[string]string{
						"subject": "team:everyone",
						"role_id": "mcp-server-user",
					}),
				),
			},
			// In-place update: new URL and passthrough auth, no tool overrides.
			{
				Config: providerConfig(host) + `
resource "truefoundry_mcp_server" "wiki" {
  name        = "spike-deepwiki"
  description = "DeepWiki via the EU endpoint"
  url         = "https://mcp-eu.deepwiki.com/mcp"

  auth = {
    type = "passthrough"
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_mcp_server.wiki", "url", "https://mcp-eu.deepwiki.com/mcp"),
					resource.TestCheckResourceAttr("truefoundry_mcp_server.wiki", "auth.type", "passthrough"),
					resource.TestCheckNoResourceAttr("truefoundry_mcp_server.wiki", "tool_settings"),
					resource.TestCheckNoResourceAttr("truefoundry_mcp_server.wiki", "collaborators"),
				),
			},
			// Out-of-band deletion is detected and the server is recreated.
			{
				PreConfig: func() { mock.DeleteMcpServer("spike-deepwiki") },
				Config: providerConfig(host) + `
resource "truefoundry_mcp_server" "wiki" {
  name        = "spike-deepwiki"
  description = "DeepWiki via the EU endpoint"
  url         = "https://mcp-eu.deepwiki.com/mcp"
}
`,
				Check: resource.TestCheckResourceAttr("truefoundry_mcp_server.wiki", "fqn", mockcp.Tenant+":mcp-server:spike-deepwiki"),
			},
		},
	})
}

func TestAccMcpServer_oauth2(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + `
resource "truefoundry_mcp_server" "internal" {
  name        = "spike-internal-api"
  description = "Internal API MCP with OIDC"
  url         = "https://internal.example.com/mcp"

  auth = {
    type              = "oauth2"
    grant_type        = "authorization_code"
    provider          = "custom"
    jwt_source        = "id_token"
    authorization_url = "https://idp.example.com/auth"
    token_url         = "https://idp.example.com/token"
    client_id         = "tenant:hashicorp:vault-secrets:secret-store:vault::idp/client_id"
    client_secret     = "tenant:hashicorp:vault-secrets:secret-store:vault::idp/client_secret"
    scopes            = ["openid", "email"]

    code_challenge_methods_supported = ["S256"]
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_mcp_server.internal", "auth.grant_type", "authorization_code"),
					resource.TestCheckTypeSetElemAttr("truefoundry_mcp_server.internal", "auth.scopes.*", "openid"),
				),
			},
		},
	})
}

func TestAccMcpServer_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + mcpServerConfig},
			{
				ResourceName:      "truefoundry_mcp_server.wiki",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     mockcp.Tenant + ":mcp-server:spike-deepwiki",
			},
		},
	})
}

func TestAccMcpServer_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "header auth without headers",
			config: `
resource "truefoundry_mcp_server" "bad" {
  name        = "spike-bad"
  description = "bad"
  url         = "https://example.com/mcp"
  auth = {
    type       = "header"
    auth_level = "global"
  }
}
`,
			want: regexp.MustCompile("`headers` is required"),
		},
		{
			name: "oauth2 auth without token_url",
			config: `
resource "truefoundry_mcp_server" "bad" {
  name        = "spike-bad"
  description = "bad"
  url         = "https://example.com/mcp"
  auth = {
    type       = "oauth2"
    grant_type = "client_credentials"
    provider   = "custom"
    jwt_source = "access_token"
  }
}
`,
			want: regexp.MustCompile("`token_url` is required"),
		},
		{
			name: "passthrough auth with headers",
			config: `
resource "truefoundry_mcp_server" "bad" {
  name        = "spike-bad"
  description = "bad"
  url         = "https://example.com/mcp"
  auth = {
    type = "passthrough"
    headers = {
      "Authorization" = "Bearer x"
    }
  }
}
`,
			want: regexp.MustCompile("`headers` must not be set"),
		},
		{
			name: "header auth with oauth2 fields",
			config: `
resource "truefoundry_mcp_server" "bad" {
  name        = "spike-bad"
  description = "bad"
  url         = "https://example.com/mcp"
  auth = {
    type       = "header"
    auth_level = "global"
    headers = {
      "Authorization" = "Bearer x"
    }
    token_url = "https://idp.example.com/token"
  }
}
`,
			want: regexp.MustCompile("`token_url` must not be set"),
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
