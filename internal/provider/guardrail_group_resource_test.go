package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/Staffbase/terraform-provider-truefoundry/internal/mockcp"
)

// Mirrors the guardrail setup on the dev tenant: one prompt shield and one
// content safety config per region group, keys as Vault secret references.
// Guardrails are in alphabetical order so import verification is stable.
const guardrailGroupConfig = `
resource "truefoundry_guardrail_group" "region" {
  name = "spike-gr-francecentral"

  collaborators = [
    {
      subject = "user:falk@staffbase.com"
      role_id = "provider-account-manager"
    },
  ]

  guardrails = [
    {
      type               = "azure-content-safety"
      name               = "spike-cs-francecentral"
      description        = "Content safety for hate, self-harm, sexual, and violence detection"
      api_key            = "tenant:hashicorp:vault-secrets:secret-store:vault::contentsafety/francecentral/value"
      enforcing_strategy = "enforce_but_ignore_on_error"
      resource_name      = "cog-guardrails-francecentral"
      api_version        = "2024-09-01"
      severity_threshold = 4
      categories         = ["Hate", "SelfHarm", "Sexual", "Violence"]
    },
    {
      type               = "azure-prompt-shield"
      name               = "spike-ps-francecentral"
      description        = "Prompt shield for jailbreak detection"
      api_key            = "tenant:hashicorp:vault-secrets:secret-store:vault::contentsafety/francecentral/value"
      enforcing_strategy = "enforce_but_ignore_on_error"
      resource_name      = "cog-guardrails-francecentral"
      api_version        = "2024-09-01"
    },
  ]
}
`

func TestAccGuardrailGroup_lifecycle(t *testing.T) {
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
				Config: providerConfig(host) + guardrailGroupConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_guardrail_group.region", "fqn", mockcp.Tenant+":guardrail-config-group:spike-gr-francecentral"),
					resource.TestCheckResourceAttr("truefoundry_guardrail_group.region", "guardrails.#", "2"),
					resource.TestCheckResourceAttr("truefoundry_guardrail_group.region", "guardrails.0.type", "azure-content-safety"),
					resource.TestCheckResourceAttr("truefoundry_guardrail_group.region", "guardrails.0.severity_threshold", "4"),
					resource.TestCheckResourceAttr("truefoundry_guardrail_group.region", "guardrails.0.categories.#", "4"),
					resource.TestCheckResourceAttr("truefoundry_guardrail_group.region", "guardrails.1.type", "azure-prompt-shield"),
					resource.TestCheckResourceAttr("truefoundry_guardrail_group.region", "collaborators.0.role_id", "provider-account-manager"),
				),
			},
			// In-place update: stricter threshold, audit strategy, blocklist.
			{
				Config: providerConfig(host) + `
resource "truefoundry_guardrail_group" "region" {
  name = "spike-gr-francecentral"

  guardrails = [
    {
      type               = "azure-content-safety"
      name               = "spike-cs-francecentral"
      api_key            = "tenant:hashicorp:vault-secrets:secret-store:vault::contentsafety/francecentral/value"
      enforcing_strategy = "audit"
      resource_name      = "cog-guardrails-francecentral"
      api_version        = "2024-09-01"
      severity_threshold = 2
      categories         = ["Hate", "Violence"]
      blocklist_names    = ["internal-terms"]
    },
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_guardrail_group.region", "guardrails.#", "1"),
					resource.TestCheckResourceAttr("truefoundry_guardrail_group.region", "guardrails.0.enforcing_strategy", "audit"),
					resource.TestCheckResourceAttr("truefoundry_guardrail_group.region", "guardrails.0.severity_threshold", "2"),
					resource.TestCheckResourceAttr("truefoundry_guardrail_group.region", "guardrails.0.blocklist_names.0", "internal-terms"),
					func(*terraform.State) error {
						if n := mock.AccountCount(); n != 1 {
							return fmt.Errorf("control plane has %d accounts, want 1", n)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccGuardrailGroup_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + guardrailGroupConfig},
			{
				ResourceName:      "truefoundry_guardrail_group.region",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     mockcp.Tenant + ":guardrail-config-group:spike-gr-francecentral",
			},
		},
	})
}

func TestAccGuardrailGroup_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	base := `
resource "truefoundry_guardrail_group" "bad" {
  name = "spike-gr-bad"
  guardrails = [%s]
}
`
	cases := []struct {
		name      string
		guardrail string
		want      *regexp.Regexp
	}{
		{
			name: "content safety without severity threshold",
			guardrail: `{
      type               = "azure-content-safety"
      name               = "cs"
      api_key            = "key"
      enforcing_strategy = "enforce"
      resource_name      = "res"
      api_version        = "2024-09-01"
      categories         = ["Hate"]
    }`,
			want: regexp.MustCompile("severity_threshold` is required"),
		},
		{
			name: "prompt shield with categories",
			guardrail: `{
      type               = "azure-prompt-shield"
      name               = "ps"
      api_key            = "key"
      enforcing_strategy = "enforce"
      resource_name      = "res"
      api_version        = "2024-09-01"
      categories         = ["Hate"]
    }`,
			want: regexp.MustCompile("categories` must not be set"),
		},
		{
			name: "invalid enforcing strategy",
			guardrail: `{
      type               = "azure-prompt-shield"
      name               = "ps"
      api_key            = "key"
      enforcing_strategy = "block-everything"
      resource_name      = "res"
      api_version        = "2024-09-01"
    }`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "invalid category",
			guardrail: `{
      type               = "azure-content-safety"
      name               = "cs"
      api_key            = "key"
      enforcing_strategy = "enforce"
      resource_name      = "res"
      api_version        = "2024-09-01"
      severity_threshold = 4
      categories         = ["Gossip"]
    }`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "severity threshold out of range",
			guardrail: `{
      type               = "azure-content-safety"
      name               = "cs"
      api_key            = "key"
      enforcing_strategy = "enforce"
      resource_name      = "res"
      api_version        = "2024-09-01"
      severity_threshold = 9
      categories         = ["Hate"]
    }`,
			want: regexp.MustCompile(`value must be between 0 and 6`),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6Factories,
				Steps: []resource.TestStep{
					{Config: providerConfig(host) + fmt.Sprintf(base, tc.guardrail), ExpectError: tc.want},
				},
			})
		})
	}
}
