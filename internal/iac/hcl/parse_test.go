package hcl

import (
	"strings"
	"testing"
)

// Fixtures follow the documented `terraform show -json` plan representation
// (format_version 1.x): resource_changes / resource_drift entries with
// change.actions, before/after, after_unknown, before_sensitive /
// after_sensitive.

const fixtureCreateUpdateDeleteReplace = `{
  "format_version": "1.2",
  "terraform_version": "1.9.8",
  "resource_changes": [
    {
      "address": "random_pet.name",
      "type": "random_pet",
      "name": "name",
      "change": {
        "actions": ["create"],
        "before": null,
        "after": {"length": 2, "separator": "-"},
        "after_unknown": {"id": true},
        "before_sensitive": false,
        "after_sensitive": {}
      }
    },
    {
      "address": "aws_instance.web",
      "type": "aws_instance",
      "name": "web",
      "change": {
        "actions": ["update"],
        "before": {"instance_type": "t3.small", "tags": {"env": "stg"}},
        "after": {"instance_type": "t3.large", "tags": {"env": "prod"}},
        "after_unknown": {},
        "before_sensitive": {"tags": {}},
        "after_sensitive": {"tags": {}}
      }
    },
    {
      "address": "null_resource.old",
      "type": "null_resource",
      "name": "old",
      "change": {
        "actions": ["delete"],
        "before": {"id": "8926806816"},
        "after": null,
        "after_unknown": {},
        "before_sensitive": {},
        "after_sensitive": false
      }
    },
    {
      "address": "aws_db_instance.db",
      "type": "aws_db_instance",
      "name": "db",
      "action_reason": "replace_because_cannot_update",
      "change": {
        "actions": ["delete", "create"],
        "before": {"engine": "postgres", "engine_version": "15"},
        "after": {"engine": "postgres", "engine_version": "16"},
        "after_unknown": {"id": true},
        "before_sensitive": {},
        "after_sensitive": {},
        "replace_paths": [["engine_version"]]
      }
    },
    {
      "address": "aws_s3_bucket.same",
      "type": "aws_s3_bucket",
      "name": "same",
      "change": {
        "actions": ["no-op"],
        "before": {"bucket": "logs"},
        "after": {"bucket": "logs"},
        "after_unknown": {},
        "before_sensitive": {},
        "after_sensitive": {}
      }
    }
  ]
}`

func TestParsePlanCounts(t *testing.T) {
	p, err := parsePlan([]byte(fixtureCreateUpdateDeleteReplace))
	if err != nil {
		t.Fatal(err)
	}
	c := countsFrom(p.ResourceChanges)
	if c.Add != 1 || c.Change != 1 || c.Delete != 1 || c.Replace != 1 {
		t.Fatalf("counts off: %+v", c)
	}
}

func TestOpOfReplaceBothOrders(t *testing.T) {
	if op := opOf([]string{"delete", "create"}); op != opReplace {
		t.Fatalf("delete,create should be replace, got %s", op)
	}
	if op := opOf([]string{"create", "delete"}); op != opReplace {
		t.Fatalf("create,delete should be replace, got %s", op)
	}
	if op := opOf([]string{"no-op"}); op != opNoop {
		t.Fatalf("no-op misclassified: %s", op)
	}
	if op := opOf([]string{"read"}); op != opRead {
		t.Fatalf("read misclassified: %s", op)
	}
}

func TestShortSummaryRendering(t *testing.T) {
	p, err := parsePlan([]byte(fixtureCreateUpdateDeleteReplace))
	if err != nil {
		t.Fatal(err)
	}
	short := shortSummary(p.ResourceChanges, 10)
	for _, want := range []string{
		"+ random_pet.name",
		"+     id: (known after apply)",
		"+     length: 2",
		"~ aws_instance.web",
		`~     instance_type: "t3.small" => "t3.large"`,
		`~     tags: {env: "stg"} => {env: "prod"}`,
		"- null_resource.old",
		`-     id: "8926806816"`,
		"± aws_db_instance.db",
		`~     engine_version: "15" => "16"  (forces replacement)`,
	} {
		if !strings.Contains(short, want) {
			t.Fatalf("summary missing %q:\n%s", want, short)
		}
	}
	// The no-op resource must not appear.
	if strings.Contains(short, "aws_s3_bucket.same") {
		t.Fatalf("no-op resource leaked into summary:\n%s", short)
	}
	// The unchanged replace attribute must not render.
	if strings.Contains(short, "engine:") {
		t.Fatalf("unchanged attribute leaked into summary:\n%s", short)
	}
}

