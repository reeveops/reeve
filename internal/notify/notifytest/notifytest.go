// Package notifytest is the executable form of the notify.Channel contract.
//
// Every channel is run through the same suite, so the promises the dispatch
// layer relies on are enforced rather than assumed. The assertions are
// channel-agnostic; each channel package supplies a Subject that wires its
// own adapter to a local recorder, because the seams differ (an HTTP
// endpoint, an issue client, a comment client, a blob store) and some are
// unexported.
//
// What this deliberately does NOT do is mock the channel. The code under
// test is the real adapter: real config parsing, real subscription
// derivation, real payload rendering, real delivery call. Only the
// destination is local.
//
// Usage, from the channel's package:
//
//	func TestContract(t *testing.T) {
//	    notifytest.RunContract(t, notifytest.Subject{
//	        Type: "webhook",
//	        New:  newConformanceChannel,
//	    })
//	}
package notifytest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/notify"
)

// Sink records what a channel emitted, whatever seam it emitted through.
// A channel package fills whichever fields apply to it.
type Sink struct {
	// Bodies holds each JSON document the channel sent, decoded. HTTP
	// channels append here.
	Bodies []map[string]any
	// Texts holds each human-readable document the channel produced: an
	// issue body, a PR comment, a Slack message. Used for the rendering and
	// injection assertions.
	Texts []string
}

// Add records a raw JSON body, decoding it so assertions can inspect fields.
// A body that is not valid JSON is recorded as a Text instead, which the
// wire-format assertion then fails on.
func (s *Sink) Add(raw []byte) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		s.Texts = append(s.Texts, string(raw))
		return
	}
	s.Bodies = append(s.Bodies, m)
	s.Texts = append(s.Texts, string(raw))
}

// AddText records a rendered document that is not JSON.
func (s *Sink) AddText(v string) { s.Texts = append(s.Texts, v) }

// Len is the total number of deliveries recorded.
func (s *Sink) Len() int {
	if len(s.Bodies) > len(s.Texts) {
		return len(s.Bodies)
	}
	return len(s.Texts)
}

// Subject is one channel under test.
type Subject struct {
	// Type is the registry key (config `type:`).
	Type string
	// New builds the channel wired to a local recorder. Called once per
	// subtest so no state carries between assertions. cfg lets the suite
	// vary the configuration; the channel package should honor at least
	// Name, On and Grouping from it.
	New func(t *testing.T, cfg schemas.ChannelYAML) (notify.Channel, *Sink)
	// Drift is true when the channel renders drift payloads.
	Drift bool
	// PR is true when the channel renders PR-flow payloads.
	PR bool
	// JSON is true when deliveries are JSON documents, enabling the
	// wire-format and injection assertions over Sink.Bodies.
	JSON bool
}

// InjectionCanary is planted in operator- and contributor-controlled strings
// (a PR title, an author login). It carries Slack markup, an @-mention, a
// JSON-hostile quote and a newline: anything a channel renders must not let
// these break the document or forge a mention.
const InjectionCanary = `" <!channel> *bold* <https://evil.example|click> ` + "\n" + `end`

// RunContract runs every contract assertion against the subject.
func RunContract(t *testing.T, s Subject) {
	t.Helper()
	if s.New == nil {
		t.Fatal("notifytest.Subject.New is required")
	}
	t.Run("Registered", func(t *testing.T) { testRegistered(t, s) })
	t.Run("Identity", func(t *testing.T) { testIdentity(t, s) })
	t.Run("SubscriptionIsValid", func(t *testing.T) { testSubscription(t, s) })
	t.Run("EmptyPayloadIsSafe", func(t *testing.T) { testEmptyPayload(t, s) })
	t.Run("CancelledContextReturns", func(t *testing.T) { testCancelled(t, s) })
	t.Run("CheckFailedImpliesRecovered", func(t *testing.T) { testImpliedRecovery(t, s) })
	t.Run("GroupingModeIsValid", func(t *testing.T) { testGroupingMode(t, s) })
	if s.JSON {
		t.Run("RendersValidJSON", func(t *testing.T) { testValidJSON(t, s) })
	}
	if s.PR {
		t.Run("UntrustedTextIsNeutralized", func(t *testing.T) { testInjection(t, s) })
	}
}

