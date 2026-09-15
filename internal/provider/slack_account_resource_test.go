package provider_test

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/client"
	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

// Mirrors the live dev setup: a webhook notification channel with the URL in
// a Vault secret reference.
const slackAccountConfig = `
resource "truefoundry_slack_account" "notifications" {
  name = "spike-slack-webhooks"

  webhooks = [
    {
      name        = "team-ai-foundation"
      webhook_url = "tenant:hashicorp:vault-secrets:secret-store:vault::slack/team-ai-foundation/value"
    },
  ]
}
`

func TestAccSlackAccount_lifecycle(t *testing.T) {
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
				Config: providerConfig(host) + slackAccountConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_slack_account.notifications", "fqn", mockcp.Tenant+":slack:spike-slack-webhooks"),
					resource.TestCheckResourceAttr("truefoundry_slack_account.notifications", "webhooks.#", "1"),
					resource.TestCheckNoResourceAttr("truefoundry_slack_account.notifications", "bots"),
				),
			},
			// In-place update: add a bot channel with authorized subjects.
			{
				Config: providerConfig(host) + `
resource "truefoundry_slack_account" "notifications" {
  name = "spike-slack-webhooks"

  webhooks = [
    {
      name        = "team-ai-foundation"
      webhook_url = "tenant:hashicorp:vault-secrets:secret-store:vault::slack/team-ai-foundation/value"
    },
  ]

  bots = [
    {
      name                = "alerts-bot"
      bot_token           = "tenant:hashicorp:vault-secrets:secret-store:vault::slack/alerts-bot/value"
      authorized_subjects = ["team:everyone"]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_slack_account.notifications", "bots.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_slack_account.notifications", "bots.*", map[string]string{
						"name":                  "alerts-bot",
						"authorized_subjects.#": "1",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_slack_account.notifications", "bots.*.authorized_subjects.*", "team:everyone"),
				),
			},
			// Bots only.
			{
				Config: providerConfig(host) + `
resource "truefoundry_slack_account" "notifications" {
  name = "spike-slack-webhooks"

  bots = [
    {
      name      = "alerts-bot"
      bot_token = "tenant:hashicorp:vault-secrets:secret-store:vault::slack/alerts-bot/value"
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("truefoundry_slack_account.notifications", "webhooks"),
					resource.TestCheckResourceAttr("truefoundry_slack_account.notifications", "bots.#", "1"),
				),
			},
		},
	})
}

// TestAccSlackAccount_channelTypeChange guards against Read() misrouting a
// channel that keeps its name but changes type out-of-band (e.g. someone
// applies a raw manifest against the control plane directly): it must land
// in bots on refresh, not get force-converted back into a stale webhook
// with a leftover URL and an empty (silently wrong) plan.
func TestAccSlackAccount_channelTypeChange(t *testing.T) {
	_, host := startMockControlPlane(t)
	c, err := client.New(host, testAPIKey, "test")
	if err != nil {
		t.Fatal(err)
	}

	config := providerConfig(host) + `
resource "truefoundry_slack_account" "notifications" {
  name = "spike-slack-webhooks"

  webhooks = [
    {
      name        = "shared-name"
      webhook_url = "tenant:hashicorp:vault-secrets:secret-store:vault::slack/shared-name/value"
    },
  ]
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  resource.TestCheckResourceAttr("truefoundry_slack_account.notifications", "webhooks.#", "1"),
			},
			{
				PreConfig: func() {
					// Simulate someone else replacing "shared-name" with a
					// bot channel directly against the control plane.
					if _, err := c.Apply(context.Background(), client.SlackAccountManifest{
						Type: "provider-account/slack",
						Name: "spike-slack-webhooks",
						Integrations: []client.SlackNotificationChannel{
							{
								Type: "integration/notification-channel/slack-bot",
								Name: "shared-name",
								AuthData: &client.APIKeyAuth{
									Type:     "slack-bot",
									BotToken: "tenant:hashicorp:vault-secrets:secret-store:vault::slack/shared-name-bot/value",
								},
							},
						},
					}, false); err != nil {
						t.Fatal(err)
					}
				},
				// Config still asks for the webhook; the control plane now
				// only has a bot with that name, so the plan must show a
				// change. Read() masking the type change would report state
				// matching config and produce an empty plan instead.
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccSlackAccount_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + slackAccountConfig},
			{
				ResourceName:      "truefoundry_slack_account.notifications",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     mockcp.Tenant + ":slack:spike-slack-webhooks",
				// The control plane masks webhook_url on read; a fresh
				// import has no prior state to preserve, so it reads back
				// as "[redacted]" (see slackAccountResource.Read).
				ImportStateVerifyIgnore: []string{"webhooks.0.webhook_url"},
			},
		},
	})
}

func TestAccSlackAccount_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "no channels",
			config: `
resource "truefoundry_slack_account" "bad" {
  name = "spike-bad"
}
`,
			want: regexp.MustCompile(`At least one webhook or bot`),
		},
		{
			name: "webhook without url",
			config: `
resource "truefoundry_slack_account" "bad" {
  name = "spike-bad"
  webhooks = [
    { name = "w1" },
  ]
}
`,
			want: regexp.MustCompile(`webhook_url`),
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
