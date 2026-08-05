package slack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/notify"
	"github.com/FynxLabs/reeve/internal/slack"
)

// --- fakes ---------------------------------------------------------------

type call struct {
	method  string // post | update | thread
	channel string
	ts      string
	text    string
	blocks  string // marshalled blocks/attachments for content asserts
}

type fakeClient struct {
	calls   []call
	nextTS  int
	postErr error
}

func (f *fakeClient) Post(_ context.Context, m slack.Message) (*slack.PostResult, error) {
	if f.postErr != nil {
		return nil, f.postErr
	}
	f.nextTS++
	f.calls = append(f.calls, call{method: "post", channel: m.Channel, text: m.Text, blocks: renderMsg(m)})
	return &slack.PostResult{TS: fmt.Sprintf("ts-%d", f.nextTS), Channel: "C1"}, nil
}

func (f *fakeClient) Update(_ context.Context, m slack.Message) (*slack.PostResult, error) {
	f.calls = append(f.calls, call{method: "update", channel: m.Channel, ts: m.TS, text: m.Text, blocks: renderMsg(m)})
	return &slack.PostResult{TS: m.TS, Channel: m.Channel}, nil
}

func (f *fakeClient) Upsert(ctx context.Context, channel, ts, text string, blocks []slack.Block) (*slack.PostResult, error) {
	if ts == "" {
		return f.Post(ctx, slack.Message{Channel: channel, Text: text, Blocks: blocks})
	}
	return f.Update(ctx, slack.Message{Channel: channel, TS: ts, Text: text, Blocks: blocks})
}

func (f *fakeClient) PostThread(_ context.Context, channel, parentTS, text string, _ []slack.Block) (*slack.PostResult, error) {
	f.nextTS++
	f.calls = append(f.calls, call{method: "thread", channel: channel, ts: parentTS, text: text})
	return &slack.PostResult{TS: fmt.Sprintf("ts-%d", f.nextTS), Channel: channel}, nil
}

func renderMsg(m slack.Message) string {
	var b strings.Builder
	for _, blk := range m.Blocks {
		b.Write(blk)
	}
	for _, a := range m.Attachments {
		for _, blk := range a.Blocks {
			b.Write(blk)
		}
	}
	// json.Marshal HTML-escapes & < > to \u00XX inside strings; undo it so
	// content asserts read naturally.
	s := b.String()
	s = strings.ReplaceAll(s, `\u0026`, "&")
	s = strings.ReplaceAll(s, `\u003c`, "<")
	s = strings.ReplaceAll(s, `\u003e`, ">")
	return s
}

// memStore is an in-memory blob.Store with ETag compare-and-swap.
type memStore struct {
	mu     sync.Mutex
	data   map[string][]byte
	etags  map[string]int
	getErr error
}

func newMemStore() *memStore {
	return &memStore{data: map[string][]byte{}, etags: map[string]int{}}
}

func (m *memStore) Get(_ context.Context, key string) (io.ReadCloser, *blob.Metadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, nil, m.getErr
	}
	d, ok := m.data[key]
	if !ok {
		return nil, nil, blob.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(d)), &blob.Metadata{ETag: fmt.Sprintf("v%d", m.etags[key])}, nil
}

func (m *memStore) Put(_ context.Context, key string, r io.Reader) (*blob.Metadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, _ := io.ReadAll(r)
	m.data[key] = d
	m.etags[key]++
	return &blob.Metadata{ETag: fmt.Sprintf("v%d", m.etags[key])}, nil
}

func (m *memStore) PutIfMatch(_ context.Context, key string, r io.Reader, ifMatch string) (*blob.Metadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.data[key]
	if ifMatch == "" {
		if exists {
			return nil, blob.ErrPreconditionFailed
		}
	} else if !exists || fmt.Sprintf("v%d", m.etags[key]) != ifMatch {
		return nil, blob.ErrPreconditionFailed
	}
	d, _ := io.ReadAll(r)
	m.data[key] = d
	m.etags[key]++
	return &blob.Metadata{ETag: fmt.Sprintf("v%d", m.etags[key])}, nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *memStore) List(_ context.Context, _ string) ([]string, error) { return nil, nil }

