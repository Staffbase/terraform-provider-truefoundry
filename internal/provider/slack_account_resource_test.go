package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

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
					resource.TestCheckResourceAttr("truefoundry_slack_account.notifications", "bots.0.authorized_subjects.0", "team:everyone"),
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
