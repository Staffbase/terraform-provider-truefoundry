# The rate limiting config is a tenant-wide singleton; its import ID is the
# fixed string "gateway-rate-limiting-config".
terraform import truefoundry_gateway_rate_limiting_config.limits gateway-rate-limiting-config
