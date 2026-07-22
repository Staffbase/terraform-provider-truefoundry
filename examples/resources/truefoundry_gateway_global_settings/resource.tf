# The tenant-wide AI gateway global settings (singleton — define at most one).
# An apply replaces the whole settings manifest; omitted settings are reset to
# platform defaults.
resource "truefoundry_gateway_global_settings" "settings" {
  # Retry failed requests up to 3 times, 1 second apart.
  retry_settings = {
    attempts        = 3
    delay           = 1000
    on_status_codes = ["429", "503"]
  }

  # Log request/response bodies unless a request header opts out.
  logging_settings = {
    type                = "header_controlled"
    log_body_by_default = true
  }

  # Embed prompts for the semantic cache with this model.
  semantic_cache_settings = {
    semantic_cache_embedding_model = "generic/text-embedding-3-small"
  }

  # Evaluate guardrails in parallel with a 5 second timeout.
  guardrail_settings = {
    timeout_ms                = 5000
    enable_parallel_execution = true
  }

  # Serve no models until they are explicitly allowlisted; omit the block
  # to allow all models.
  model_configuration = {
    allowed_models = []
  }
}