const fixtureSensitive = `{
  "format_version": "1.2",
  "resource_changes": [
    {
      "address": "aws_db_instance.db",
      "type": "aws_db_instance",
      "name": "db",
      "change": {
        "actions": ["update"],
        "before": {"password": "hunter2", "username": "admin", "port": 5432},
        "after": {"password": "hunter3", "username": "admin", "port": 5433},
        "after_unknown": {},
        "before_sensitive": {"password": true},
        "after_sensitive": {"password": true}
      }
    },
    {
      "address": "vault_generic_secret.blob",
      "type": "vault_generic_secret",
      "name": "blob",
      "change": {
        "actions": ["create"],
        "before": null,
        "after": {"data_json": "{\"token\":\"squirrel\"}", "path": "kv/app"},
        "after_unknown": {},
        "before_sensitive": false,
        "after_sensitive": {"data_json": true}
      }
    }
  ]
}`

func TestSensitiveValuesMasked(t *testing.T) {
	p, err := parsePlan([]byte(fixtureSensitive))
	if err != nil {
		t.Fatal(err)
	}
	short := shortSummary(p.ResourceChanges, 10)
	for _, secret := range []string{"hunter2", "hunter3", "squirrel"} {
		if strings.Contains(short, secret) {
			t.Fatalf("sensitive value %q leaked into summary:\n%s", secret, short)
		}
	}
	for _, want := range []string{
		"~     password: [sensitive] => [sensitive]",
		"~     port: 5432 => 5433",
		"+     data_json: [sensitive]",
		`+     path: "kv/app"`,
	} {
		if !strings.Contains(short, want) {
			t.Fatalf("summary missing %q:\n%s", want, short)
		}
	}
	// Unchanged sensitive-adjacent attribute must not render.
	if strings.Contains(short, "username") {
		t.Fatalf("unchanged attribute leaked:\n%s", short)
	}
}

func TestScrubPlanJSONMasksSensitive(t *testing.T) {
	out, err := scrubPlanJSON([]byte(fixtureSensitive))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hunter2", "hunter3", "squirrel"} {
		if strings.Contains(out, secret) {
			t.Fatalf("sensitive value %q survived the scrub:\n%s", secret, out)
		}
	}
	// Non-sensitive values survive.
	if !strings.Contains(out, "kv/app") {
		t.Fatalf("non-sensitive value scrubbed away:\n%s", out)
	}
	if !strings.Contains(out, sensitivePlaceholder) {
		t.Fatalf("expected placeholder in scrubbed output:\n%s", out)
	}
}

func TestScrubPlanJSONStateValues(t *testing.T) {
	// prior_state/planned_values use (values, sensitive_values) pairs.
	raw := `{
	  "format_version": "1.2",
	  "prior_state": {
	    "values": {
	      "root_module": {
	        "resources": [
	          {"address": "aws_db_instance.db",
	           "values": {"password": "hunter2", "port": 5432},
	           "sensitive_values": {"password": true}}
	        ]
	      }
	    }
	  }
	}`
	out, err := scrubPlanJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hunter2") {
		t.Fatalf("state sensitive value survived the scrub:\n%s", out)
	}
	if !strings.Contains(out, "5432") {
		t.Fatalf("non-sensitive state value scrubbed away:\n%s", out)
	}
}

func TestScrubPlanJSONUnparseable(t *testing.T) {
	if _, err := scrubPlanJSON([]byte("not json")); err == nil {
		t.Fatal("scrub of unparseable input must error (callers never fall back to the raw blob)")
	}
}

const fixtureUnknown = `{
  "format_version": "1.2",
  "resource_changes": [
    {
      "address": "null_resource.trigger",
      "type": "null_resource",
      "name": "trigger",
      "change": {
        "actions": ["update"],
        "before": {"triggers": {"rev": "abc"}},
        "after": {"triggers": {"rev": "def"}},
        "after_unknown": {"id": true},
        "before_sensitive": {},
        "after_sensitive": {}
      }
    }
  ]
}`

