package main

import rego.v1

# Check the stack's default AWS region. Explicit providers and per-resource
# region overrides require additional rules; see README.md.

allowed_regions := {"us-east-1", "us-east-2", "us-west-2"}

deny contains "prod stack must declare an allowed aws:region in plan.config" if {
  input.env == "prod"
  not allowed_default_region
}

allowed_default_region if {
  input.plan.config["aws:region"] in allowed_regions
}
