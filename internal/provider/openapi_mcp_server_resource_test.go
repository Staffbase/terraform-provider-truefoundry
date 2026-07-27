package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

const openapiMcpServerConfig = `
resource "truefoundry_openapi_mcp_server" "picklerick" {
  name        = "spike-picklerick"
  description = "picklerick api mcp"
  url         = "https://picklerick.example.com/api"

  openapi_spec_source = {
    type = "upload"
    url  = "https://blob.example.com/tfy-openapi-specs/spike/picklerick.json"
  }

  enable_all_tools = false

  auth = {
    type              = "oauth2"
    grant_type        = "authorization_code"
    provider          = "custom"
    jwt_source        = "id_token"
    authorization_url = "https://dex.example.com/auth"
    token_url         = "https://dex.example.com/token"
    client_id         = "tenant:hashicorp:vault-secrets:secret-store:vault::dex/client_id"
    client_secret     = "tenant:hashicorp:vault-secrets:secret-store:vault::dex/client_secret"
    scopes            = ["openid", "email", "groups", "profile", "offline_access"]

    code_challenge_methods_supported = ["S256"]
  }

  collaborators = [
    {
      subject = "team:everyone"
      role_id = "mcp-server-user"
    },
  ]
}
`

func TestAccOpenapiMcpServer_lifecycle(t *testing.T) {
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
				Config: providerConfig(host) + openapiMcpServerConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_openapi_mcp_server.picklerick", "fqn", mockcp.Tenant+":mcp-server:spike-picklerick"),
					resource.TestCheckResourceAttrSet("truefoundry_openapi_mcp_server.picklerick", "id"),
					resource.TestCheckResourceAttr("truefoundry_openapi_mcp_server.picklerick", "openapi_spec_source.type", "upload"),
					resource.TestCheckResourceAttr("truefoundry_openapi_mcp_server.picklerick", "enable_all_tools", "false"),
					resource.TestCheckResourceAttr("truefoundry_openapi_mcp_server.picklerick", "auth.grant_type", "authorization_code"),
					resource.TestCheckTypeSetElemAttr("truefoundry_openapi_mcp_server.picklerick", "auth.scopes.*", "offline_access"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_openapi_mcp_server.picklerick", "collaborators.*", map[string]string{
						"subject": "team:everyone",
						"role_id": "mcp-server-user",
					}),
				),
			},
			// In-place update: remote spec source, selected tools instead of
			// OAuth2, no collaborators.
			{
				Config: providerConfig(host) + `
resource "truefoundry_openapi_mcp_server" "picklerick" {
  name        = "spike-picklerick"
  description = "picklerick api mcp via remote spec"
  url         = "https://picklerick.example.com/api"

  openapi_spec_source = {
    type = "remote"
    url  = "https://picklerick.example.com/api/openapi.json"
  }

  tool_settings = [
    {
      method      = "get"
      path        = "/v1/builds/{id}"
      name        = "get_build"
      description = "Fetch one native app build"
      annotations = {
        read_only_hint = true
      }
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_openapi_mcp_server.picklerick", "openapi_spec_source.type", "remote"),
					resource.TestCheckResourceAttr("truefoundry_openapi_mcp_server.picklerick", "tool_settings.0.method", "get"),
					resource.TestCheckResourceAttr("truefoundry_openapi_mcp_server.picklerick", "tool_settings.0.annotations.read_only_hint", "true"),
					resource.TestCheckNoResourceAttr("truefoundry_openapi_mcp_server.picklerick", "auth"),
					resource.TestCheckNoResourceAttr("truefoundry_openapi_mcp_server.picklerick", "collaborators"),
				),
			},
			// Out-of-band deletion is detected and the server is recreated.
			{
				PreConfig: func() { mock.DeleteMcpServer("spike-picklerick") },
				Config: providerConfig(host) + `
resource "truefoundry_openapi_mcp_server" "picklerick" {
  name        = "spike-picklerick"
  description = "picklerick api mcp via remote spec"
  url         = "https://picklerick.example.com/api"

  openapi_spec_source = {
    type = "remote"
    url  = "https://picklerick.example.com/api/openapi.json"
  }
}
`,
				Check: resource.TestCheckResourceAttr("truefoundry_openapi_mcp_server.picklerick", "fqn", mockcp.Tenant+":mcp-server:spike-picklerick"),
			},
		},
	})
}

func TestAccOpenapiMcpServer_inlineSpec(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + `
resource "truefoundry_openapi_mcp_server" "inline" {
  name        = "spike-inline"
  description = "inline spec"
  url         = "https://api.example.com"

  openapi_spec_source = {
    type = "inline"
    openapi_spec = jsonencode({
      openapi = "3.0.3"
      info    = { title = "example", version = "1.0.0" }
      paths   = {}
    })
  }

  enable_all_tools = true
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_openapi_mcp_server.inline", "openapi_spec_source.type", "inline"),
					resource.TestCheckResourceAttr("truefoundry_openapi_mcp_server.inline", "enable_all_tools", "true"),
				),
			},
		},
	})
}

func TestAccOpenapiMcpServer_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + openapiMcpServerConfig},
			{
				ResourceName:      "truefoundry_openapi_mcp_server.picklerick",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     mockcp.Tenant + ":mcp-server:spike-picklerick",
			},
		},
	})
}

func TestAccOpenapiMcpServer_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "remote spec source without url",
			config: `
resource "truefoundry_openapi_mcp_server" "bad" {
  name        = "spike-bad"
  description = "bad"
  url         = "https://api.example.com"
  openapi_spec_source = {
    type = "remote"
  }
}
`,
			want: regexp.MustCompile("`url` is required for spec source type \"remote\""),
		},
		{
			name: "upload spec source with inline spec",
			config: `
resource "truefoundry_openapi_mcp_server" "bad" {
  name        = "spike-bad"
  description = "bad"
  url         = "https://api.example.com"
  openapi_spec_source = {
    type         = "upload"
    url          = "https://blob.example.com/spec.json"
    openapi_spec = "{}"
  }
}
`,
			want: regexp.MustCompile("`openapi_spec` must not be set"),
		},
		{
			name: "inline spec source without spec",
			config: `
resource "truefoundry_openapi_mcp_server" "bad" {
  name        = "spike-bad"
  description = "bad"
  url         = "https://api.example.com"
  openapi_spec_source = {
    type = "inline"
  }
}
`,
			want: regexp.MustCompile("`openapi_spec` is required for spec source type \"inline\""),
		},
		{
			name: "oauth2 auth without token_url",
			config: `
resource "truefoundry_openapi_mcp_server" "bad" {
  name        = "spike-bad"
  description = "bad"
  url         = "https://api.example.com"
  openapi_spec_source = {
    type = "remote"
    url  = "https://api.example.com/openapi.json"
  }
  auth = {
    type       = "oauth2"
    grant_type = "authorization_code"
    provider   = "custom"
    jwt_source = "id_token"
  }
}
`,
			want: regexp.MustCompile("`token_url` is required"),
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
