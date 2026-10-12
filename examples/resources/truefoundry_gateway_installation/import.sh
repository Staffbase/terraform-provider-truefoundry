# Gateway installations are imported by their name. The token cannot be read
# back, so an imported installation has none; replace it to mint one.
terraform import truefoundry_gateway_installation.gateway_eu gateway-eu
