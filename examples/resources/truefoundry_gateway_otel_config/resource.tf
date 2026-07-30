# The tenant-wide AI gateway OpenTelemetry config (singleton — define at most
# one): where the gateway ships its traces and metrics.
resource "truefoundry_gateway_otel_config" "otel" {
  otel_traces_exporter_config = {
    type     = "http"
    encoding = "proto" # required for http, invalid for grpc
    endpoint = "https://otel-collector.example.com/v1/traces"

    # Prefer a TrueFoundry secret reference over a raw token.
    headers = {
      authorization = "my-tenant:hashicorp:vault-secrets:secret-store:vault::otel/token"
    }

    additional_resource_attributes = {
      "service.namespace" = "ai-gateway"
      "deployment.env"    = "production"
    }

    # Keep prompts and completions out of the exported spans.
    exclude_request_data = true

    # Export only spans of one gateway cluster; omit `when` to export all.
    when = [
      {
        key   = "tfy_gateway_cluster"
        value = ["production-cluster"]
      },
    ]
  }

  # gRPC exporters take no encoding.
  otel_metrics_exporter_config = {
    type     = "grpc"
    endpoint = "https://otel-collector.example.com:4317"
  }
}