func testChannel(fc *fakeClient, store blob.Store, trigger schemas.SlackTrigger) *Channel {
	return &Channel{
		name:    "slack",
		client:  fc,
		channel: "#infra",
		trigger: trigger,
		blob:    store,
	}
}

func prPayload(ev notify.Event) notify.Payload {
	return notify.Payload{Event: ev, PR: &notify.PRPayload{
		PR: 7, CommitSHA: "abc1234def", RunURL: "https://ci/run/1",
		Title: "add thing", Author: "dev", RepoFull: "org/repo",
	}}
}

// --- constructor ---------------------------------------------------------

func TestNewSkipsWithoutToken(t *testing.T) {
	s, err := New(context.Background(), schemas.ChannelYAML{Type: "slack"}, notify.Deps{})
	if err != nil || s != nil {
		t.Fatalf("want skip, got %v %v", s, err)
	}
	s, err = New(context.Background(), schemas.ChannelYAML{Type: "slack"}, notify.Deps{SlackToken: "xoxb-1"})
	if err != nil || s == nil {
		t.Fatalf("want channel with deps token, got %v %v", s, err)
	}
}

func TestDefaultPREventsMatchLegacyTriggerSemantics(t *testing.T) {
	// trigger apply (and empty): every lifecycle event fires - "apply" is
	// not itself an event, exactly like schemas.SlackEventEnabled.
	for _, trig := range []schemas.SlackTrigger{"", schemas.SlackTriggerApply, schemas.SlackTriggerPlan} {
		if got := defaultPREvents(trig); len(got) != len(notify.PREvents()) {
			t.Fatalf("trigger %q: want all events, got %v", trig, got)
		}
	}
	got := defaultPREvents(schemas.SlackTriggerReady)
	if len(got) != len(notify.PREvents())-1 || got[0] != notify.EventReady {
		t.Fatalf("trigger ready: %v", got)
	}
}

// --- drift rendering -----------------------------------------------------

