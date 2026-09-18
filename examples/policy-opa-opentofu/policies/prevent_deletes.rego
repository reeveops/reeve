package main

import rego.v1

deny contains "production deletes require a separately reviewed policy exception" if {
    input.env == "prod"
    input.counts.delete > 0
}