// baseCfg is a config the channel should accept, with the suite's type and
// an explicit subscription.
func baseCfg(s Subject, on ...string) schemas.ChannelYAML {
	return schemas.ChannelYAML{Type: s.Type, On: on}
}

// samplePR is a benign PR payload.
func samplePR() *notify.PRPayload {
	return &notify.PRPayload{
		PR: 7, CommitSHA: "abcdef1234567890", RepoFull: "acme/infra",
		Title: "bump the thing", Author: "octocat", RunURL: "https://ci.example/1",
		Stacks: []notify.StackResult{{Project: "net", Stack: "prod", Env: "prod", Add: 1}},
	}
}

// sampleDrift is a benign drift payload.
func sampleDrift() *notify.DriftPayload {
	return &notify.DriftPayload{
		Project: "net", Stack: "prod", Env: "prod",
		Outcome: "drift_detected", Add: 1, RunID: "run-1", Fingerprint: "fp1",
	}
}

// anyEvent returns an event the channel subscribes to, preferring one that
// matches the payload kind the subject renders.
func anyEvent(t *testing.T, c notify.Channel) notify.Event {
	t.Helper()
	subs := c.Subscribes()
	if len(subs) == 0 {
		t.Skip("channel subscribes to nothing with this config")
	}
	return subs[0]
}

// --- contract assertions ------------------------------------------------

