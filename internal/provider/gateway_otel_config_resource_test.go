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
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Both exporters over HTTP, with the traces exporter filtering spans and
// stripping prompt/completion payloads.
const gatewayOtelConfigConfig = `
resource "truefoundry_gateway_otel_config" "otel" {
  otel_traces_exporter_config = {
    type     = "http"
    encoding = "proto"
    endpoint = "https://otel.example.com/v1/traces"
    headers  = { authorization = "Bearer collector-token" }

    additional_resource_attributes = { "service.namespace" = "ai-gateway" }
    exclude_request_data           = true

    when = [
      {
        key   = "tfy_gateway_cluster"
        value = ["eu-cluster"]
      },
    ]
  }

  otel_metrics_exporter_config = {
    type     = "http"
    encoding = "proto"
    endpoint = "https://otel.example.com/v1/metrics"
  }
}
`

func TestAccGatewayOtelConfig_lifecycle(t *testing.T) {
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
				Config: providerConfig(host) + gatewayOtelConfigConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_gateway_otel_config.otel", "id"),
					resource.TestCheckResourceAttr("truefoundry_gateway_otel_config.otel", "otel_traces_exporter_config.type", "http"),
					resource.TestCheckResourceAttr("truefoundry_gateway_otel_config.otel", "otel_traces_exporter_config.encoding", "proto"),
					resource.TestCheckResourceAttr("truefoundry_gateway_otel_config.otel", "otel_traces_exporter_config.endpoint", "https://otel.example.com/v1/traces"),
					resource.TestCheckResourceAttr("truefoundry_gateway_otel_config.otel", "otel_traces_exporter_config.exclude_request_data", "true"),
					resource.TestCheckResourceAttr("truefoundry_gateway_otel_config.otel", "otel_traces_exporter_config.additional_resource_attributes.service.namespace", "ai-gateway"),
					resource.TestCheckResourceAttr("truefoundry_gateway_otel_config.otel", "otel_traces_exporter_config.when.#", "1"),
					resource.TestCheckResourceAttr("truefoundry_gateway_otel_config.otel", "otel_traces_exporter_config.when.0.key", "tfy_gateway_cluster"),
					resource.TestCheckResourceAttr("truefoundry_gateway_otel_config.otel", "otel_metrics_exporter_config.endpoint", "https://otel.example.com/v1/metrics"),
				),
			},
			// In-place update: switch traces to gRPC (no encoding), drop the
			// metrics exporter and the span filter.
			{
				Config: providerConfig(host) + `
resource "truefoundry_gateway_otel_config" "otel" {
  otel_traces_exporter_config = {
    type     = "grpc"
    endpoint = "https://otel.example.com:4317"
    headers  = { authorization = "Bearer collector-token" }
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_gateway_otel_config.otel", "otel_traces_exporter_config.type", "grpc"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_otel_config.otel", "otel_traces_exporter_config.encoding"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_otel_config.otel", "otel_traces_exporter_config.when.#"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_otel_config.otel", "otel_metrics_exporter_config.endpoint"),
				),
			},
			// Metrics-only config.
			{
				Config: providerConfig(host) + `
resource "truefoundry_gateway_otel_config" "otel" {
  otel_metrics_exporter_config = {
    type     = "http"
    encoding = "json"
    endpoint = "https://otel.example.com/v1/metrics"
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("truefoundry_gateway_otel_config.otel", "otel_traces_exporter_config.endpoint"),
					resource.TestCheckResourceAttr("truefoundry_gateway_otel_config.otel", "otel_metrics_exporter_config.encoding", "json"),
				),
			},
		},
	})
}

func TestAccGatewayOtelConfig_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + gatewayOtelConfigConfig},
			{
				ResourceName:      "truefoundry_gateway_otel_config.otel",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "gateway-otel-config",
			},
		},
	})
}

func TestAccGatewayOtelConfig_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "no exporter",
			config: `
resource "truefoundry_gateway_otel_config" "bad" {
}
`,
			want: regexp.MustCompile("Empty OTel config"),
		},
		{
			name: "http exporter without encoding",
			config: `
resource "truefoundry_gateway_otel_config" "bad" {
  otel_traces_exporter_config = {
    type     = "http"
    endpoint = "https://otel.example.com/v1/traces"
  }
}
`,
			want: regexp.MustCompile("`encoding` is required for `http`"),
		},
		{
			name: "grpc exporter with encoding",
			config: `
resource "truefoundry_gateway_otel_config" "bad" {
  otel_metrics_exporter_config = {
    type     = "grpc"
    encoding = "proto"
    endpoint = "https://otel.example.com:4317"
  }
}
`,
			want: regexp.MustCompile("only valid for `http` exporters"),
		},
		{
			name: "unknown encoding",
			config: `
resource "truefoundry_gateway_otel_config" "bad" {
  otel_traces_exporter_config = {
    type     = "http"
    encoding = "protobuf"
    endpoint = "https://otel.example.com/v1/traces"
  }
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "unknown transport",
			config: `
resource "truefoundry_gateway_otel_config" "bad" {
  otel_traces_exporter_config = {
    type     = "kafka"
    endpoint = "https://otel.example.com/v1/traces"
  }
}
`,
			want: regexp.MustCompile(`value must be one of`),
		},
		{
			name: "empty endpoint",
			config: `
resource "truefoundry_gateway_otel_config" "bad" {
  otel_metrics_exporter_config = {
    type     = "http"
    encoding = "proto"
    endpoint = ""
  }
}
`,
			want: regexp.MustCompile(`string length must be at\s+least 1`),
		},
		{
			name: "span filter without values",
			config: `
resource "truefoundry_gateway_otel_config" "bad" {
  otel_traces_exporter_config = {
    type     = "http"
    encoding = "proto"
    endpoint = "https://otel.example.com/v1/traces"
    when     = [{ key = "tfy_gateway_cluster", value = [] }]
  }
}
`,
			want: regexp.MustCompile(`must contain at least\s+1 elements`),
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
