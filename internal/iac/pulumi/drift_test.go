package pulumi

import (
	"errors"
	"testing"
)

func TestDriftedURNsFromJSONExcludesSame(t *testing.T) {
	// Two "same" (unchanged) steps and one "update" (drift). Fingerprint
	// input must contain only the drifted URN, so drift in a different
	// resource changes the fingerprint.
	js := []byte(`{"steps":[
		{"op":"same","urn":"urn:pulumi:prod::app::a::x"},
		{"op":"update","urn":"urn:pulumi:prod::app::b::y"},
		{"op":"same","urn":"urn:pulumi:prod::app::c::z"}
	]}`)
	got := driftedURNsFromJSON(js)
	if len(got) != 1 || got[0] != "urn:pulumi:prod::app::b::y" {
		t.Fatalf("expected only the updated URN, got %v", got)
	}
}

func TestDriftedURNsFromJSONBadInput(t *testing.T) {
	if got := driftedURNsFromJSON([]byte("not json")); got != nil {
		t.Fatalf("bad input should yield nil, got %v", got)
	}
}

func TestFailureMessageNeverEmpty(t *testing.T) {
	if got := failureMessage("", nil); got == "" {
		t.Fatal("failureMessage must never return empty (this is the false-resolve guard)")
	}
	if got := failureMessage("", errors.New("killed")); got != "killed" {
		t.Fatalf("expected process error fallback, got %q", got)
	}
	if got := failureMessage("boom", nil); got != "boom" {
		t.Fatalf("expected stderr, got %q", got)
	}
}

func TestDriftResourcesFromJSON(t *testing.T) {
	// An update with a detailedDiff (property paths) and a create (orphaned:
	// resource vanished from cloud, program wants to recreate it).
	js := []byte(`{"steps":[
		{"op":"same","urn":"urn:pulumi:prod::app::a::x"},
		{"op":"update","urn":"urn:pulumi:prod::app::aws:ec2/instance:Instance::web","type":"aws:ec2/instance:Instance","detailedDiff":{"tags.LastScanned":{"kind":"update"},"instanceType":{"kind":"update"}}},
		{"op":"create","urn":"urn:pulumi:prod::app::aws:s3/bucket:Bucket::data","type":"aws:s3/bucket:Bucket"}
	]}`)
	got := driftResourcesFromJSON(js)
	if len(got) != 2 {
		t.Fatalf("expected 2 changed resources (same excluded), got %d: %+v", len(got), got)
	}
	upd := got[0]
	if upd.Op != "update" || upd.Type != "aws:ec2/instance:Instance" || upd.Category != "changed" {
		t.Fatalf("update mapping wrong: %+v", upd)
	}
	if len(upd.Paths) != 2 {
		t.Fatalf("update should expose its 2 changed property paths, got %v", upd.Paths)
	}
	cre := got[1]
	if cre.Op != "create" || cre.Category != "orphaned" {
		t.Fatalf("a create in a drift preview is orphaned-state drift, got %+v", cre)
	}
}