func TestUnknownValuesRendered(t *testing.T) {
	p, err := parsePlan([]byte(fixtureUnknown))
	if err != nil {
		t.Fatal(err)
	}
	short := shortSummary(p.ResourceChanges, 10)
	for _, want := range []string{
		"+     id: (known after apply)",
		`~     triggers: {rev: "abc"} => {rev: "def"}`,
	} {
		if !strings.Contains(short, want) {
			t.Fatalf("summary missing %q:\n%s", want, short)
		}
	}
}

const fixtureDrift = `{
  "format_version": "1.2",
  "resource_changes": [
    {"address": "random_pet.name", "type": "random_pet", "name": "name",
     "change": {"actions": ["no-op"], "before": {}, "after": {},
                "after_unknown": {}, "before_sensitive": {}, "after_sensitive": {}}}
  ],
  "resource_drift": [
    {"address": "aws_s3_bucket.data", "type": "aws_s3_bucket", "name": "data",
     "change": {"actions": ["update"],
                "before": {"acl": "private"},
                "after": {"acl": "public-read"},
                "after_unknown": {}, "before_sensitive": {}, "after_sensitive": {}}},
    {"address": "aws_iam_role.gone", "type": "aws_iam_role", "name": "gone",
     "change": {"actions": ["delete"],
                "before": {"name": "app-role"},
                "after": null,
                "after_unknown": {}, "before_sensitive": {}, "after_sensitive": false}}
  ]
}`

func TestDriftFixture(t *testing.T) {
	p, err := parsePlan([]byte(fixtureDrift))
	if err != nil {
		t.Fatal(err)
	}
	c := countsFrom(p.ResourceDrift)
	if c.Change != 1 || c.Delete != 1 || c.Add != 0 || c.Replace != 0 {
		t.Fatalf("drift counts off: %+v", c)
	}
	addrs := changedAddresses(p.ResourceDrift)
	if len(addrs) != 2 || addrs[0] != "aws_s3_bucket.data" || addrs[1] != "aws_iam_role.gone" {
		t.Fatalf("drifted addresses off: %v", addrs)
	}
	// resource_changes no-ops must not contaminate the drift set.
	for _, a := range addrs {
		if a == "random_pet.name" {
			t.Fatal("no-op resource leaked into drifted addresses")
		}
	}
}

func TestParsePlanMalformed(t *testing.T) {
	if _, err := parsePlan([]byte("]{ not json")); err == nil {
		t.Fatal("malformed JSON must be an error (fail closed)")
	}
	// Valid JSON that is not a plan representation also fails.
	if _, err := parsePlan([]byte(`{"foo": "bar"}`)); err == nil {
		t.Fatal("JSON without format_version must be an error (fail closed)")
	}
}

func TestMaskValueStructureMismatchFailsClosed(t *testing.T) {
	// Marker says something below is sensitive but the value is a scalar:
	// mask the whole value rather than leak it.
	got := maskValue("top-secret", map[string]any{"inner": true})
	if got != sensitivePlaceholder {
		t.Fatalf("expected full mask on structure mismatch, got %v", got)
	}
}

func TestDriftResourcesAndChangedPaths(t *testing.T) {
	changes := []resourceChange{
		{
			Address: "aws_instance.web",
			Type:    "aws_instance",
			Change: changeDetail{
				Actions: []string{"update"},
				Before:  map[string]any{"tags": map[string]any{"LastScanned": "old", "Name": "web"}},
				After:   map[string]any{"tags": map[string]any{"LastScanned": "new", "Name": "web"}},
			},
		},
		{
			Address: "aws_s3_bucket.gone",
			Type:    "aws_s3_bucket",
			Change:  changeDetail{Actions: []string{"delete"}, Before: map[string]any{"id": "b"}, After: nil},
		},
		{
			Address: "aws_vpc.noop",
			Type:    "aws_vpc",
			Change:  changeDetail{Actions: []string{"no-op"}},
		},
	}
	got := driftResources(changes)
	if len(got) != 2 {
		t.Fatalf("no-op must be excluded; expected 2 resources, got %d", len(got))
	}
	if got[0].Op != "update" || len(got[0].Paths) != 1 || got[0].Paths[0] != "tags.LastScanned" {
		t.Fatalf("nested changed path should be tags.LastScanned, got %+v", got[0])
	}
	if got[1].Op != "delete" || got[1].Category != "orphaned" {
		t.Fatalf("a delete in resource_drift is orphaned-state drift, got %+v", got[1])
	}
}
