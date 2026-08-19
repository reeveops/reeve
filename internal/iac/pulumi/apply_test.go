package pulumi

import (
	"strings"
	"testing"
)

func TestParseApplyFromEventStream(t *testing.T) {
	stream := []byte(`
{"summaryEvent":{"resourceChanges":{"create":2,"update":1,"replace":1}}}
`)
	counts, errMsg := parseApply(stream)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if counts.Add != 2 || counts.Change != 1 || counts.Replace != 1 {
		t.Fatalf("counts off: %+v", counts)
	}
}

func TestParseApplyFallbackText(t *testing.T) {
	text := []byte(`Updating (prod)

Resources:
    + 2 created
    ~ 1 updated
    - 0 deleted
Duration: 14s
`)
	counts, _ := parseApply(text)
	if counts.Add != 2 || counts.Change != 1 {
		t.Fatalf("counts: %+v", counts)
	}
}

// In --json mode Pulumi writes engine events to stdout, so a failed apply
// often leaves only a generic wrapper line on stderr while the resource-level
// cause arrives as a diagnostic event. Those diagnostics were parsed and then
// discarded, which is what made a failed apply unactionable.
func TestParseApplyReturnsErrorDiagnostics(t *testing.T) {
	t.Parallel()
	stream := []byte(`{"diagnosticEvent":{"severity":"info","message":"starting update"}}
{"diagnosticEvent":{"severity":"error","message":"aws:s3:Bucket (data):\n  error: creating S3 bucket: AccessDenied: not authorized"}}
{"diagnosticEvent":{"severity":"error","message":"aws:iam:Role (task): error: entity already exists"}}
{"summaryEvent":{"resourceChanges":{"create":1}}}
`)
	counts, errMsg := parseApply(stream)
	if errMsg == "" {
		t.Fatal("error diagnostics must be returned, not discarded")
	}
	for _, want := range []string{"AccessDenied", "not authorized", "entity already exists"} {
		if !strings.Contains(errMsg, want) {
			t.Fatalf("diagnostics lost %q: %q", want, errMsg)
		}
	}
	// Every failing resource, each on its own line.
	if got := strings.Count(errMsg, "error:"); got != 2 {
		t.Fatalf("want 2 error diagnostics, got %d: %q", got, errMsg)
	}
	// Informational diagnostics are not errors.
	if strings.Contains(errMsg, "starting update") {
		t.Fatalf("info diagnostic leaked into the error: %q", errMsg)
	}
	// Counts still parse alongside the failure.
	if counts.Add != 1 {
		t.Fatalf("counts: %+v", counts)
	}
}

// A clean run reports no error even though diagnostics were present.
func TestParseApplyIgnoresNonErrorDiagnostics(t *testing.T) {
	t.Parallel()
	stream := []byte(`{"diagnosticEvent":{"severity":"warning","message":"deprecated property"}}
{"summaryEvent":{"resourceChanges":{"update":1}}}
`)
	counts, errMsg := parseApply(stream)
	if errMsg != "" {
		t.Fatalf("warning must not become an error: %q", errMsg)
	}
	if counts.Change != 1 {
		t.Fatalf("counts: %+v", counts)
	}
}
