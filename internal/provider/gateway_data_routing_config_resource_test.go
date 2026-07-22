package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Mirrors the live dev setup: controlplane-managed default, one
// customer-managed destination per gateway cluster.
const gatewayDataRoutingConfigConfig = `
resource "truefoundry_gateway_data_routing_config" "routing" {
  name = "datarouting"

  default = {
    storage = {
      type = "controlplane-managed"
    }
  }

  destinations = [
    {
      name    = "devde1truefoundrydevelop"
      enabled = true

      when = [
        {
          field  = "metadata"
          key    = "tfy_gateway_cluster"
          values = ["devde1truefoundrydevelop"]
        },
      ]

      storage = {
        type                    = "customer-managed"
        storage_integration_fqn = "tenant:azure:blobstorage:blob-storage:devde1truefoundrydevelop"
      }
    },
  ]
}
`

func TestAccGatewayDataRoutingConfig_lifecycle(t *testing.T) {
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
				Config: providerConfig(host) + gatewayDataRoutingConfigConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("truefoundry_gateway_data_routing_config.routing", "id"),
					resource.TestCheckResourceAttr("truefoundry_gateway_data_routing_config.routing", "default.storage.type", "controlplane-managed"),
					resource.TestCheckResourceAttr("truefoundry_gateway_data_routing_config.routing", "destinations.#", "1"),
					resource.TestCheckResourceAttr("truefoundry_gateway_data_routing_config.routing", "destinations.0.storage.storage_integration_fqn", "tenant:azure:blobstorage:blob-storage:devde1truefoundrydevelop"),
					resource.TestCheckTypeSetElemNestedAttrs("truefoundry_gateway_data_routing_config.routing", "destinations.0.when.*", map[string]string{
						"field":    "metadata",
						"key":      "tfy_gateway_cluster",
						"values.#": "1",
					}),
					resource.TestCheckTypeSetElemAttr("truefoundry_gateway_data_routing_config.routing", "destinations.0.when.*.values.*", "devde1truefoundrydevelop"),
				),
			},
			// In-place update: pinned default region with retention, no
			// destinations, so everything goes to the default.
			{
				Config: providerConfig(host) + `
resource "truefoundry_gateway_data_routing_config" "routing" {
  name = "datarouting"

  default = {
    storage = {
      type   = "controlplane-managed"
      region = "us"
    }
    traces_retention_period_days = 30
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("truefoundry_gateway_data_routing_config.routing", "default.storage.region", "us"),
					resource.TestCheckResourceAttr("truefoundry_gateway_data_routing_config.routing", "default.traces_retention_period_days", "30"),
					resource.TestCheckNoResourceAttr("truefoundry_gateway_data_routing_config.routing", "destinations"),
				),
			},
		},
	})
}

func TestAccGatewayDataRoutingConfig_import(t *testing.T) {
	_, host := startMockControlPlane(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories,
		Steps: []resource.TestStep{
			{Config: providerConfig(host) + gatewayDataRoutingConfigConfig},
			{
				ResourceName:      "truefoundry_gateway_data_routing_config.routing",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "gateway-data-routing-config",
			},
		},
	})
}

func TestAccGatewayDataRoutingConfig_invalidConfigRejected(t *testing.T) {
	_, host := startMockControlPlane(t)

	cases := []struct {
		name   string
		config string
		want   *regexp.Regexp
	}{
		{
			name: "customer-managed without fqn",
			config: `
resource "truefoundry_gateway_data_routing_config" "bad" {
  name = "datarouting"
  default = {
    storage = { type = "customer-managed" }
  }
}
`,
			want: regexp.MustCompile("storage_integration_fqn` is required on customer-managed"),
		},
		{
			name: "region on customer-managed",
			config: `
resource "truefoundry_gateway_data_routing_config" "bad" {
  name = "datarouting"
  default = {
    storage = {
      type                    = "customer-managed"
      storage_integration_fqn = "tenant:azure:b:blob-storage:c"
      region                  = "us"
    }
  }
}
`,
			want: regexp.MustCompile("region` is not allowed on customer-managed"),
		},
		{
			name: "fqn on controlplane-managed",
			config: `
resource "truefoundry_gateway_data_routing_config" "bad" {
  name = "datarouting"
  default = {
    storage = {
      type                    = "controlplane-managed"
      storage_integration_fqn = "tenant:azure:b:blob-storage:c"
    }
  }
}
`,
			want: regexp.MustCompile("storage_integration_fqn` is not allowed on controlplane-managed"),
		},
		{
			name: "destination without conditions",
			config: `
resource "truefoundry_gateway_data_routing_config" "bad" {
  name = "datarouting"
  default = {
    storage = { type = "controlplane-managed" }
  }
  destinations = [
    {
      name    = "d1"
      enabled = true
      when    = []
      storage = { type = "controlplane-managed" }
    },
  ]
}
`,
			want: regexp.MustCompile(`at least 1 elements`),
		},
		{
			name: "duplicate destination names",
			config: `
resource "truefoundry_gateway_data_routing_config" "bad" {
  name = "datarouting"
  default = {
    storage = { type = "controlplane-managed" }
  }
  destinations = [
    {
      name    = "d1"
      enabled = true
      when    = [{ field = "created_by", values = ["user:x"] }]
      storage = { type = "controlplane-managed" }
    },
    {
      name    = "d1"
      enabled = true
      when    = [{ field = "created_by", values = ["user:y"] }]
      storage = { type = "controlplane-managed" }
    },
  ]
}
`,
			want: regexp.MustCompile(`Duplicate destination name`),
		},
		{
			name: "invalid region",
			config: `
resource "truefoundry_gateway_data_routing_config" "bad" {
  name = "datarouting"
  default = {
    storage = {
      type   = "controlplane-managed"
      region = "eu"
    }
  }
}
`,
			want: regexp.MustCompile(`value must be one of`),
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
