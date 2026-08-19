package timeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/notify"
	slackchannel "github.com/reeveops/reeve/internal/notify/channels/slack"
	slackapi "github.com/reeveops/reeve/internal/slack"
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
		PR: 7, CommitSHA: sha, RunID: "run-" + string(ev), RunURL: "https://ci/run/" + string(ev),
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
	t.Parallel()

	fc := &fakeComments{}
	s := testGitHubChannel(fc, newMemStore())
	ctx := context.Background()

	must := func(p notify.Payload) {
		t.Helper()
		if err := s.Deliver(ctx, p); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
	}
	for _, p := range planRun("aaaa111bbbb", "push-1", false) {
		must(p)
	}
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
	// Both preview lifecycle entries link the run they share.
	if strings.Count(second, "https://ci/run/push-1") != 2 {
		t.Fatalf("shared run URL missing:\n%s", second)
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
	series := st.Series["aaaa111"]
	if len(series) != 1 {
		t.Fatalf("non-plan events must not open a series: %+v", series)
	}
	entries := series[0]
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

// --- plan series ---------------------------------------------------------

// requestedPlan is a planning payload an operator explicitly asked for, which
// is what opens a new series on a SHA that already has one.
func requestedPlan(sha, run string) notify.Payload {
	p := payload(notify.EventPlanning, sha)
	p.PR.PlanRequested = true
	p.PR.RunID = run
	p.PR.RunURL = "https://ci/run/" + run
	return p
}

func finishedPlan(sha, run string) notify.Payload {
	p := payload(notify.EventPlan, sha)
	p.PR.RunID = run
	p.PR.RunURL = "https://ci/run/" + run
	return p
}

func planRun(sha, run string, requested bool) []notify.Payload {
	start := requestedPlan(sha, run)
	start.PR.PlanRequested = requested
	return []notify.Payload{start, finishedPlan(sha, run)}
}

func withoutRunURLs(payloads []notify.Payload) []notify.Payload {
	for i := range payloads {
		payloads[i].PR.RunURL = ""
	}
	return payloads
}

func TestGitHubSeriesCountsAndMarkers(t *testing.T) {
	t.Parallel()
	const sha = "aaaa111bbbb"
	tests := []struct {
		name         string
		payloads     []notify.Payload
		wantSeries   int
		wantMarkers  []string
		lastContains []string
		lastStarts   int
	}{
		{
			name: "explicit plan opens new series",
			payloads: append(planRun(sha, "push-1", false),
				planRun(sha, "requested-2", true)...),
			wantSeries: 2,
			wantMarkers: []string{
				CommentMarker(sha), CommentMarker(sha),
				SeriesMarker(sha, 2), SeriesMarker(sha, 2),
			},
			lastContains: []string{"plan 2"},
			lastStarts:   1,
		},
		{
			name: "non-explicit retry stays in current series",
			payloads: []notify.Payload{
				payload(notify.EventPlanning, sha),
				payload(notify.EventPlanning, sha),
				payload(notify.EventPlanning, sha),
			},
			wantSeries:  1,
			wantMarkers: []string{CommentMarker(sha), CommentMarker(sha), CommentMarker(sha)},
		},
		{
			name: "new SHA opens its own series",
			payloads: []notify.Payload{
				payload(notify.EventPlanning, sha),
				payload(notify.EventPlanning, "cccc222dddd"),
			},
			wantSeries:  2,
			wantMarkers: []string{CommentMarker(sha), CommentMarker("cccc222dddd")},
		},
		{
			name: "duplicate explicit delivery reuses request identity",
			payloads: []notify.Payload{
				payload(notify.EventPlanning, sha),
				requestedPlan(sha, "requested-2"),
				requestedPlan(sha, "requested-2"),
			},
			wantSeries:  2,
			wantMarkers: []string{CommentMarker(sha), SeriesMarker(sha, 2), SeriesMarker(sha, 2)},
		},
		{
			name: "run ID routes finish when run URL is empty",
			payloads: append(withoutRunURLs(planRun(sha, "push-1", false)),
				withoutRunURLs(planRun(sha, "requested-2", true))...),
			wantSeries: 2,
			wantMarkers: []string{
				CommentMarker(sha), CommentMarker(sha),
				SeriesMarker(sha, 2), SeriesMarker(sha, 2),
			},
		},
		{
			name: "unknown finish identity opens recovery series",
			payloads: []notify.Payload{
				requestedPlan(sha, "requested-1"),
				requestedPlan(sha, "requested-2"),
				finishedPlan(sha, "unknown-finish"),
			},
			wantSeries: 3,
			wantMarkers: []string{
				CommentMarker(sha), SeriesMarker(sha, 2), SeriesMarker(sha, 3),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := newMemStore()
			fc := &fakeComments{}
			s := testGitHubChannel(fc, store)
			ctx := context.Background()
			for _, p := range tt.payloads {
				if err := s.Deliver(ctx, p); err != nil {
					t.Fatalf("Deliver %s: %v", p.Event, err)
				}
			}

			st, _, err := s.loadState(ctx, 7)
			if err != nil {
				t.Fatal(err)
			}
			var gotSeries int
			for _, series := range st.Series {
				gotSeries += len(series)
			}
			if gotSeries != tt.wantSeries {
				t.Fatalf("series count = %d, want %d: %+v", gotSeries, tt.wantSeries, st.Series)
			}

			gotMarkers := make([]string, 0, len(fc.upserts))
			for _, u := range fc.upserts {
				gotMarkers = append(gotMarkers, u.marker)
			}
			if !slices.Equal(gotMarkers, tt.wantMarkers) {
				t.Fatalf("markers = %v, want %v", gotMarkers, tt.wantMarkers)
			}
			lastBody := fc.upserts[len(fc.upserts)-1].body
			for _, want := range tt.lastContains {
				if !strings.Contains(lastBody, want) {
					t.Fatalf("last comment missing %q:\n%s", want, lastBody)
				}
			}
			if tt.lastStarts > 0 && strings.Count(lastBody, "**preview started**") != tt.lastStarts {
				t.Fatalf("last comment has wrong preview-started count:\n%s", lastBody)
			}
		})
	}
}

func TestGitHubPreSeriesStateLoadsAsSeriesOne(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	fc := &fakeComments{}
	s := testGitHubChannel(fc, store)
	ctx := context.Background()

	// State as written before series grouping existed.
	legacy := `{"entries":{"aaaa111":[{"event":"planning","sha":"aaaa111bbbb","at":"2026-07-19T12:03:05Z","run_url":"https://ci/run/plan"}]}}`
	if _, err := store.Put(ctx, legacyStateKey(7), strings.NewReader(legacy)); err != nil {
		t.Fatal(err)
	}

	if err := s.Deliver(ctx, payload(notify.EventPlan, "aaaa111bbbb")); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	// The pre-series history is preserved, in series 1, under the marker the
	// existing comment already carries.
	u := fc.upserts[0]
	if u.marker != CommentMarker("aaaa111bbbb") {
		t.Fatalf("migrated state changed the marker: %q", u.marker)
	}
	if !strings.Contains(u.body, "**preview started**") || !strings.Contains(u.body, "**preview finished**") {
		t.Fatalf("migration lost history:\n%s", u.body)
	}
	if strings.Contains(u.body, "plan 1") {
		t.Fatalf("series 1 header must stay unnumbered:\n%s", u.body)
	}

	st, _, err := s.loadState(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Series["aaaa111"]) != 1 || len(st.Entries) != 0 {
		t.Fatalf("state not migrated: %+v", st)
	}
	if _, _, err := store.Get(ctx, legacyStateKey(7)); err != nil {
		t.Fatalf("legacy state was not retained for downgrade safety: %v", err)
	}
	if _, _, err := store.Get(ctx, stateKey(7)); err != nil {
		t.Fatalf("versioned state was not written: %v", err)
	}
	// Simulate a pinned pre-series binary rewriting the legacy object after
	// migration. The versioned history must remain authoritative.
	if _, err := store.Put(ctx, legacyStateKey(7), strings.NewReader(`{"entries":{}}`)); err != nil {
		t.Fatal(err)
	}
	st, _, err = s.loadState(ctx, 7)
	if err != nil || len(st.Series["aaaa111"]) != 1 || len(st.Series["aaaa111"][0]) != 2 {
		t.Fatalf("legacy rewrite replaced versioned history: state=%+v err=%v", st, err)
	}
}

func TestGitHubConcurrentSeriesMintsDoNotCollide(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	fc := &fakeComments{}
	s := testGitHubChannel(fc, store)
	ctx := context.Background()

	const sha = "aaaa111bbbb"
	if err := s.Deliver(ctx, payload(notify.EventPlanning, sha)); err != nil {
		t.Fatal(err)
	}

	// Two explicitly requested plans race. Each must land in its own series
	// rather than both claiming the same ordinal.
	store.putIfMatchHook = func() {
		other := testGitHubChannel(&fakeComments{}, store)
		if err := other.Deliver(ctx, requestedPlan(sha, "requested-2")); err != nil {
			t.Errorf("concurrent Deliver: %v", err)
		}
	}
	if err := s.Deliver(ctx, requestedPlan(sha, "requested-3")); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	st, _, err := s.loadState(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	series := st.Series[shortSHA(sha)]
	if len(series) != 3 {
		t.Fatalf("want 3 series (1 push + 2 requested), got %d: %+v", len(series), series)
	}
	for i, entries := range series {
		if len(entries) != 1 {
			t.Fatalf("series %d holds %d entries, want 1", i+1, len(entries))
		}
	}
}

func TestGitHubOverlappingPlansFinishInTheirOwnSeries(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	fc := &fakeComments{}
	s := testGitHubChannel(fc, store)
	ctx := context.Background()

	const sha = "aaaa111bbbb"
	for _, p := range []notify.Payload{
		payload(notify.EventPlanning, sha),
		requestedPlan(sha, "requested-2"),
		requestedPlan(sha, "requested-3"),
		finishedPlan(sha, "requested-2"),
		finishedPlan(sha, "requested-3"),
	} {
		if err := s.Deliver(ctx, p); err != nil {
			t.Fatalf("Deliver %s: %v", p.Event, err)
		}
	}

	st, _, err := s.loadState(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	series := st.Series[shortSHA(sha)]
	if len(series) != 3 {
		t.Fatalf("series count = %d, want 3", len(series))
	}
	for i, run := range []string{"planning", "requested-2", "requested-3"} {
		entries := series[i]
		want := "https://ci/run/" + run
		for _, entry := range entries {
			if entry.RunURL != want {
				t.Fatalf("series %d contains run %q, want %q: %+v", i+1, entry.RunURL, want, entries)
			}
		}
	}
	if got := fc.upserts[3].marker; got != SeriesMarker(sha, 2) {
		t.Fatalf("second plan finish updated marker %q, want series 2", got)
	}
	if got := fc.upserts[4].marker; got != SeriesMarker(sha, 3) {
		t.Fatalf("third plan finish updated marker %q, want series 3", got)
	}
}

func TestSeriesMarkerFirstSeriesIsUnchanged(t *testing.T) {
	t.Parallel()
	const sha = "aaaa111bbbb"
	// A marker change orphans live comments, so series 1 must stay exactly
	// what the pre-series channel emitted.
	if got := SeriesMarker(sha, 1); got != CommentMarker(sha) {
		t.Fatalf("SeriesMarker(1) = %q, want %q", got, CommentMarker(sha))
	}
	if got := SeriesMarker(sha, 0); got != CommentMarker(sha) {
		t.Fatalf("SeriesMarker(0) = %q, want %q", got, CommentMarker(sha))
	}
	if got, want := SeriesMarker(sha, 2), "<!-- reeve:timeline:v1:aaaa111:2 -->"; got != want {
		t.Fatalf("SeriesMarker(2) = %q, want %q", got, want)
	}
}

func TestSlackNewSeriesKeepsOneThread(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	fs := &fakeSlack{}
	s := testSlackChannel(fs, store)
	ctx := context.Background()

	const sha = "aaaa111bbbb"
	for _, p := range []notify.Payload{
		payload(notify.EventPlanning, sha),
		requestedPlan(sha, "requested-2"),
	} {
		if err := s.Deliver(ctx, p); err != nil {
			t.Fatalf("Deliver %s: %v", p.Event, err)
		}
	}
	// A new plan series is a GitHub-comment concept: Slack keeps one anchor
	// and threads every entry under it.
	var anchors, replies int
	parents := map[string]bool{}
	for _, c := range fs.calls {
		switch c.method {
		case "post":
			anchors++
		case "thread":
			replies++
			parents[c.parentTS] = true
		}
	}
	if anchors != 1 {
		t.Fatalf("new series created %d anchors, want 1", anchors)
	}
	if replies != 2 {
		t.Fatalf("want 2 thread replies, got %d", replies)
	}
	if len(parents) != 1 {
		t.Fatalf("replies split across %d threads, want 1: %v", len(parents), parents)
	}
}
