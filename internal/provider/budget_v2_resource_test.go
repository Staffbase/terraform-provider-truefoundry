package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// The v2 equivalent of the live dev setup: a per-cluster daily cost budget
// with a Slack alert at 25% usage.
const tenantBudgetConfigConfig = `
resource "truefoundry_tenant_budget_config" "cluster" {
  name = "devde1truefoundrydevelop"
  mode = "enforce"

  limits = {
    cost_per_day = 150
  }

  applies_to = {
    type = "aggregate"
  }

  when = {
    metadata = {
      tfy_gateway_cluster = { in = ["devde1truefoundrydevelop"] }
    }
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
}
`

func TestAccTenantBudgetConfig_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.BudgetCount(); n != 0 {
				return fmt.Errorf("%d budget rules left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + tenantBudgetConfigConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_tenant_budget_config.cluster", "id"),
					resource.TestCheckResourceAttr("truefoundry_tenant_budget_config.cluster", "mode", "enforce"),
					resource.TestCheckResourceAttr("truefoundry_tenant_budget_config.cluster", "limits.cost_per_day", "150"),
					resource.TestCheckResourceAttr("truefoundry_tenant_budget_config.cluster", "applies_to.type", "aggregate"),
					resource.TestCheckResourceAttr("truefoundry_tenant_budget_config.cluster", "when.metadata.tfy_gateway_cluster.in.#", "1"),
					resource.TestCheckTypeSetElemAttr("truefoundry_tenant_budget_config.cluster", "alerts.thresholds.*", "25"),
				),
			},
			// In-place update: several periods at once, switch to audit mode,
			// add subject and model filters, drop the alerts.
			{
				Config: providerConfig(host) + `
resource "truefoundry_tenant_budget_config" "cluster" {
  name = "devde1truefoundrydevelop"
  mode = "audit"

  limits = {
    cost_per_day   = 200
    cost_per_month = 4000
  }

  applies_to = {
    type = "aggregate"
  }

  when = {
    subjects = {
      users = { not_in = ["falk@staffbase.com"] }
      teams = { in = ["platform"] }
    }
    models            = { in = ["gpt-4o"] }
    provider_accounts = { not_in = ["openai-main"] }
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_tenant_budget_config.cluster", "mode", "audit"),
					resource.TestCheckResourceAttr("truefoundry_tenant_budget_config.cluster", "limits.cost_per_day", "200"),
					resource.TestCheckResourceAttr("truefoundry_tenant_budget_config.cluster", "limits.cost_per_month", "4000"),
					resource.TestCheckNoResourceAttr("truefoundry_tenant_budget_config.cluster", "alerts.thresholds.#"),
					resource.TestCheckTypeSetElemAttr("truefoundry_tenant_budget_config.cluster", "when.subjects.users.not_in.*", "falk@staffbase.com"),
					resource.TestCheckTypeSetElemAttr("truefoundry_tenant_budget_config.cluster", "when.provider_accounts.not_in.*", "openai-main"),
				),
			},
			// Per-user partitioning with an override and a breaching-user alert.
			{
				Config: providerConfig(host) + `
resource "truefoundry_tenant_budget_config" "cluster" {
  name = "devde1truefoundrydevelop"
  mode = "enforce"

  limits = {
    cost_per_day = 5
  }

  applies_to = {
    type = "per-user"
    overrides = [
      {
        users  = ["falk@staffbase.com"]
        limits = { cost_per_day = 50 }
      },
    ]
  }

  alerts = {
    thresholds            = [90, 100]
    notify_breaching_user = true
    notification_targets = [
      {
        type                 = "email"
        notification_channel = "tenant:email:default:notification-channel:alerts"
        to_emails            = ["{{user.email}}"]
      },
    ]
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_tenant_budget_config.cluster", "applies_to.type", "per-user"),
					resource.TestCheckResourceAttr("truefoundry_tenant_budget_config.cluster", "applies_to.overrides.#", "1"),
					resource.TestCheckResourceAttr("truefoundry_tenant_budget_config.cluster", "applies_to.overrides.0.limits.cost_per_day", "50"),
					resource.TestCheckResourceAttr("truefoundry_tenant_budget_config.cluster", "alerts.notify_breaching_user", "true"),
				),
			},
		},
	})
}

