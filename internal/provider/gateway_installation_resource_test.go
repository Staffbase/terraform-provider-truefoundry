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
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func gatewayInstallationConfig(name, url, extra string) string {
	return fmt.Sprintf(`
resource "truefoundry_gateway_installation" "develop" {
  name = %q
  url  = %q
%s
}
`, name, url, extra)
}

// checkTokenCount asserts how many tokens the mock minted for an installation.
func checkTokenCount(mock interface{ GatewayInstallationTokenCount(string) int }, name string, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := mock.GatewayInstallationTokenCount(name); got != want {
			return fmt.Errorf("%d tokens minted for %q, want %d", got, name, want)
		}
		return nil
	}
}

func TestAccGatewayInstallation_lifecycle(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		CheckDestroy: func(*terraform.State) error {
			if n := mock.GatewayInstallationCount(); n != 0 {
				return fmt.Errorf("%d gateway installations left after destroy, want 0", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerConfig(host) + gatewayInstallationConfig("develop", "https://gateway.example.com", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_gateway_installation.develop", "id"),
					resource.TestCheckResourceAttr("truefoundry_gateway_installation.develop", "enabled", "true"),
					resource.TestCheckResourceAttr("truefoundry_gateway_installation.develop", "is_default", "false"),
					resource.TestCheckResourceAttr("truefoundry_gateway_installation.develop", "token", "gwtoken-develop-1"),
					checkTokenCount(mock, "develop", 1),
				),
			},
			{
				// Updating in place keeps the token and mints no new one.
				Config: providerConfig(host) + gatewayInstallationConfig("develop", "https://gateway2.example.com", "  enabled = false"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("truefoundry_gateway_installation.develop", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_gateway_installation.develop", "url", "https://gateway2.example.com"),
					resource.TestCheckResourceAttr("truefoundry_gateway_installation.develop", "enabled", "false"),
					resource.TestCheckResourceAttr("truefoundry_gateway_installation.develop", "token", "gwtoken-develop-1"),
					checkTokenCount(mock, "develop", 1),
				),
			},
		},
	})
}

func TestAccGatewayInstallation_replaceOnRenameMintsNewToken(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + gatewayInstallationConfig("develop", "https://gateway.example.com", "")},
			{
				Config:           providerConfig(host) + gatewayInstallationConfig("sandbox", "https://gateway.example.com", ""),
				ConfigPlanChecks: replacePlanCheck("truefoundry_gateway_installation.develop"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_gateway_installation.develop", "token", "gwtoken-sandbox-1"),
					checkTokenCount(mock, "sandbox", 1),
				),
			},
		},
	})
}

func TestAccGatewayInstallation_urlDriftReverted(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + gatewayInstallationConfig("develop", "https://gateway.example.com", "")},
			{
				PreConfig: func() { mock.SetGatewayInstallationURL("develop", "https://changed.example.com") },
				Config:    providerConfig(host) + gatewayInstallationConfig("develop", "https://gateway.example.com", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("truefoundry_gateway_installation.develop", plancheck.ResourceActionUpdate),
					},
				},
				Check: checkTokenCount(mock, "develop", 1),
			},
		},
	})
}

func TestAccGatewayInstallation_deletedOutOfBandRecreated(t *testing.T) {
	mock, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + gatewayInstallationConfig("develop", "https://gateway.example.com", "")},
			{
				PreConfig: func() { mock.DeleteGatewayInstallationByName("develop") },
				Config:    providerConfig(host) + gatewayInstallationConfig("develop", "https://gateway.example.com", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_gateway_installation.develop", "token", "gwtoken-develop-1"),
					checkTokenCount(mock, "develop", 1),
				),
			},
		},
	})
}

func TestAccGatewayInstallation_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + gatewayInstallationConfig("develop", "https://gateway.example.com", "")},
			{
				ResourceName:            "truefoundry_gateway_installation.develop",
				ImportState:             true,
				ImportStateId:           "develop",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"token"},
			},
			{
				ResourceName:  "truefoundry_gateway_installation.develop",
				ImportState:   true,
				ImportStateId: "nope",
				ExpectError:   regexp.MustCompile(`no gateway installation named "nope"`),
			},
		},
	})
}

func TestAccGatewayInstallation_urlConflict(t *testing.T) {
	_, host := startMockControlPlane(t)

	config := gatewayInstallationConfig("develop", "https://gateway.example.com", "") + `
resource "truefoundry_gateway_installation" "other" {
  name       = "other"
  url        = "https://gateway.example.com"
  depends_on = [truefoundry_gateway_installation.develop]
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{
				Config:      providerConfig(host) + config,
				ExpectError: regexp.MustCompile(`HTTP 409`),
			},
		},
	})
}

func TestAccGatewayInstallation_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := map[string]struct {
		name, url string
		want      *regexp.Regexp
	}{
		"upper case name": {"Develop", "https://gateway.example.com", regexp.MustCompile(`must be 3-35 characters`)},
		"name too short":  {"ab", "https://gateway.example.com", regexp.MustCompile(`must be 3-35 characters`)},
		"name too long":   {"a23456789012345678901234567890123456", "https://gateway.example.com", regexp.MustCompile(`must be 3-35 characters`)},
		"underscore":      {"dev_gateway", "https://gateway.example.com", regexp.MustCompile(`must be 3-35 characters`)},
		"not a url":       {"develop", "gateway.example.com", regexp.MustCompile(`must be an http\(s\) URL`)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6Factories,
				Steps: []resource.TestStep{
					{Config: providerConfig(host) + gatewayInstallationConfig(tc.name, tc.url, ""), ExpectError: tc.want},
				},
			})
		})
	}
}
