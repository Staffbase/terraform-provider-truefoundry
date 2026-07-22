package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const gatewayGlobalSettingsConfig = `
resource "truefoundry_gateway_global_settings" "settings" {
  retry_settings = {
    attempts        = 3
    delay           = 1000
    on_status_codes = ["429", "503"]
  }

  logging_settings = {
    type                = "header_controlled"
    log_body_by_default = false
  }

  model_configuration = {
    allowed_models = []
  }
}
`

func TestAccGatewayGlobalSettings_lifecycle(t *testing.T) {
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
				Config: providerConfig(host) + gatewayGlobalSettingsConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_gateway_global_settings.settings", "id"),
					resource.TestCheckResourceAttr("truefoundry_gateway_global_settings.settings", "retry_settings.attempts", "3"),
					resource.TestCheckResourceAttr("truefoundry_gateway_global_settings.settings", "retry_settings.on_status_codes.#", "2"),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_global_settings.settings", "retry_settings.on_status_codes.*", "429"),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_global_settings.settings", "retry_settings.on_status_codes.*", "503"),
					resource.TestCheckResourceAttr("truefoundry_gateway_global_settings.settings", "logging_settings.log_body_by_default", "false"),
					// The empty allowlist must survive as an empty (non-null) set.
					resource.TestCheckResourceAttr("truefoundry_gateway_global_settings.settings", "model_configuration.allowed_models.#", "0"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_global_settings.settings", "semantic_cache_settings"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_global_settings.settings", "guardrail_settings"),
				),
			},
			// In-place update: drop optional retry fields and the model
			// allowlist, switch logging mode, add caching and guardrails.
			{
				Config: providerConfig(host) + `
resource "truefoundry_gateway_global_settings" "settings" {
  retry_settings = {
    attempts = 5
  }

  logging_settings = {
    type = "always"
  }

  semantic_cache_settings = {
    semantic_cache_embedding_model = "generic/text-embedding-3-small"
  }

  guardrail_settings = {
    timeout_ms                = 5000
    enable_parallel_execution = true
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_gateway_global_settings.settings", "retry_settings.attempts", "5"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_global_settings.settings", "retry_settings.delay"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_global_settings.settings", "retry_settings.on_status_codes"),
					resource.TestCheckResourceAttr("truefoundry_gateway_global_settings.settings", "logging_settings.type", "always"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_global_settings.settings", "logging_settings.log_body_by_default"),
					resource.TestCheckResourceAttr("truefoundry_gateway_global_settings.settings", "semantic_cache_settings.semantic_cache_embedding_model", "generic/text-embedding-3-small"),
					resource.TestCheckResourceAttr("truefoundry_gateway_global_settings.settings", "guardrail_settings.timeout_ms", "5000"),
					resource.TestCheckResourceAttr("truefoundry_gateway_global_settings.settings", "guardrail_settings.enable_parallel_execution", "true"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_global_settings.settings", "model_configuration"),
				),
			},
			// All settings unset is a valid manifest: everything pinned to
			// platform defaults.
			{
				Config: providerConfig(host) + `
resource "truefoundry_gateway_global_settings" "settings" {
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_gateway_global_settings.settings", "id"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_global_settings.settings", "retry_settings"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_global_settings.settings", "logging_settings"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_global_settings.settings", "semantic_cache_settings"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_global_settings.settings", "guardrail_settings"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_global_settings.settings", "model_configuration"),
				),
			},
		},
	})
}

func TestAccGatewayGlobalSettings_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + gatewayGlobalSettingsConfig},
			{
				ResourceName:      "truefoundry_gateway_global_settings.settings",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "gateway-global-settings",
			},
		},
	})
}

func TestAccGatewayGlobalSettings_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "log_body_by_default without header_controlled",
			config: `
resource "truefoundry_gateway_global_settings" "bad" {
  logging_settings = {
    type                = "always"
    log_body_by_default = true
  }
}
`,
			want: regexp.MustCompile("log_body_by_default` is only valid with logging type"),
		},
		{
			name: "header_controlled without log_body_by_default",
			config: `
resource "truefoundry_gateway_global_settings" "bad" {
  logging_settings = {
    type = "header_controlled"
  }
}
`,
			want: regexp.MustCompile("log_body_by_default` is required with logging type"),
		},
		{
			name: "invalid logging type",
			config: `
resource "truefoundry_gateway_global_settings" "bad" {
  logging_settings = {
    type = "sometimes"
  }
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "retry settings without attempts",
			config: `
resource "truefoundry_gateway_global_settings" "bad" {
  retry_settings = {}
}
`,
			want: regexp.MustCompile(`"attempts" is\s+required`),
		},
		{
			name: "zero retry attempts",
			config: `
resource "truefoundry_gateway_global_settings" "bad" {
  retry_settings = {
    attempts = 0
  }
}
`,
			want: regexp.MustCompile(`at least 1`),
		},
		{
			name: "empty on_status_codes",
			config: `
resource "truefoundry_gateway_global_settings" "bad" {
  retry_settings = {
    attempts        = 2
    on_status_codes = []
  }
}
`,
			want: regexp.MustCompile(`at least 1\s+elements`),
		},
		{
			name: "semantic cache without embedding model",
			config: `
resource "truefoundry_gateway_global_settings" "bad" {
  semantic_cache_settings = {}
}
`,
			want: regexp.MustCompile(`"semantic_cache_embedding_model" is\s+required`),
		},
		{
			name: "empty guardrail settings",
			config: `
resource "truefoundry_gateway_global_settings" "bad" {
  guardrail_settings = {}
}
`,
			want: regexp.MustCompile("needs at least one of `timeout_ms`"),
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
