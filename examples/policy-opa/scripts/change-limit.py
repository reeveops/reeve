#!/usr/bin/env python3
"""Advisory count limit over the Reeve plan wrapper; no cloud credentials."""
import json
import sys

with open(sys.argv[1], encoding="utf-8") as plan_file:
    counts = json.load(plan_file)["counts"]
limit = int(sys.argv[2])
total = sum(counts[key] for key in ("add", "change", "delete", "replace"))
if total > limit:
    print(f"planned change count {total} exceeds advisory limit {limit}")
    sys.exit(1)
print(f"planned change count {total} is within advisory limit {limit}")
