package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Mirrors the live dev setup: a per-cluster daily cost budget with a Slack
// alert at 25% usage.
const gatewayBudgetConfigConfig = `
resource "truefoundry_gateway_budget_config" "budget" {
  name = "budget"

  rules = [
    {
      id       = "devde1truefoundrydevelop"
      limit_to = 150
      unit     = "cost_per_day"

      when = {
        metadata = { tfy_gateway_cluster = "devde1truefoundrydevelop" }
      }

      alerts = {
        thresholds = [25]
        notification_targets = [
          {
            type                 = "slack-webhook"
            notification_channel = "tenant:slack:slack-webhooks:notification-channel:team-ai-foundation"
          },
        ]
      }

      audit_mode = false
    },
  ]
}
`

func TestAccGatewayBudgetConfig_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.GatewayConfigCount(); n != 0 {
				return fmt.Errorf("%d gateway configs left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + gatewayBudgetConfigConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_gateway_budget_config.budget", "id"),
					resource.TestCheckResourceAttr("truefoundry_gateway_budget_config.budget", "rules.#", "1"),
					resource.TestCheckResourceAttr("truefoundry_gateway_budget_config.budget", "rules.0.unit", "cost_per_day"),
					resource.TestCheckResourceAttr("truefoundry_gateway_budget_config.budget", "rules.0.when.metadata.tfy_gateway_cluster", "devde1truefoundrydevelop"),
					resource.TestCheckResourceAttr("truefoundry_gateway_budget_config.budget", "rules.0.alerts.thresholds.0", "25"),
				),
			},
			// In-place update: second rule scoped to subjects and models,
			// tracked per user, token-based, without alerts.
			{
				Config: providerConfig(host) + `
resource "truefoundry_gateway_budget_config" "budget" {
  name = "budget"

  rules = [
    {
      id       = "devde1truefoundrydevelop"
      limit_to = 200
      unit     = "cost_per_day"
      when = {
        metadata = { tfy_gateway_cluster = "devde1truefoundrydevelop" }
      }
    },
    {
      id       = "ci-tokens"
      limit_to = 1000000
      unit     = "tokens_per_day"
      when = {
        subjects = ["virtualaccount:continuous-delivery"]
        models   = ["tenant:azure-openai:eu:model:gpt-4o"]
      }
      budget_applies_per = ["user"]
      audit_mode         = true
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_gateway_budget_config.budget", "rules.#", "2"),
					resource.TestCheckResourceAttr("truefoundry_gateway_budget_config.budget", "rules.0.limit_to", "200"),
					resource.TestCheckResourceAttr("truefoundry_gateway_budget_config.budget", "rules.1.budget_applies_per.0", "user"),
					resource.TestCheckResourceAttr("truefoundry_gateway_budget_config.budget", "rules.1.audit_mode", "true"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_budget_config.budget", "rules.1.alerts"),
				),
			},
		},
	})
}

func TestAccGatewayBudgetConfig_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + gatewayBudgetConfigConfig},
			{
				ResourceName:      "truefoundry_gateway_budget_config.budget",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "gateway-budget-config",
			},
		},
	})
}

func TestAccGatewayBudgetConfig_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "duplicate rule ids",
			config: `
resource "truefoundry_gateway_budget_config" "bad" {
  name = "budget"
  rules = [
    { id = "r1", limit_to = 10, unit = "cost_per_day", when = {} },
    { id = "r1", limit_to = 20, unit = "cost_per_month", when = {} },
  ]
}
`,
			want: regexp.MustCompile(`Duplicate rule id`),
		},
		{
			name: "invalid unit",
			config: `
resource "truefoundry_gateway_budget_config" "bad" {
  name = "budget"
  rules = [
    { id = "r1", limit_to = 10, unit = "cost_per_hour", when = {} },
  ]
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "to_emails on slack target",
			config: `
resource "truefoundry_gateway_budget_config" "bad" {
  name = "budget"
  rules = [
    {
      id       = "r1"
      limit_to = 10
      unit     = "cost_per_day"
      when     = {}
      alerts = {
        thresholds = [50]
        notification_targets = [
          {
            type                 = "slack-webhook"
            notification_channel = "tenant:slack:s:notification-channel:c"
            to_emails            = ["x@example.com"]
          },
        ]
      }
    },
  ]
}
`,
			want: regexp.MustCompile("to_emails` is only valid on email targets"),
		},
		{
			name: "empty thresholds",
			config: `
resource "truefoundry_gateway_budget_config" "bad" {
  name = "budget"
  rules = [
    {
      id       = "r1"
      limit_to = 10
      unit     = "cost_per_day"
      when     = {}
      alerts = {
        thresholds = []
        notification_targets = [
          {
            type                 = "slack-webhook"
            notification_channel = "tenant:slack:s:notification-channel:c"
          },
        ]
      }
    },
  ]
}
`,
			want: regexp.MustCompile(`at least 1 elements`),
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
