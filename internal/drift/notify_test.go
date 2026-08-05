package drift

import (
	"testing"

	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
	"github.com/FynxLabs/reeve/internal/notify"
)

func TestNotifyPayloadsMapsItemsAndSkipsSilent(t *testing.T) {
	out := &RunOutput{
		RunID: "drift-42",
		Items: []Item{
			{Project: "net", Stack: "prod", Env: "prod", Outcome: OutcomeDriftDetected,
				Counts: iac.PreviewResult{
					Counts:      summary.Counts{Add: 1, Change: 2, Delete: 3, Replace: 4},
					PlanSummary: "~ aws:ec2 sg",
				},
				Fingerprint: "fp1", Error: "", NotifyEvent: EventDriftDetected},
			{Project: "app", Stack: "dev", Env: "dev", Outcome: OutcomeNoDrift},
			{Project: "db", Stack: "prod", Env: "prod", Outcome: OutcomeError, Error: "boom", NotifyEvent: EventCheckFailed},
		},
		Events: []Event{EventDriftDetected, EventNone, EventCheckFailed},
	}

	got := NotifyPayloads(out)
	if len(got) != 2 {
		t.Fatalf("want 2 payloads (EventNone skipped), got %d", len(got))
	}
	p := got[0]
	if p.Event != notify.EventDriftDetected || p.PR != nil || p.Drift == nil {
		t.Fatalf("payload 0: %+v", p)
	}
	d := p.Drift
	if d.Ref() != "net/prod" || d.Outcome != "drift_detected" ||
		d.Add != 1 || d.Change != 2 || d.Delete != 3 || d.Replace != 4 ||
		d.PlanSummary != "~ aws:ec2 sg" || d.Fingerprint != "fp1" || d.RunID != "drift-42" {
		t.Fatalf("drift payload: %+v", d)
	}
	if got[1].Event != notify.EventCheckFailed || got[1].Drift.Error != "boom" {
		t.Fatalf("payload 1: %+v", got[1])
	}
}

func TestNotifyPayloadsNilRun(t *testing.T) {
	if got := NotifyPayloads(nil); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}