func TestAccTenantBudgetConfig_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + tenantBudgetConfigConfig},
			{
				ResourceName:      "truefoundry_tenant_budget_config.cluster",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "devde1truefoundrydevelop",
			},
		},
	})
}

// A rule deleted outside Terraform must be planned for recreation, not error.
func TestAccTenantBudgetConfig_recreatesAfterDrift(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + tenantBudgetConfigConfig},
			{
				PreConfig: func() { mock.DeleteBudgetByName("devde1truefoundrydevelop") },
				Config:    providerConfig(host) + tenantBudgetConfigConfig,
				Check:     resource.TestCheckResourceAttrSet("truefoundry_tenant_budget_config.cluster", "id"),
			},
		},
	})
}

const teamBudgetConfigConfig = `
resource "truefoundry_team_budget_config" "platform" {
  name      = "platform-per-user"
  team_name = "platform"
  mode      = "enforce"

  limits = {
    cost_per_day   = 1
    cost_per_month = 20
  }

  applies_to = {
    type = "per-user"
    overrides = [
      {
        users  = ["falk@staffbase.com"]
        limits = { cost_per_day = 5 }
      },
    ]
  }

  when = {
    models = { in = ["gpt-4o"] }
  }
}
`

func TestAccTeamBudgetConfig_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.BudgetCount(); n != 0 {
				return fmt.Errorf("%d budget rules left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + teamBudgetConfigConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_team_budget_config.platform", "id"),
					resource.TestCheckResourceAttr("truefoundry_team_budget_config.platform", "team_name", "platform"),
					resource.TestCheckResourceAttr("truefoundry_team_budget_config.platform", "limits.cost_per_month", "20"),
					resource.TestCheckTypeSetElemAttr("truefoundry_team_budget_config.platform", "applies_to.overrides.0.users.*", "falk@staffbase.com"),
				),
			},
			// Changing the owning team replaces the rule.
			{
				Config: providerConfig(host) + `
resource "truefoundry_team_budget_config" "platform" {
  name      = "platform-per-user"
  team_name = "mock-team"
  mode      = "enforce"

  limits = {
    cost_per_day   = 1
    cost_per_month = 20
  }

  applies_to = {
    type = "per-user"
    overrides = [
      {
        users  = ["falk@staffbase.com"]
        limits = { cost_per_day = 5 }
      },
    ]
  }

  when = {
    models = { in = ["gpt-4o"] }
  }
}
`,
				Check: resource.TestCheckResourceAttr("truefoundry_team_budget_config.platform", "team_name", "mock-team"),
			},
			{
				ResourceName:      "truefoundry_team_budget_config.platform",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "platform-per-user",
			},
		},
	})
}

func TestAccTeamBudgetConfig_unknownTeamRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + `
resource "truefoundry_team_budget_config" "bad" {
  name       = "nope"
  team_name  = "no-such-team"
  mode       = "enforce"
  limits     = { cost_per_day = 1 }
  applies_to = { type = "aggregate" }
}
`,
				ExpectError: regexp.MustCompile(`Invalid team id`),
			},
		},
	})
}