func TestDriftDeliveryContent(t *testing.T) {
	fc := &fakeClient{}
	s := testChannel(fc, newMemStore(), "")
	err := s.Deliver(context.Background(), notify.Payload{
		Event: notify.EventDriftDetected,
		Drift: &notify.DriftPayload{
			Project: "net", Stack: "prod", Env: "prod",
			Add: 1, Change: 2, Delete: 3, Replace: 4,
			Error:       "boom <tag> & stuff",
			PlanSummary: "x\n```\ninjected\n```",
		},
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0].method != "post" {
		t.Fatalf("calls: %+v", fc.calls)
	}
	text := fc.calls[0].text
	if !strings.Contains(text, "*net/prod* - 🆕 drift detected (+1 ~2 -3 ±4)") {
		t.Fatalf("message body changed: %q", text)
	}
	if !strings.Contains(text, "&lt;tag&gt; &amp; stuff") {
		t.Fatalf("error not escaped: %q", text)
	}
	// The injected fence must not close the outer fence: exactly one
	// literal ``` pair (open + close) may remain.
	if strings.Count(text, "```") != 2 {
		t.Fatalf("code fence broken by payload: %q", text)
	}
}

func TestDriftGroupedDeliveryListsStacks(t *testing.T) {
	fc := &fakeClient{}
	s := testChannel(fc, newMemStore(), "")
	s.grouping = notify.GroupingByEnvironment
	err := s.Deliver(context.Background(), notify.Payload{
		Event:    notify.EventDriftDetected,
		GroupKey: "prod",
		Drift:    &notify.DriftPayload{Project: "net", Stack: "a", Env: "prod"},
		Group: []notify.DriftPayload{
			{Project: "net", Stack: "a", Env: "prod", Change: 1},
			{Project: "net", Stack: "c", Env: "prod", Add: 2},
		},
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("grouped delivery should be one message, got %+v", fc.calls)
	}
	text := fc.calls[0].text
	if !strings.Contains(text, "net/a") || !strings.Contains(text, "net/c") {
		t.Fatalf("grouped message must list both stacks, got %q", text)
	}
	if !strings.Contains(text, "prod") || !strings.Contains(text, "2 stack(s)") {
		t.Fatalf("grouped message must name env and count, got %q", text)
	}
}

func TestDriftDeliveryTruncatesLongError(t *testing.T) {
	fc := &fakeClient{}
	s := testChannel(fc, newMemStore(), "")
	long := strings.Repeat("é", 300) // multibyte: must not split a rune
	err := s.Deliver(context.Background(), notify.Payload{
		Event: notify.EventCheckFailed,
		Drift: &notify.DriftPayload{Project: "a", Stack: "b", Error: long},
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if strings.Contains(fc.calls[0].text, long) {
		t.Fatal("error was not truncated")
	}
	if strings.Contains(fc.calls[0].text, "�") {
		t.Fatal("truncation split a rune")
	}
}

// --- PR lifecycle --------------------------------------------------------

func TestPRPlanDoesNotCreateOnApplyTrigger(t *testing.T) {
	fc := &fakeClient{}
	s := testChannel(fc, newMemStore(), schemas.SlackTriggerApply)
	if err := s.Deliver(context.Background(), prPayload(notify.EventPlan)); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(fc.calls) != 0 {
		t.Fatalf("plan must not create on apply trigger: %+v", fc.calls)
	}
}

func TestPRPlanCreatesOnPlanTriggerAndPersistsState(t *testing.T) {
	fc := &fakeClient{}
	store := newMemStore()
	s := testChannel(fc, store, schemas.SlackTriggerPlan)
	if err := s.Deliver(context.Background(), prPayload(notify.EventPlan)); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(fc.calls) != 2 || fc.calls[0].method != "post" || fc.calls[1].method != "thread" {
		t.Fatalf("calls: %+v", fc.calls)
	}
	st, _, err := s.loadPRState(context.Background(), 7)
	if err != nil || st.MainTS == "" || st.ThreadTS == "" || st.Channel != "C1" {
		t.Fatalf("state: %+v err=%v", st, err)
	}

	// Second event updates in place instead of posting again.
	if err := s.Deliver(context.Background(), prPayload(notify.EventApplying)); err != nil {
		t.Fatalf("Deliver 2: %v", err)
	}
	if fc.calls[2].method != "update" || fc.calls[2].ts != st.MainTS {
		t.Fatalf("second delivery: %+v", fc.calls[2])
	}
}

func TestPRFailedNeverCreates(t *testing.T) {
	fc := &fakeClient{}
	s := testChannel(fc, newMemStore(), schemas.SlackTriggerPlan)
	if err := s.Deliver(context.Background(), prPayload(notify.EventFailed)); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(fc.calls) != 0 {
		t.Fatalf("failed must not create: %+v", fc.calls)
	}
}

func TestPRStateLoadErrorPreventsDuplicatePost(t *testing.T) {
	fc := &fakeClient{}
	store := newMemStore()
	store.getErr = errors.New("bucket unavailable")
	s := testChannel(fc, store, schemas.SlackTriggerPlan)
	err := s.Deliver(context.Background(), prPayload(notify.EventPlan))
	if err == nil || !strings.Contains(err.Error(), "bucket unavailable") {
		t.Fatalf("want propagated state error, got %v", err)
	}
	if len(fc.calls) != 0 {
		t.Fatalf("must not post with unknown state: %+v", fc.calls)
	}
}

func TestPRStateCASConflictKeepsFirstWriter(t *testing.T) {
	fc := &fakeClient{}
	store := newMemStore()
	s := testChannel(fc, store, schemas.SlackTriggerPlan)

	// Simulate a concurrent run winning the race after our load: state gets
	// created (with a different message TS) between load and save.
	_, err := store.Put(context.Background(), PRStateKey(7), strings.NewReader(`{"channel":"C1","main_ts":"other-ts"}`))
	if err != nil {
		t.Fatal(err)
	}
	st := &PRState{Channel: "C1", MainTS: "our-ts"}
	err = s.savePRState(context.Background(), 7, st, "") // we loaded before it existed
	if err == nil || !strings.Contains(err.Error(), "concurrent") {
		t.Fatalf("want conflict error, got %v", err)
	}
	// First writer's state survives.
	remote, _, _ := s.loadPRState(context.Background(), 7)
	if remote.MainTS != "other-ts" {
		t.Fatalf("state clobbered: %+v", remote)
	}
}

func TestPRStateCASRetriesOverSameMessage(t *testing.T) {
	store := newMemStore()
	s := testChannel(&fakeClient{}, store, "")
	// Remote state exists with the SAME main ts (concurrent thread update).
	_, _ = store.Put(context.Background(), PRStateKey(7), strings.NewReader(`{"channel":"C1","main_ts":"ts-1","thread_ts":"th-9"}`))
	st := &PRState{Channel: "C1", MainTS: "ts-1"}
	if err := s.savePRState(context.Background(), 7, st, "stale-etag"); err != nil {
		t.Fatalf("want retry to succeed, got %v", err)
	}
	remote, _, _ := s.loadPRState(context.Background(), 7)
	if remote.ThreadTS != "th-9" {
		t.Fatalf("thread ts not preserved: %+v", remote)
	}
}

func TestPROwnedThreadSuppressesCourtesyEntries(t *testing.T) {
	// When a timeline channel owns the thread (state.ThreadOwner), the dashboard
	// still updates the main message but must not double-post thread entries.
	fc := &fakeClient{}
	store := newMemStore()
	s := testChannel(fc, store, schemas.SlackTriggerPlan)
	_, err := store.Put(context.Background(), PRStateKey(7),
		strings.NewReader(`{"channel":"C1","main_ts":"ts-1","thread_owner":"timeline"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(context.Background(), prPayload(notify.EventApplying)); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(fc.calls) != 1 || fc.calls[0].method != "update" {
		t.Fatalf("want main update only, no thread post: %+v", fc.calls)
	}
	st, _, _ := s.loadPRState(context.Background(), 7)
	if st.ThreadOwner != "timeline" {
		t.Fatalf("owner must survive the save: %+v", st)
	}
}

func TestPRTitleEscapedInBlocks(t *testing.T) {
	fc := &fakeClient{}
	s := testChannel(fc, newMemStore(), schemas.SlackTriggerPlan)
	p := prPayload(notify.EventPlan)
	p.PR.Title = "<!channel> pwn & win"
	if err := s.Deliver(context.Background(), p); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	blocks := fc.calls[0].blocks
	if strings.Contains(blocks, "<!channel>") {
		t.Fatalf("title injection survived: %s", blocks)
	}
	// json.Marshal renders & as &, so the mrkdwn-escaped title reads
	// &lt;!channel&gt; ... in the raw block JSON.
	if !strings.Contains(blocks, `&lt;!channel&gt; pwn &amp; win`) {
		t.Fatalf("title not escaped: %s", blocks)
	}
}

func TestPRRulesFilterStacks(t *testing.T) {
	fc := &fakeClient{}
	s := testChannel(fc, newMemStore(), schemas.SlackTriggerPlan)
	s.rules = []schemas.SlackNotifyRule{{Environments: []string{"prod"}}}
	p := prPayload(notify.EventPlan)
	p.PR.Stacks = []notify.StackResult{
		{Project: "app", Stack: "prod", Env: "prod", Status: "planned", Add: 1},
		{Project: "app", Stack: "dev", Env: "dev", Status: "planned", Add: 1},
	}
	if err := s.Deliver(context.Background(), p); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	blocks := fc.calls[0].blocks
	if !strings.Contains(blocks, "`prod`") || strings.Contains(blocks, "`dev`") {
		t.Fatalf("rules not applied: %s", blocks)
	}
}
