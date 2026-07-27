package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

const promptConfig = mlRepoConfig + `
resource "truefoundry_prompt" "triage" {
  name          = "ticket-triage"
  ml_repo       = truefoundry_ml_repo.artifacts.name
  description   = "Classifies inbound support tickets"
  version_alias = "v1.0.0"

  messages = [
    {
      role    = "system"
      content = "You triage support tickets for the {{product}} team."
    },
    {
      role    = "user"
      content = "Ticket: {{ticket}}"
    },
  ]

  variables = {
    product = "app"
    ticket  = ""
  }

  model_configuration = {
    provider = "azure-main"
    model    = "gpt-5-mini"
    parameters = {
      max_tokens  = 2048
      temperature = 0.1
      stop        = ["END"]
    }
    extra_parameters = jsonencode({ seed = 42 })
  }
}
`

func TestAccPrompt_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	minimalConfig := providerConfig(host) + mlRepoConfig + `
resource "truefoundry_prompt" "triage" {
  name    = "ticket-triage"
  ml_repo = truefoundry_ml_repo.artifacts.name

  messages = [
    {
      role    = "system"
      content = "Answer the ticket directly."
    },
  ]
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.PromptCount(); n != 0 {
				return fmt.Errorf("%d prompts left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + promptConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_prompt.triage", "id"),
					resource.TestCheckResourceAttr("truefoundry_prompt.triage", "fqn",
						"chat_prompt:"+mockcp.Tenant+"/gateway-artifacts/ticket-triage"),
					resource.TestCheckResourceAttr("truefoundry_prompt.triage", "messages.#", "2"),
					resource.TestCheckResourceAttr("truefoundry_prompt.triage", "messages.0.role", "system"),
					resource.TestCheckResourceAttr("truefoundry_prompt.triage", "messages.1.content", "Ticket: {{ticket}}"),
					resource.TestCheckResourceAttr("truefoundry_prompt.triage", "variables.product", "app"),
					resource.TestCheckResourceAttr("truefoundry_prompt.triage", "model_configuration.provider", "azure-main"),
					resource.TestCheckResourceAttr("truefoundry_prompt.triage", "model_configuration.parameters.max_tokens", "2048"),
					resource.TestCheckResourceAttr("truefoundry_prompt.triage", "model_configuration.parameters.stop.0", "END"),
				),
			},
			// In-place update (a new version): minimal manifest without
			// variables and model configuration.
			{
				Config: minimalConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_prompt.triage", "messages.#", "1"),
					resource.TestCheckResourceAttr("truefoundry_prompt.triage", "messages.0.content", "Answer the ticket directly."),
					resource.TestCheckNoResourceAttr("truefoundry_prompt.triage", "variables"),
					resource.TestCheckNoResourceAttr("truefoundry_prompt.triage", "model_configuration"),
				),
			},
			// Out-of-band deletion is detected and the prompt is recreated.
			{
				PreConfig: func() { mock.DeletePromptByName("ticket-triage") },
				Config:    minimalConfig,
				Check: resource.TestCheckResourceAttr("truefoundry_prompt.triage", "fqn",
					"chat_prompt:"+mockcp.Tenant+"/gateway-artifacts/ticket-triage"),
			},
		},
	})
}

func TestAccPrompt_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + promptConfig},
			{
				ResourceName:      "truefoundry_prompt.triage",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "ticket-triage",
			},
		},
	})
}

func TestAccPrompt_validation(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "invalid message role",
			config: `
resource "truefoundry_prompt" "bad" {
  name     = "spike-bad"
  ml_repo  = "gateway-artifacts"
  messages = [{ role = "robot", content = "beep" }]
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "uppercase name",
			config: `
resource "truefoundry_prompt" "bad" {
  name     = "Spike-Bad"
  ml_repo  = "gateway-artifacts"
  messages = [{ role = "system", content = "x" }]
}
`,
			want: regexp.MustCompile("must be lowercase alphanumeric with hyphens"),
		},
		{
			// The control plane's referential check: prompts may only live in
			// existing ML repos.
			name: "unknown ml_repo",
			config: `
resource "truefoundry_prompt" "bad" {
  name     = "spike-bad"
  ml_repo  = "does-not-exist"
  messages = [{ role = "system", content = "x" }]
}
`,
			want: regexp.MustCompile(`(?s)ML Repo\s+\\"does-not-exist\\" not\s+found`),
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
