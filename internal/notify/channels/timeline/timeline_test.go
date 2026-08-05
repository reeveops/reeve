package timeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/notify"
	slackchannel "github.com/FynxLabs/reeve/internal/notify/channels/slack"
	slackapi "github.com/FynxLabs/reeve/internal/slack"
)

// --- fakes ---------------------------------------------------------------

// memStore is an in-memory blob.Store with ETag compare-and-swap.
type memStore struct {
	mu    sync.Mutex
	data  map[string][]byte
	etags map[string]int
	// putIfMatchHook runs (unlocked) before each PutIfMatch attempt; tests
	// use it to interleave a concurrent writer.
	putIfMatchHook func()
}

func newMemStore() *memStore {
	return &memStore{data: map[string][]byte{}, etags: map[string]int{}}
}

func (m *memStore) Get(_ context.Context, key string) (io.ReadCloser, *blob.Metadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	if m.putIfMatchHook != nil {
		hook := m.putIfMatchHook
		m.putIfMatchHook = nil
		hook()
	}
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

type upsert struct {
	pr           int
	body, marker string
}

type fakeComments struct {
	upserts []upsert
}

func (f *fakeComments) UpsertComment(_ context.Context, pr int, body, marker string) error {
	f.upserts = append(f.upserts, upsert{pr: pr, body: body, marker: marker})
	return nil
}

type slackCall struct {
	method   string // post | thread
	channel  string
	parentTS string
	text     string
}

type fakeSlack struct {
	calls  []slackCall
	nextTS int
	// postHook runs before each Post; tests use it to interleave a
	// concurrent state writer between load and save.
	postHook func()
}

func (f *fakeSlack) Post(_ context.Context, m slackapi.Message) (*slackapi.PostResult, error) {
	if f.postHook != nil {
		hook := f.postHook
		f.postHook = nil
		hook()
	}
	f.nextTS++
	f.calls = append(f.calls, slackCall{method: "post", channel: m.Channel, text: m.Text})
	return &slackapi.PostResult{TS: fmt.Sprintf("ts-%d", f.nextTS), Channel: "C1"}, nil
}

func (f *fakeSlack) Update(_ context.Context, m slackapi.Message) (*slackapi.PostResult, error) {
	return &slackapi.PostResult{TS: m.TS, Channel: m.Channel}, nil
}

func (f *fakeSlack) Upsert(ctx context.Context, channel, ts, text string, blocks []slackapi.Block) (*slackapi.PostResult, error) {
	if ts == "" {
		return f.Post(ctx, slackapi.Message{Channel: channel, Text: text})
	}
	return f.Update(ctx, slackapi.Message{Channel: channel, TS: ts, Text: text})
}

func (f *fakeSlack) PostThread(_ context.Context, channel, parentTS, text string, _ []slackapi.Block) (*slackapi.PostResult, error) {
	f.nextTS++
	f.calls = append(f.calls, slackCall{method: "thread", channel: channel, parentTS: parentTS, text: text})
	return &slackapi.PostResult{TS: fmt.Sprintf("ts-%d", f.nextTS), Channel: channel}, nil
}

func fixedNow() time.Time { return time.Date(2026, 7, 19, 12, 3, 5, 0, time.UTC) }

func payload(ev notify.Event, sha string) notify.Payload {
	return notify.Payload{Event: ev, PR: &notify.PRPayload{
		PR: 7, CommitSHA: sha, RunURL: "https://ci/run/" + string(ev),
		Title: "add thing", Author: "dev", RepoFull: "org/repo",
	}}
}

func testGitHubChannel(fc *fakeComments, store blob.Store) *GitHubChannel {
	return &GitHubChannel{name: "timeline_github", comments: fc, blob: store, events: notify.TimelinePREvents(), now: fixedNow}
}

func testSlackChannel(fs *fakeSlack, store blob.Store) *SlackChannel {
	return &SlackChannel{name: "timeline_slack", client: fs, channel: "#infra",
		events: notify.TimelinePREvents(), state: slackchannel.StateStore{Blob: store}, now: fixedNow}
}

// --- entry rendering -----------------------------------------------------

func TestEntryMarkdownLine(t *testing.T) {
	p := payload(notify.EventPlan, "abc1234def5678")
	p.PR.Stacks = []notify.StackResult{
		{Project: "app", Stack: "prod", Env: "prod", Status: "planned", Add: 1, Change: 2},
		{Project: "app", Stack: "dev", Env: "dev", Status: "noop"},
	}
	e := newEntry(p, fixedNow())
	got := e.markdownLine()
	want := "- 📋 **preview finished**: app/prod +1 ~2 -0 ±0, 1 no-op · 2026-07-19 12:03:05 UTC · [run](https://ci/run/plan)"
	if got != want {
		t.Fatalf("markdown line:\n got %q\nwant %q", got, want)
	}
}

func TestEntrySlackTextCarriesSHAAndEscapesDetail(t *testing.T) {
	p := payload(notify.EventFailed, "abc1234def5678")
	p.PR.Stacks = []notify.StackResult{{Project: "a<b", Stack: "prod&x", Status: "error"}}
	e := newEntry(p, fixedNow())
	got := e.slackText()
	if !strings.Contains(got, ":red_circle: *apply failed*") {
		t.Fatalf("label: %q", got)
	}
	if !strings.Contains(got, "`abc1234`") {
		t.Fatalf("short sha missing: %q", got)
	}
	if !strings.Contains(got, "<https://ci/run/failed|run>") {
		t.Fatalf("run link missing: %q", got)
	}
	if strings.Contains(got, "a<b") || !strings.Contains(got, "a&lt;b/prod&amp;x") {
		t.Fatalf("detail not escaped: %q", got)
	}
}

func TestEntryUnknownEventFallsBack(t *testing.T) {
	e := Entry{Event: "someday_event", At: "2026-07-19T12:03:05Z"}
	if !strings.Contains(e.markdownLine(), "**someday_event**") {
		t.Fatalf("unknown event dropped: %q", e.markdownLine())
	}
}

func TestDetailForBlockedListsBlockedRefs(t *testing.T) {
	stacks := []notify.StackResult{
		{Project: "app", Stack: "prod", Status: "blocked"},
		{Project: "app", Stack: "dev", Status: "planned", Add: 1},
	}
	if got := detailFor(notify.EventBlocked, stacks); got != "app/prod" {
		t.Fatalf("blocked detail: %q", got)
	}
}

// --- github channel ---------------------------------------------------------

func TestGitHubGroupsCommentsBySHA(t *testing.T) {
	fc := &fakeComments{}
	s := testGitHubChannel(fc, newMemStore())
	ctx := context.Background()

	must := func(p notify.Payload) {
		t.Helper()
		if err := s.Deliver(ctx, p); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
	}
	must(payload(notify.EventPlanning, "aaaa111bbbb"))
	must(payload(notify.EventPlan, "aaaa111bbbb"))
	must(payload(notify.EventPlanning, "cccc222dddd")) // new commit pushed

	if len(fc.upserts) != 3 {
		t.Fatalf("upserts: %d", len(fc.upserts))
	}
	// Same SHA → same marker, comment accumulates entries.
	if fc.upserts[0].marker != CommentMarker("aaaa111bbbb") || fc.upserts[1].marker != fc.upserts[0].marker {
		t.Fatalf("markers: %+v", fc.upserts)
	}
	second := fc.upserts[1].body
	if !strings.Contains(second, "**preview started**") || !strings.Contains(second, "**preview finished**") {
		t.Fatalf("second comment must hold both entries:\n%s", second)
	}
	if !strings.Contains(second, "commit `aaaa111`") {
		t.Fatalf("sha header: %s", second)
	}
	// New SHA → new marker, fresh comment with only its own entry.
	third := fc.upserts[2]
	if third.marker != CommentMarker("cccc222dddd") || strings.Contains(third.body, "preview finished") {
		t.Fatalf("sha grouping leaked: %+v", third)
	}
	// Each entry links its own run.
	if !strings.Contains(second, "https://ci/run/planning") || !strings.Contains(second, "https://ci/run/plan") {
		t.Fatalf("per-run URLs missing:\n%s", second)
	}
}

func TestGitHubCASConflictKeepsBothWriters(t *testing.T) {
	store := newMemStore()
	fc := &fakeComments{}
	s := testGitHubChannel(fc, store)
	ctx := context.Background()

	// A concurrent run writes its entry between our load and save.
	store.putIfMatchHook = func() {
		other := testGitHubChannel(&fakeComments{}, store)
		if err := other.Deliver(ctx, payload(notify.EventApproved, "aaaa111bbbb")); err != nil {
			t.Errorf("concurrent Deliver: %v", err)
		}
	}
	if err := s.Deliver(ctx, payload(notify.EventApplying, "aaaa111bbbb")); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	st, _, err := s.loadState(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	entries := st.Entries["aaaa111"]
	if len(entries) != 2 || entries[0].Event != "approved" || entries[1].Event != "applying" {
		t.Fatalf("conflict lost a writer: %+v", entries)
	}
	// The re-rendered comment carries both entries.
	final := fc.upserts[len(fc.upserts)-1].body
	if !strings.Contains(final, "**approved**") || !strings.Contains(final, "**apply started**") {
		t.Fatalf("final comment: %s", final)
	}
}

func TestGitHubIgnoresDriftAndLocalPayloads(t *testing.T) {
	fc := &fakeComments{}
	s := testGitHubChannel(fc, newMemStore())
	if err := s.Deliver(context.Background(), notify.Payload{
		Event: notify.EventDriftDetected, Drift: &notify.DriftPayload{Project: "a", Stack: "b"},
	}); err != nil {
		t.Fatal(err)
	}
	p := payload(notify.EventPlan, "abc")
	p.PR.PR = 0 // local run
	if err := s.Deliver(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(fc.upserts) != 0 {
		t.Fatalf("non-PR payloads must not comment: %+v", fc.upserts)
	}
}

func TestGitHubMarkerNamespace(t *testing.T) {
	m := CommentMarker("abc1234def")
	if m != "<!-- reeve:timeline:v1:abc1234 -->" {
		t.Fatalf("marker changed: %q (existing timeline comments would be orphaned)", m)
	}
	// Must never collide with the dashboard/apply markers.
	for _, other := range []string{
		"<!-- reeve:pr-comment:v1 -->",
		"<!-- reeve:apply:v1 -->",
		"<!-- reeve:help -->",
		"<!-- reeve:apply-timeline:apply-1-abc1234 -->",
	} {
		if strings.Contains(other, m) || strings.Contains(m, other) {
			t.Fatalf("marker collision: %q vs %q", m, other)
		}
	}
}

// --- slack channel ----------------------------------------------------------

func TestSlackCreatesAnchorOnceThenThreads(t *testing.T) {
	fs := &fakeSlack{}
	store := newMemStore()
	s := testSlackChannel(fs, store)
	ctx := context.Background()

	if err := s.Deliver(ctx, payload(notify.EventPlanning, "abc1234def")); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(fs.calls) != 2 || fs.calls[0].method != "post" || fs.calls[1].method != "thread" {
		t.Fatalf("calls: %+v", fs.calls)
	}
	if !strings.Contains(fs.calls[0].text, "Deployment timeline") ||
		!strings.Contains(fs.calls[0].text, "https://github.com/org/repo/pull/7") {
		t.Fatalf("anchor text: %q", fs.calls[0].text)
	}
	if fs.calls[1].parentTS != "ts-1" {
		t.Fatalf("thread must hang off the anchor: %+v", fs.calls[1])
	}
	if !strings.Contains(fs.calls[1].text, ":mag: *preview started*") {
		t.Fatalf("entry text: %q", fs.calls[1].text)
	}

	// State: anchor recorded + thread claimed.
	st, _, err := slackchannel.StateStore{Blob: store}.Load(ctx, 7)
	if err != nil || st.MainTS != "ts-1" || st.ThreadOwner != "timeline" {
		t.Fatalf("state: %+v err=%v", st, err)
	}

	// Second event: no new anchor, just a thread reply.
	if err := s.Deliver(ctx, payload(notify.EventPlan, "abc1234def")); err != nil {
		t.Fatalf("Deliver 2: %v", err)
	}
	if len(fs.calls) != 3 || fs.calls[2].method != "thread" || fs.calls[2].parentTS != "ts-1" {
		t.Fatalf("second delivery: %+v", fs.calls)
	}
}

func TestSlackReusesDashboardAnchorAndClaimsThread(t *testing.T) {
	fs := &fakeSlack{}
	store := newMemStore()
	ctx := context.Background()
	// The dashboard slack channel already created the per-PR status message.
	seed := &slackchannel.PRState{Channel: "C9", MainTS: "dash-ts"}
	data, _ := json.Marshal(seed)
	if _, err := store.Put(ctx, slackchannel.PRStateKey(7), bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}

	s := testSlackChannel(fs, store)
	if err := s.Deliver(ctx, payload(notify.EventApplying, "abc1234def")); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if len(fs.calls) != 1 || fs.calls[0].method != "thread" || fs.calls[0].parentTS != "dash-ts" || fs.calls[0].channel != "C9" {
		t.Fatalf("must thread under the dashboard message: %+v", fs.calls)
	}
	st, _, _ := slackchannel.StateStore{Blob: store}.Load(ctx, 7)
	if st.ThreadOwner != "timeline" || st.MainTS != "dash-ts" {
		t.Fatalf("thread not claimed: %+v", st)
	}
}

func TestSlackAnchorRaceThreadsUnderFirstWriter(t *testing.T) {
	fs := &fakeSlack{}
	store := newMemStore()
	ctx := context.Background()
	// A concurrent dashboard delivery records its message between our state
	// load and save (triggered from the anchor Post).
	fs.postHook = func() {
		st := &slackchannel.PRState{Channel: "C1", MainTS: "winner-ts"}
		data, _ := json.Marshal(st)
		if _, err := store.Put(ctx, slackchannel.PRStateKey(7), bytes.NewReader(data)); err != nil {
			t.Errorf("seed: %v", err)
		}
	}
	s := testSlackChannel(fs, store)
	if err := s.Deliver(ctx, payload(notify.EventPlanning, "abc1234def")); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	last := fs.calls[len(fs.calls)-1]
	if last.method != "thread" || last.parentTS != "winner-ts" {
		t.Fatalf("must thread under first writer's anchor: %+v", fs.calls)
	}
	// First writer's state survives.
	st, _, _ := slackchannel.StateStore{Blob: store}.Load(ctx, 7)
	if st.MainTS != "winner-ts" {
		t.Fatalf("state clobbered: %+v", st)
	}
}

func TestSlackIgnoresDriftPayloads(t *testing.T) {
	fs := &fakeSlack{}
	s := testSlackChannel(fs, newMemStore())
	if err := s.Deliver(context.Background(), notify.Payload{
		Event: notify.EventDriftDetected, Drift: &notify.DriftPayload{Project: "a", Stack: "b"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(fs.calls) != 0 {
		t.Fatalf("drift must not post: %+v", fs.calls)
	}
}

// --- constructors --------------------------------------------------------

func TestConstructorsSkipOnMissingDeps(t *testing.T) {
	ctx := context.Background()
	if s, err := NewSlack(ctx, schemas.ChannelYAML{Type: "timeline_slack"}, notify.Deps{Blob: newMemStore()}); err != nil || s != nil {
		t.Fatalf("no token: want skip, got %v %v", s, err)
	}
	if s, err := NewSlack(ctx, schemas.ChannelYAML{Type: "timeline_slack", AuthToken: "xoxb-1"}, notify.Deps{}); err != nil || s != nil {
		t.Fatalf("no blob: want skip, got %v %v", s, err)
	}
	if s, err := NewGitHub(ctx, schemas.ChannelYAML{Type: "timeline_github"}, notify.Deps{Blob: newMemStore()}); err != nil || s != nil {
		t.Fatalf("no comments: want skip, got %v %v", s, err)
	}
	if s, err := NewGitHub(ctx, schemas.ChannelYAML{Type: "timeline_github"}, notify.Deps{Comments: &fakeComments{}}); err != nil || s != nil {
		t.Fatalf("no blob: want skip, got %v %v", s, err)
	}
}

func TestDefaultSubscriptionsCoverAllTimelineEvents(t *testing.T) {
	ctx := context.Background()
	g, err := NewGitHub(ctx, schemas.ChannelYAML{Type: "timeline_github"},
		notify.Deps{Comments: &fakeComments{}, Blob: newMemStore()})
	if err != nil || g == nil {
		t.Fatalf("NewGitHub: %v %v", g, err)
	}
	want := notify.TimelinePREvents()
	got := g.Subscribes()
	if len(got) != len(want) {
		t.Fatalf("defaults: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("defaults: got %v want %v", got, want)
		}
	}
	// planning first, break_glass last: the surface stays extensible.
	if got[0] != notify.EventPlanning || got[len(got)-1] != notify.EventBreakGlass {
		t.Fatalf("timeline event order: %v", got)
	}

	sl, err := NewSlack(ctx, schemas.ChannelYAML{Type: "timeline_slack", AuthToken: "xoxb-1", On: []string{"applied", "failed"}},
		notify.Deps{Blob: newMemStore()})
	if err != nil || sl == nil {
		t.Fatalf("NewSlack: %v %v", sl, err)
	}
	if evs := sl.Subscribes(); len(evs) != 2 || evs[0] != notify.EventApplied {
		t.Fatalf("explicit on: %v", evs)
	}
}
