package drift

import (
	"context"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/redact"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
)

// defaultClassification mirrors the cmd wiring: treat flags default true.
func defaultClassification(c *Classification) *Classification {
	c.TreatOrphanedAsDrift = true
	c.TreatMissingAsDrift = true
	return c
}

func TestFilterIgnorePropertiesNullifiesUpdate(t *testing.T) {
	c := defaultClassification(&Classification{
		IgnoreProperties: []IgnoreProperty{{
			ResourceType: "aws:ec2/instance:Instance",
			Properties:   []string{"tags.LastScanned", "tags.AutoManaged"},
		}},
	})
	res := []iac.ResourceChange{{
		Address:  "urn::web",
		Type:     "aws:ec2/instance:Instance",
		Op:       "update",
		Paths:    []string{"tags.LastScanned", "tags.AutoManaged"},
		Category: iac.DriftChanged,
	}}
	kept, removed := c.filter(res)
	if !removed || len(kept) != 0 {
		t.Fatalf("an update whose only changes are ignored props must drop out: kept=%d removed=%v", len(kept), removed)
	}
}

func TestFilterIgnorePropertiesKeepsRemainingChange(t *testing.T) {
	c := defaultClassification(&Classification{
		IgnoreProperties: []IgnoreProperty{{
			ResourceType: "aws:ec2/instance:Instance",
			Properties:   []string{"tags.*"},
		}},
	})
	res := []iac.ResourceChange{{
		Address:  "urn::web",
		Type:     "aws:ec2/instance:Instance",
		Op:       "update",
		Paths:    []string{"tags.LastScanned", "instanceType"},
		Category: iac.DriftChanged,
	}}
	kept, removed := c.filter(res)
	if len(kept) != 1 {
		t.Fatalf("a real change beyond ignored props must survive: kept=%d", len(kept))
	}
	if !removed {
		t.Fatalf("removed should be true (a path was trimmed)")
	}
	if len(kept[0].Paths) != 1 || kept[0].Paths[0] != "instanceType" {
		t.Fatalf("only the non-ignored path should remain, got %v", kept[0].Paths)
	}
}

func TestFilterIgnorePropertiesDoesNotDropCreate(t *testing.T) {
	c := defaultClassification(&Classification{
		IgnoreProperties: []IgnoreProperty{{ResourceType: "*", Properties: []string{"*"}}},
	})
	res := []iac.ResourceChange{{Address: "urn::new", Type: "aws:s3/bucket:Bucket", Op: "create", Category: iac.DriftOrphaned}}
	kept, _ := c.filter(res)
	if len(kept) != 1 {
		t.Fatalf("ignore_properties must not erase a create/delete resource, got kept=%d", len(kept))
	}
}

func TestFilterIgnoreResources(t *testing.T) {
	c := defaultClassification(&Classification{
		IgnoreResources: []string{"urn:*:autoscaling/group:*::*autoscaler-managed*"},
	})
	res := []iac.ResourceChange{
		{Address: "urn:pulumi:prod::app::aws:autoscaling/group:Group::autoscaler-managed-asg", Op: "update", Paths: []string{"desired"}},
		{Address: "urn:pulumi:prod::app::aws:s3/bucket:Bucket::data", Op: "update", Paths: []string{"policy"}},
	}
	kept, removed := c.filter(res)
	if !removed || len(kept) != 1 || kept[0].Address != "urn:pulumi:prod::app::aws:s3/bucket:Bucket::data" {
		t.Fatalf("ignore_resources should exclude only the matching URN, got %+v", kept)
	}
}

func TestFilterTreatOrphanedFalse(t *testing.T) {
	c := &Classification{TreatOrphanedAsDrift: false, TreatMissingAsDrift: true}
	res := []iac.ResourceChange{
		{Address: "a", Op: "create", Category: iac.DriftOrphaned},
		{Address: "b", Op: "update", Category: iac.DriftChanged, Paths: []string{"x"}},
	}
	kept, removed := c.filter(res)
	if !removed || len(kept) != 1 || kept[0].Address != "b" {
		t.Fatalf("orphaned_state:false must drop orphaned resources, got %+v", kept)
	}
}

func TestFilterEmptyIsNoOp(t *testing.T) {
	c := defaultClassification(&Classification{}) // no ignores, treat flags true
	if !c.empty() {
		t.Fatal("a classification with no rules and default treat flags must be empty()")
	}
	res := []iac.ResourceChange{{Address: "a", Op: "update", Paths: []string{"x"}}}
	kept, removed := c.filter(res)
	if removed || len(kept) != 1 {
		t.Fatalf("empty classification must not change anything")
	}
}

// runOne-level: filtering all drift away resolves the stack to no_drift.
func TestRunOneClassificationResolvesNoise(t *testing.T) {
	res := iac.PreviewResult{
		Counts:      summary.Counts{Change: 1},
		DriftedURNs: []string{"urn::web"},
		Resources: []iac.ResourceChange{{
			Address:  "urn::web",
			Type:     "aws:ec2/instance:Instance",
			Op:       "update",
			Paths:    []string{"tags.LastScanned"},
			Category: iac.DriftChanged,
		}},
	}
	opts := Options{
		Engine:   fakeEngine{res: res},
		Redactor: redact.New(),
		Classification: defaultClassification(&Classification{
			IgnoreProperties: []IgnoreProperty{{ResourceType: "aws:*", Properties: []string{"tags.*"}}},
		}),
	}
	item, ev, _, _ := runOne(context.Background(), opts, discovery.Stack{Project: "p", Name: "s", Path: "p/s"}, time.Now())
	if item.Outcome != OutcomeNoDrift || ev != EventNone {
		t.Fatalf("all-noise drift must filter to no_drift, got outcome=%s ev=%s", item.Outcome, ev)
	}
	if item.Counts.Counts.Total() != 0 {
		t.Fatalf("counts must be recomputed to zero after filtering, got %+v", item.Counts.Counts)
	}
}
