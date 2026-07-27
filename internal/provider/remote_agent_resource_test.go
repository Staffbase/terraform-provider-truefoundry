package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

const remoteAgentConfig = `
resource "truefoundry_remote_agent" "billing" {
  name        = "billing-agent"
  description = "Self-hosted billing agent"
  url         = "https://agents.example.com/billing"

  framework = {
    type            = "a2a"
    agent_card_path = "/.well-known/agent.json"
  }

  auth = {
    type = "header"
    headers = {
      "Authorization" = "tenant:hashicorp:vault-secrets:secret-store:vault::agents/billing/token"
    }
  }

  collaborators = [
    {
      subject = "team:everyone"
      role_id = "agent-access"
    },
  ]

  tags = {
    team = "billing"
  }
}
`

func TestAccRemoteAgent_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	updatedConfig := providerConfig(host) + `
resource "truefoundry_remote_agent" "billing" {
  name        = "billing-agent"
  description = "Billing agent speaking a custom protocol"
  url         = "https://agents-eu.example.com/billing"

  framework = {
    type = "custom"
  }

  auth = {
    type = "passthrough"
  }
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.AgentCount(); n != 0 {
				return fmt.Errorf("%d agents left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + remoteAgentConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_remote_agent.billing", "fqn", mockcp.Tenant+":agent:billing-agent"),
					resource.TestCheckResourceAttrSet("truefoundry_remote_agent.billing", "id"),
					resource.TestCheckResourceAttr("truefoundry_remote_agent.billing", "framework.type", "a2a"),
					resource.TestCheckResourceAttr("truefoundry_remote_agent.billing", "framework.agent_card_path", "/.well-known/agent.json"),
					resource.TestCheckResourceAttr("truefoundry_remote_agent.billing", "auth.type", "header"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_remote_agent.billing", "collaborators.*", map[string]string{
						"subject": "team:everyone",
						"role_id": "agent-access",
					}),
				),
			},
			// In-place update: custom framework and passthrough auth.
			{
				Config: updatedConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_remote_agent.billing", "url", "https://agents-eu.example.com/billing"),
					resource.TestCheckResourceAttr("truefoundry_remote_agent.billing", "framework.type", "custom"),
					resource.TestCheckNoResourceAttr("truefoundry_remote_agent.billing", "framework.agent_card_path"),
					resource.TestCheckResourceAttr("truefoundry_remote_agent.billing", "auth.type", "passthrough"),
					resource.TestCheckNoResourceAttr("truefoundry_remote_agent.billing", "collaborators"),
				),
			},
			// Out-of-band deletion is detected and the agent is recreated.
			{
				PreConfig: func() { mock.DeleteAgentByName("billing-agent") },
				Config:    updatedConfig,
				Check:     resource.TestCheckResourceAttr("truefoundry_remote_agent.billing", "fqn", mockcp.Tenant+":agent:billing-agent"),
			},
		},
	})
}

func TestAccRemoteAgent_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + remoteAgentConfig},
			{
				ResourceName:      "truefoundry_remote_agent.billing",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "billing-agent",
			},
		},
	})
}

func TestAccRemoteAgent_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "a2a framework without agent_card_path",
			config: `
resource "truefoundry_remote_agent" "bad" {
  name        = "spike-bad"
  description = "bad"
  url         = "https://example.com/agent"
  framework   = { type = "a2a" }
}
`,
			want: regexp.MustCompile("`agent_card_path` is required"),
		},
		{
			name: "custom framework with agent_card_path",
			config: `
resource "truefoundry_remote_agent" "bad" {
  name        = "spike-bad"
  description = "bad"
  url         = "https://example.com/agent"
  framework = {
    type            = "custom"
    agent_card_path = "/.well-known/agent.json"
  }
}
`,
			want: regexp.MustCompile("`agent_card_path` must not be set"),
		},
		{
			name: "header auth without headers",
			config: `
resource "truefoundry_remote_agent" "bad" {
  name        = "spike-bad"
  description = "bad"
  url         = "https://example.com/agent"
  framework   = { type = "custom" }
  auth        = { type = "header" }
}
`,
			want: regexp.MustCompile("`headers` is required"),
		},
		{
			name: "passthrough auth with headers",
			config: `
resource "truefoundry_remote_agent" "bad" {
  name        = "spike-bad"
  description = "bad"
  url         = "https://example.com/agent"
  framework   = { type = "custom" }
  auth = {
    type    = "passthrough"
    headers = { "Authorization" = "Bearer token" }
  }
}
`,
			want: regexp.MustCompile("`headers` must not be set"),
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