// The channel type resolves through the registry, and an unregistered type
// is an error that names the registered set. Build is how commands construct
// channels, so a type that is not registered is invisible at runtime.
func testRegistered(t *testing.T, s Subject) {
	found := false
	for _, typ := range notify.Registered() {
		if typ == s.Type {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("channel type %q is not registered (registered: %v)", s.Type, notify.Registered())
	}
	_, err := notify.Build(context.Background(),
		[]schemas.ChannelYAML{{Type: "definitely-not-a-channel"}}, notify.Deps{})
	if err == nil {
		t.Error("Build with an unregistered type must error")
	} else if !strings.Contains(err.Error(), s.Type) {
		t.Errorf("unknown-type error must name the registered set (missing %q): %v", s.Type, err)
	}
}

// Name() is non-empty and honors an explicit name, because it is what every
// dispatch error message identifies the channel by. Two channels of the same
// type with different names must be distinguishable in a log line.
func testIdentity(t *testing.T, s Subject) {
	c, _ := s.New(t, baseCfg(s))
	if c == nil {
		t.Skip("channel skipped itself with the base config")
	}
	if strings.TrimSpace(c.Name()) == "" {
		t.Error("Name() must be non-empty: dispatch errors identify the channel by it")
	}

	named := baseCfg(s)
	named.Name = "my-custom-name"
	if c2, _ := s.New(t, named); c2 != nil && c2.Name() != "my-custom-name" {
		t.Errorf("Name() = %q, want the configured name %q", c2.Name(), "my-custom-name")
	}
}

// Subscribes() is stable and returns only known event names. Dispatch filters
// on it, so an unknown name is a delivery that silently never happens.
func testSubscription(t *testing.T, s Subject) {
	c, _ := s.New(t, baseCfg(s))
	if c == nil {
		t.Skip("channel skipped itself with the base config")
	}
	first, second := c.Subscribes(), c.Subscribes()
	if len(first) != len(second) {
		t.Fatalf("Subscribes() is not stable: %d then %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("Subscribes() order changed at %d: %q then %q", i, first[i], second[i])
		}
		if !schemas.IsValidChannelEvent(string(first[i])) {
			t.Errorf("Subscribes() returned unknown event %q", first[i])
		}
	}
}

// A payload carrying neither a Drift nor a PR body must be a no-op, not a
// panic and not a delivery. Dispatch is shared by two producers, and a
// channel that assumes its own producer's payload shape crashes the run when
// the other one fires.
func testEmptyPayload(t *testing.T, s Subject) {
	c, sink := s.New(t, baseCfg(s))
	if c == nil {
		t.Skip("channel skipped itself with the base config")
	}
	ev := anyEvent(t, c)
	if err := c.Deliver(context.Background(), notify.Payload{Event: ev}); err != nil {
		t.Errorf("Deliver with an empty payload should be a no-op, got: %v", err)
	}
	if sink.Len() != 0 {
		t.Errorf("Deliver with an empty payload sent %d message(s); expected none", sink.Len())
	}
}

// Deliver honors context cancellation and returns promptly. Dispatch bounds
// each delivery with a timeout and abandons a channel that overruns it, so a
// channel ignoring ctx costs the whole batch.
func testCancelled(t *testing.T, s Subject) {
	c, _ := s.New(t, baseCfg(s))
	if c == nil {
		t.Skip("channel skipped itself with the base config")
	}
	ev := anyEvent(t, c)

	p := notify.Payload{Event: ev}
	if s.Drift {
		p.Drift = sampleDrift()
	} else if s.PR {
		p.PR = samplePR()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.Deliver(ctx, p) // an error is fine; hanging is not
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Deliver did not return on a cancelled context within 10s")
	}
}

// A channel subscribing to check_failed must also receive check_recovered.
// A stateful channel opens an incident or an issue on check_failed; without
// the all-clear that record never closes, so a healed check leaves a live
// alert forever. notify.WithImpliedEvents exists for this, and this asserts
// the channel actually applies it.
func testImpliedRecovery(t *testing.T, s Subject) {
	if !s.Drift {
		t.Skip("not a drift-rendering channel")
	}
	c, _ := s.New(t, baseCfg(s, schemas.ChannelEventCheckFailed))
	if c == nil {
		t.Skip("channel skipped itself with the base config")
	}
	var failed, recovered bool
	for _, e := range c.Subscribes() {
		switch string(e) {
		case schemas.ChannelEventCheckFailed:
			failed = true
		case schemas.ChannelEventCheckRecovered:
			recovered = true
		}
	}
	if !failed {
		t.Fatal("channel configured with on: [check_failed] does not subscribe to it")
	}
	if !recovered {
		t.Error("a channel subscribing to check_failed must also receive check_recovered, " +
			"or whatever it opened on failure never closes when the check heals")
	}
}

// GroupingMode, when implemented, returns a value config validation accepts.
// A channel advertising a mode the validator rejects can never be configured.
func testGroupingMode(t *testing.T, s Subject) {
	c, _ := s.New(t, baseCfg(s))
	if c == nil {
		t.Skip("channel skipped itself with the base config")
	}
	g, ok := c.(notify.Grouper)
	if !ok {
		return
	}
	if !notify.IsValidGroupingMode(g.GroupingMode()) {
		t.Errorf("GroupingMode() = %q, which config validation rejects (valid: %v)",
			g.GroupingMode(), notify.ValidGroupingModes)
	}
}

// Every delivery is a well-formed JSON document. Sink.Add records anything
// that fails to decode as text only, so a malformed body shows up here.
func testValidJSON(t *testing.T, s Subject) {
	c, sink := s.New(t, baseCfg(s))
	if c == nil {
		t.Skip("channel skipped itself with the base config")
	}
	ev := anyEvent(t, c)
	p := notify.Payload{Event: ev}
	if s.Drift {
		p.Drift = sampleDrift()
	} else {
		p.PR = samplePR()
	}
	if err := c.Deliver(context.Background(), p); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if sink.Len() == 0 {
		t.Skip("channel did not deliver for this event")
	}
	if len(sink.Bodies) != sink.Len() {
		t.Errorf("%d of %d deliveries were not valid JSON", sink.Len()-len(sink.Bodies), sink.Len())
	}
}

// A PR title and author are contributor-controlled: anyone who can open a PR
// picks them. They flow into rendered messages, so a channel must not let
// them break the document or forge a broadcast mention. The canary carries a
// quote, a newline, Slack markup and <!channel>.
func testInjection(t *testing.T, s Subject) {
	c, sink := s.New(t, baseCfg(s))
	if c == nil {
		t.Skip("channel skipped itself with the base config")
	}
	ev := anyEvent(t, c)
	pr := samplePR()
	pr.Title = InjectionCanary
	pr.Author = InjectionCanary

	if err := c.Deliver(context.Background(), notify.Payload{Event: ev, PR: pr}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if sink.Len() == 0 {
		t.Skip("channel did not deliver for this event")
	}
	if s.JSON && len(sink.Bodies) != sink.Len() {
		t.Error("an untrusted PR title broke the JSON document")
	}
	for _, doc := range sink.Texts {
		if strings.Contains(doc, "<!channel>") {
			t.Error("a broadcast mention from an untrusted PR title reached the message verbatim")
		}
	}
}
