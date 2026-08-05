package drift

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/FynxLabs/reeve/internal/blob/filesystem"
	"github.com/FynxLabs/reeve/internal/notify"
)

// flakyChannel fails deliveries while fail is true and records successes.
type flakyChannel struct {
	mu        sync.Mutex
	fail      bool
	delivered []notify.Payload
}

func (c *flakyChannel) Name() string               { return "flaky" }
func (c *flakyChannel) Subscribes() []notify.Event { return notify.DriftEvents() }
func (c *flakyChannel) setFail(f bool)             { c.mu.Lock(); c.fail = f; c.mu.Unlock() }
func (c *flakyChannel) got() []notify.Payload {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]notify.Payload(nil), c.delivered...)
}
func (c *flakyChannel) Deliver(_ context.Context, p notify.Payload) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("endpoint down")
	}
	c.delivered = append(c.delivered, p)
	return nil
}

func driftPayload(ev notify.Event) notify.Payload {
	return notify.Payload{Event: ev, Drift: &notify.DriftPayload{
		Project: "api", Stack: "prod", Env: "prod", Outcome: "drift_detected",
		Add: 1, RunID: "drift-1",
	}}
}

func newPendingStore(t *testing.T) *PendingStore {
	t.Helper()
	fs, err := filesystem.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &PendingStore{Blob: fs}
}

// TestDispatchFailureRedeliversNextRun is the durability regression test:
// the baseline advances before dispatch, so a failed dispatch must leave a
// marker that the next run redelivers - not silently drop the event.
func TestDispatchFailureRedeliversNextRun(t *testing.T) {
	ctx := context.Background()
	pending := newPendingStore(t)
	ch := &flakyChannel{fail: true}

	// Run 1: dispatch fails.
	errs := DispatchDurable(ctx, []notify.Channel{ch}, []notify.Payload{driftPayload(notify.EventDriftDetected)}, pending)
	if len(errs) == 0 {
		t.Fatal("failed dispatch must surface errors")
	}
	leftover, perrs := pending.List(ctx)
	if len(perrs) != 0 {
		t.Fatalf("pending list errors: %v", perrs)
	}
	if len(leftover) != 1 || leftover[0].Event != notify.EventDriftDetected || leftover[0].Drift.Ref() != "api/prod" {
		t.Fatalf("undelivered marker must persist, got %+v", leftover)
	}

	// Run 2: no new events this run; the channel recovered. The pending
	// payload redelivers and its marker clears.
	ch.setFail(false)
	merged := MergePending(leftover, nil)
	errs = DispatchDurable(ctx, []notify.Channel{ch}, merged, pending)
	if len(errs) != 0 {
		t.Fatalf("redelivery errs: %v", errs)
	}
	got := ch.got()
	if len(got) != 1 || got[0].Drift.Ref() != "api/prod" {
		t.Fatalf("payload not redelivered: %+v", got)
	}
	leftover, _ = pending.List(ctx)
	if len(leftover) != 0 {
		t.Fatalf("marker must clear after successful redelivery, got %+v", leftover)
	}
}

func TestDispatchSuccessLeavesNoMarker(t *testing.T) {
	ctx := context.Background()
	pending := newPendingStore(t)
	ch := &flakyChannel{}
	errs := DispatchDurable(ctx, []notify.Channel{ch}, []notify.Payload{driftPayload(notify.EventDriftDetected)}, pending)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	leftover, _ := pending.List(ctx)
	if len(leftover) != 0 {
		t.Fatalf("no marker expected after clean dispatch, got %+v", leftover)
	}
}

func TestMergePendingFreshPayloadSupersedes(t *testing.T) {
	stale := driftPayload(notify.EventDriftDetected)
	stale.Drift.Add = 99 // old counts
	fresh := driftPayload(notify.EventDriftDetected)
	merged := MergePending([]notify.Payload{stale}, []notify.Payload{fresh})
	if len(merged) != 1 || merged[0].Drift.Add != 1 {
		t.Fatalf("fresh payload for the same stack+event must supersede the pending one: %+v", merged)
	}

	// A pending payload for a DIFFERENT event redelivers ahead of current.
	otherEv := driftPayload(notify.EventCheckFailed)
	merged = MergePending([]notify.Payload{otherEv}, []notify.Payload{fresh})
	if len(merged) != 2 || merged[0].Event != notify.EventCheckFailed || merged[1].Event != notify.EventDriftDetected {
		t.Fatalf("pending first, then current: %+v", merged)
	}
}

// groupingChannel batches by_environment and records each Deliver call.
type groupingChannel struct {
	mu        sync.Mutex
	delivered []notify.Payload
}

func (c *groupingChannel) Name() string               { return "grouping" }
func (c *groupingChannel) Subscribes() []notify.Event { return notify.DriftEvents() }
func (c *groupingChannel) GroupingMode() string       { return notify.GroupingByEnvironment }
func (c *groupingChannel) Deliver(_ context.Context, p notify.Payload) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delivered = append(c.delivered, p)
	return nil
}

func stackPayload(project, stack, env string) notify.Payload {
	return notify.Payload{Event: notify.EventDriftDetected, Drift: &notify.DriftPayload{
		Project: project, Stack: stack, Env: env, Outcome: "drift_detected", Add: 1, RunID: "drift-1",
	}}
}

// TestDispatchDurableGroupsBatch is the C1 regression: a run's drifted stacks
// in one env must reach a grouping channel as ONE grouped delivery, not one
// per stack. Before the fix DispatchDurable dispatched payloads singly, so
// GroupPayloads never saw more than one and grouping silently no-opped.
func TestDispatchDurableGroupsBatch(t *testing.T) {
	ctx := context.Background()
	ch := &groupingChannel{}
	store := newPendingStore(t)
	payloads := []notify.Payload{
		stackPayload("api", "prod", "prod"),
		stackPayload("web", "prod", "prod"),
	}
	if errs := DispatchDurable(ctx, []notify.Channel{ch}, payloads, store); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.delivered) != 1 {
		t.Fatalf("want 1 grouped delivery, got %d: %+v", len(ch.delivered), ch.delivered)
	}
	if got := len(ch.delivered[0].Group); got != 2 {
		t.Fatalf("grouped delivery must carry both stacks, got Group of %d", got)
	}
	if ch.delivered[0].GroupKey != "prod" {
		t.Fatalf("group key should be the env: %q", ch.delivered[0].GroupKey)
	}
}
