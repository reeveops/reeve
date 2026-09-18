package main

import rego.v1

# Pulumi preview JSON is nested under Reeve's plan field.
deny contains "expected a Pulumi plan.steps array" if {
    input.env == "prod"
    not valid_plan
}

deny contains sprintf("%s: required tag %s is missing or empty", [step.urn, tag]) if {
    input.env == "prod"
    some step in input.plan.steps
    step.newState.type == "aws:ec2/instance:Instance"
    some tag in {"cost-center", "owner"}
    inputs := object.get(step.newState, "inputs", {})
    tags := object.get(inputs, "tags", null)
    not valid_tag(tags, tag)
}

valid_plan if {
    is_object(input.plan)
    is_array(input.plan.steps)
}

valid_tag(tags, tag) if {
    is_object(tags)
    value := tags[tag]
    is_string(value)
    trim_space(value) != ""
}
