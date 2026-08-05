package drift

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/core/discovery"
	"github.com/FynxLabs/reeve/internal/core/redact"
	"github.com/FynxLabs/reeve/internal/core/summary"
	"github.com/FynxLabs/reeve/internal/iac"
)

func TestClassifyDriftErrorTaxonomy(t *testing.T) {
	cases := []struct {
		msg  string
		want errKind
	}{
		// Transient network (retried).
		{"Post \"https://sts.amazonaws.com\": dial tcp: i/o timeout", errTransientNetwork},
		{"read tcp 10.0.0.1:443: connection reset by peer", errTransientNetwork},
		{"lookup s3.amazonaws.com: no such host", errTransientNetwork},
		{"ThrottlingException: Rate exceeded", errTransientNetwork},
		{"RequestError: send request failed", errTransientNetwork},
		{"503 Service Unavailable", errTransientNetwork},
		// Auth expiry (rebind + retry once).
		{"ExpiredToken: The security token included in the request is expired", errAuthExpired},
		{"error refreshing state: credentials have expired", errAuthExpired},
		{"oauth2: cannot fetch token: invalid_grant", errAuthExpired},
		// Permanent (never retried).
		{"panic: runtime error: invalid memory address", errPermanent},
		{"parse pulumi preview json: unexpected end of input", errPermanent},
		{"policy violation: prod-guardrails denied the plan", errPermanent},
		{"AccessDenied: user is not authorized to perform ec2:DescribeInstances", errPermanent},
		{"", errPermanent},
	}
	for _, c := range cases {
		if got := classifyDriftError(c.msg); got != c.want {
			t.Errorf("classifyDriftError(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

// scriptEngine returns a scripted sequence of results, one per DriftCheck
// call (the last entry repeats). It records how many times auth was resolved.
type scriptEngine struct {
	results []scriptResult
	calls   int
}

type scriptResult struct {
	res iac.PreviewResult
	err error
}

func (scriptEngine) Name() string { return "script" }
func (scriptEngine) EnumerateStacks(context.Context, string) ([]discovery.Stack, error) {
	return nil, nil
}
func (e *scriptEngine) DriftCheck(context.Context, discovery.Stack, iac.PreviewOpts, bool) (iac.PreviewResult, error) {
	i := e.calls
	e.calls++
	if i >= len(e.results) {
		i = len(e.results) - 1
	}
	return e.results[i].res, e.results[i].err
}

func runRetry(ctx context.Context, eng *scriptEngine, retries int, resolver AuthResolver) (Item, Event) {
	opts := Options{
		Engine:                eng,
		Redactor:              redact.New(),
		RetryOnTransientError: retries,
		AuthResolver:          resolver,
	}
	item, ev, _, _ := runOne(ctx, opts, discovery.Stack{Project: "p", Name: "s", Path: "p/s"}, time.Now())
	return item, ev
}

func TestRetryTransientThenSuccess(t *testing.T) {
	eng := &scriptEngine{results: []scriptResult{
		{err: errors.New("dial tcp 1.2.3.4:443: connect: connection refused")},
		{res: iac.PreviewResult{}}, // clean
	}}
	item, ev := runRetry(context.Background(), eng, 2, nil)
	if eng.calls != 2 {
		t.Fatalf("expected 2 check calls (1 retry), got %d", eng.calls)
	}
	if item.Outcome != OutcomeNoDrift || ev != EventNone {
		t.Fatalf("retried-then-clean must be silent no_drift, got outcome=%s ev=%s", item.Outcome, ev)
	}
}

func TestRetryNonTransientNotRetried(t *testing.T) {
	eng := &scriptEngine{results: []scriptResult{
		{res: iac.PreviewResult{Error: "terraform plan json missing format_version"}},
	}}
	item, ev := runRetry(context.Background(), eng, 3, nil)
	if eng.calls != 1 {
		t.Fatalf("non-transient error must not retry, got %d calls", eng.calls)
	}
	if item.Outcome != OutcomeError || ev != EventCheckFailed {
		t.Fatalf("non-transient failure must be an error, got outcome=%s ev=%s", item.Outcome, ev)
	}
}

func TestRetryExhaustedIsError(t *testing.T) {
	eng := &scriptEngine{results: []scriptResult{
		{err: errors.New("dial tcp: i/o timeout")},
	}}
	item, _ := runRetry(context.Background(), eng, 2, nil)
	if eng.calls != 3 {
		t.Fatalf("expected 3 calls (1 + 2 retries), got %d", eng.calls)
	}
	if item.Outcome != OutcomeError {
		t.Fatalf("exhausted retries must classify as error, got %s", item.Outcome)
	}
}

func TestRetryAuthExpiredRebindsOnce(t *testing.T) {
	authCalls := 0
	resolver := func(context.Context, string) (map[string]string, error) {
		authCalls++
		return map[string]string{"AWS_SESSION_TOKEN": "tok"}, nil
	}
	eng := &scriptEngine{results: []scriptResult{
		{res: iac.PreviewResult{Error: "ExpiredToken: the security token included in the request is expired"}},
		{res: iac.PreviewResult{Counts: summary.Counts{Change: 1}, DriftedURNs: []string{"urn::a"}}},
	}}
	item, ev := runRetry(context.Background(), eng, 2, resolver)
	if eng.calls != 2 {
		t.Fatalf("expected 2 check calls after rebind, got %d", eng.calls)
	}
	if authCalls != 2 {
		t.Fatalf("auth-expiry must rebind (re-resolve auth): want 2 resolves, got %d", authCalls)
	}
	if item.Outcome != OutcomeDriftDetected || ev != EventDriftDetected {
		t.Fatalf("rebind-then-drift must detect drift, got outcome=%s ev=%s", item.Outcome, ev)
	}
}

func TestRetryStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	eng := &scriptEngine{results: []scriptResult{
		{err: errors.New("dial tcp: connection refused")},
	}}
	item, _ := runRetry(ctx, eng, 5, nil)
	if eng.calls != 1 {
		t.Fatalf("cancelled context must stop retries, got %d calls", eng.calls)
	}
	if item.Outcome != OutcomeError {
		t.Fatalf("want error outcome, got %s", item.Outcome)
	}
}
