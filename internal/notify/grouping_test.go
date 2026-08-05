package notify

import (
	"context"
	"sort"
	"sync"
	"testing"
)

func driftPayload(ev Event, project, stack, env string) Payload {
	return Payload{Event: ev, Drift: &DriftPayload{Project: project, Stack: stack, Env: env}}
}

// TestGroupPayloadsByEnvironment: 3 drifted stacks across 2 envs collapse into
// one payload per env, each listing its stacks; none/unset leaves them untouched.
func TestGroupPayloadsByEnvironment(t *testing.T) {
	in := []Payload{
		driftPayload(EventDriftDetected, "p", "a", "prod"),
		driftPayload(EventDriftDetected, "p", "b", "staging"),
		driftPayload(EventDriftDetected, "p", "c", "prod"),
	}

	// none / unset: unchanged (3 per-stack payloads).
	for _, mode := range []string{"", GroupingNone} {
		got := GroupPayloads(in, mode)
		if len(got) != 3 {
			t.Fatalf("mode %q: expected 3 payloads unchanged, got %d", mode, len(got))
		}
		for _, p := range got {
			if len(p.Group) != 0 {
				t.Fatalf("mode %q: ungrouped payload must have empty Group", mode)
			}
		}
	}

	// by_environment: 2 grouped payloads (prod: a,c; staging: b).
	got := GroupPayloads(in, GroupingByEnvironment)
	if len(got) != 2 {
		t.Fatalf("by_environment: expected 2 grouped payloads, got %d", len(got))
	}
	byEnv := map[string][]string{}
	for _, p := range got {
		if len(p.Group) == 0 {
			t.Fatalf("grouped payload must carry members")
		}
		for _, d := range p.Group {
			if d.Env != p.GroupKey {
				t.Fatalf("member env %q does not match group key %q", d.Env, p.GroupKey)
			}
			byEnv[p.GroupKey] = append(byEnv[p.GroupKey], d.Stack)
		}
	}
	for _, v := range byEnv {
		sort.Strings(v)
	}
	if got := byEnv["prod"]; len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("prod group should list [a c], got %v", got)
	}
	if got := byEnv["staging"]; len(got) != 1 || got[0] != "b" {
		t.Fatalf("staging group should list [b], got %v", got)
	}
}

// TestGroupPayloadsNeverGroupsIncidents: check_failed stays per-stack even
// under by_environment (one incident per stack).
func TestGroupPayloadsNeverGroupsIncidents(t *testing.T) {
	in := []Payload{
		driftPayload(EventCheckFailed, "p", "a", "prod"),
		driftPayload(EventCheckFailed, "p", "c", "prod"),
	}
	got := GroupPayloads(in, GroupingByEnvironment)
	if len(got) != 2 {
		t.Fatalf("incident events must not group, expected 2, got %d", len(got))
	}
	for _, p := range got {
		if len(p.Group) != 0 {
			t.Fatalf("incident event %s must stay per-stack", p.Event)
		}
	}
}

// TestDispatchGroupsPerChannel: a Grouper channel with by_environment receives
// 2 grouped deliveries; a plain channel receives all 3 per-stack. Grouping is
// per channel and does not change the total set of stacks delivered.
func TestDispatchGroupsPerChannel(t *testing.T) {
	payloads := []Payload{
		driftPayload(EventDriftDetected, "p", "a", "prod"),
		driftPayload(EventDriftDetected, "p", "b", "staging"),
		driftPayload(EventDriftDetected, "p", "c", "prod"),
	}

	var mu sync.Mutex
	groupedDeliveries := 0
	groupedStacks := 0
	grouped := &fakeChannel{name: "grouped", events: []Event{EventDriftDetected}, grouping: GroupingByEnvironment,
		fn: func(_ context.Context, p Payload) error {
			mu.Lock()
			groupedDeliveries++
			groupedStacks += len(p.Group)
			mu.Unlock()
			return nil
		}}

	plainDeliveries := 0
	plain := &fakeChannel{name: "plain", events: []Event{EventDriftDetected},
		fn: func(_ context.Context, p Payload) error {
			mu.Lock()
			plainDeliveries++
			mu.Unlock()
			return nil
		}}

	errs := Dispatch(context.Background(), []Channel{grouped, plain}, payloads)
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	if groupedDeliveries != 2 {
		t.Fatalf("grouped channel should get 2 deliveries, got %d", groupedDeliveries)
	}
	if groupedStacks != 3 {
		t.Fatalf("grouped deliveries should still cover all 3 stacks, got %d", groupedStacks)
	}
	if plainDeliveries != 3 {
		t.Fatalf("plain channel should get 3 per-stack deliveries, got %d", plainDeliveries)
	}
}
