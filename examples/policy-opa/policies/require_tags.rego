package main

import rego.v1

# This recipe accepts Terraform/OpenTofu JSON, not Pulumi output.
deny contains "expected a Terraform/OpenTofu plan.resource_changes array" if {
    input.env == "prod"
    not valid_plan
}

deny contains sprintf("%s: required tag %s is missing or empty", [resource.address, tag]) if {
    input.env == "prod"
    some resource in input.plan.resource_changes
    resource.mode == "managed"
    resource.type == "aws_instance"
    resource.change.after != null
    some tag in {"cost-center", "owner"}
    tags := object.get(resource.change.after, "tags", null)
    not valid_tag(tags, tag)
}

valid_plan if {
    is_object(input.plan)
    is_array(input.plan.resource_changes)
}

valid_tag(tags, tag) if {
    is_object(tags)
    value := tags[tag]
    is_string(value)
    trim_space(value) != ""
}
