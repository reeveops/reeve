package main

import rego.v1

# This recipe assumes the default AWS provider uses var.aws_region.
# Provider aliases and resource overrides need their own rules; see README.md.
allowed_regions := {"us-east-1", "us-east-2", "us-west-2"}

deny contains "prod stack must declare an allowed aws_region in plan.variables" if {
    input.env == "prod"
    not allowed_default_region
}

allowed_default_region if {
    input.plan.variables.aws_region.value in allowed_regions
}