func TestAccBudgetConfigV2_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "empty limits",
			config: `
resource "truefoundry_tenant_budget_config" "bad" {
  name       = "bad"
  mode       = "enforce"
  limits     = {}
  applies_to = { type = "aggregate" }
}
`,
			want: regexp.MustCompile(`Set at least one of cost_per_day`),
		},
		{
			name: "lifetime combined with a resetting period",
			config: `
resource "truefoundry_tenant_budget_config" "bad" {
  name       = "bad"
  mode       = "enforce"
  limits     = { cost_per_day = 1, cost_per_lifetime = 100 }
  applies_to = { type = "aggregate" }
}
`,
			want: regexp.MustCompile("cost_per_lifetime` cannot be combined"),
		},
		{
			name: "unknown mode",
			config: `
resource "truefoundry_tenant_budget_config" "bad" {
  name       = "bad"
  mode       = "warn"
  limits     = { cost_per_day = 1 }
  applies_to = { type = "aggregate" }
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "invalid name",
			config: `
resource "truefoundry_tenant_budget_config" "bad" {
  name       = "-bad-name-"
  mode       = "enforce"
  limits     = { cost_per_day = 1 }
  applies_to = { type = "aggregate" }
}
`,
			want: regexp.MustCompile(`must start with a letter`),
		},
		{
			name: "metadata key without metadata type",
			config: `
resource "truefoundry_tenant_budget_config" "bad" {
  name       = "bad"
  mode       = "enforce"
  limits     = { cost_per_day = 1 }
  applies_to = { type = "aggregate", metadata = "team" }
}
`,
			want: regexp.MustCompile(`only valid when ` + "`applies_to.type`" + ` is ` + "`metadata`"),
		},
		{
			name: "metadata type without metadata key",
			config: `
resource "truefoundry_tenant_budget_config" "bad" {
  name       = "bad"
  mode       = "enforce"
  limits     = { cost_per_day = 1 }
  applies_to = { type = "metadata" }
}
`,
			want: regexp.MustCompile(`is required when`),
		},
		{
			name: "override on aggregate budget",
			config: `
resource "truefoundry_tenant_budget_config" "bad" {
  name   = "bad"
  mode   = "enforce"
  limits = { cost_per_day = 1 }
  applies_to = {
    type      = "aggregate"
    overrides = [{ users = ["a@example.com"], limits = { cost_per_day = 2 } }]
  }
}
`,
			want: regexp.MustCompile(`not valid when ` + "`applies_to.type`" + ` is ` + "`aggregate`"),
		},
		{
			name: "override entities of the wrong kind",
			config: `
resource "truefoundry_tenant_budget_config" "bad" {
  name   = "bad"
  mode   = "enforce"
  limits = { cost_per_day = 1 }
  applies_to = {
    type      = "per-user"
    overrides = [{ models = ["gpt-4o"], limits = { cost_per_day = 2 } }]
  }
}
`,
			want: regexp.MustCompile(`is not valid for ` + "`per-user`" + ` budgets`),
		},
		{
			name: "notify_breaching_user on aggregate budget",
			config: `
resource "truefoundry_tenant_budget_config" "bad" {
  name       = "bad"
  mode       = "enforce"
  limits     = { cost_per_day = 1 }
  applies_to = { type = "aggregate" }
  alerts = {
    thresholds            = [90]
    notify_breaching_user = true
    notification_targets = [
      { type = "slack-webhook", notification_channel = "tenant:slack:s:notification-channel:c" },
    ]
  }
}
`,
			want: regexp.MustCompile(`only valid when ` + "`applies_to.type`" + ` is ` + "`per-user`"),
		},
		{
			name: "threshold above 100",
			config: `
resource "truefoundry_tenant_budget_config" "bad" {
  name       = "bad"
  mode       = "enforce"
  limits     = { cost_per_day = 1 }
  applies_to = { type = "aggregate" }
  alerts = {
    thresholds = [150]
    notification_targets = [
      { type = "slack-webhook", notification_channel = "tenant:slack:s:notification-channel:c" },
    ]
  }
}
`,
			want: regexp.MustCompile(`must be greater than 0 and at`),
		},
		{
			name: "empty filter",
			config: `
resource "truefoundry_tenant_budget_config" "bad" {
  name       = "bad"
  mode       = "enforce"
  limits     = { cost_per_day = 1 }
  applies_to = { type = "aggregate" }
  when       = { models = {} }
}
`,
			want: regexp.MustCompile(`Set ` + "`in`" + `, ` + "`not_in`" + `, or both`),
		},
		{
			name: "team filter on a team budget",
			config: `
resource "truefoundry_team_budget_config" "bad" {
  name       = "bad"
  team_name  = "platform"
  mode       = "enforce"
  limits     = { cost_per_day = 1 }
  applies_to = { type = "aggregate" }
  when       = { subjects = { teams = { in = ["platform"] } } }
}
`,
			want: regexp.MustCompile(`not valid on a team budget`),
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
