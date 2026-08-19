package timeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/notify"
)

func init() {
	notify.Register("timeline_github", NewGitHub)
}

// CommentMarker pins one timeline comment per commit SHA. This is a NEW
// marker namespace: the dashboard comment markers (reeve:pr-comment:v1,
// reeve:apply:v1, reeve:help, reeve:apply-timeline:{sha}) are sacred - a
// changed marker orphans existing comments - so the timeline never touches
// them.
func CommentMarker(sha string) string {
	return fmt.Sprintf("<!-- reeve:timeline:v1:%s -->", shortSHA(sha))
}

// SeriesMarker pins one comment per plan series. Series 1 IS CommentMarker,
// byte-identical: a PR that already carries a timeline comment must keep
// having it edited in place rather than being orphaned by a marker change.
// Later series carry the ordinal, which is what makes each new plan a new
// comment instead of an edit of the previous plan's.
func SeriesMarker(sha string, series int) string {
	if series <= 1 {
		return CommentMarker(sha)
	}
	return fmt.Sprintf("<!-- reeve:timeline:v1:%s:%d -->", shortSHA(sha), series)
}

// stateKey is versioned so a workflow pinned to a pre-series binary cannot
// decode and then overwrite series-aware state. The current loader imports
// legacyStateKey once when no v2 object exists; old binaries keep writing the
// legacy object without truncating v2 history.
func stateKey(pr int) string { return fmt.Sprintf("notifications/pr-%d/timeline-v2.json", pr) }

func legacyStateKey(pr int) string { return fmt.Sprintf("notifications/pr-%d/timeline.json", pr) }

// ghState is the persisted timeline. Entries are grouped by short SHA and,
// within a SHA, by plan series: a new plan opens a new series (and so a new
// comment) instead of appending to the previous plan's log.
//
// Entries is the pre-series field and is still read: state written before
// series grouping loads as that SHA's series 1, so an upgrade mid-PR keeps
// its history and its comment.
type ghState struct {
	Entries map[string][]Entry `json:"entries,omitempty"`
	// Series holds each SHA's series in order; index 0 is series 1.
	Series map[string][][]Entry `json:"series,omitempty"`
}

// normalize migrates the pre-series Entries field into Series and ensures
// both maps are non-nil. The legacy object itself remains untouched; migrated
// state is persisted under the versioned key on the next append.
func (st *ghState) normalize() {
	if st.Series == nil {
		st.Series = map[string][][]Entry{}
	}
	for sha, entries := range st.Entries {
		if len(entries) == 0 {
			continue
		}
		if _, ok := st.Series[sha]; !ok {
			st.Series[sha] = [][]Entry{entries}
		}
	}
	// Entries has been folded into Series; drop it so the object does not
	// carry two copies of the same history forward.
	st.Entries = nil
}

// appendTo adds e to the SHA's series, opening a new one when the event calls
// for it. It returns the series ordinal (1-based) and that series' entries.
//
// A plan opens a new series when the operator explicitly asked for it, or
// when the SHA has none yet (a new commit). A plan that is neither - a
// retried or re-dispatched CI job for a plan already logged on this SHA -
// appends to the current series, so one plan is never split across two
// comments. Every other event appends to the SHA's most recent series,
// opening series 1 if apply arrives on a commit whose plan predates this.
func (st *ghState) appendTo(sha string, e Entry, newSeries bool, targetSeries int) (int, []Entry) {
	series := st.Series[sha]
	var selected int
	if len(series) == 0 || newSeries {
		series = append(series, []Entry{e})
		selected = len(series)
	} else if targetSeries > 0 && targetSeries <= len(series) {
		series[targetSeries-1] = append(series[targetSeries-1], e)
		selected = targetSeries
	} else {
		series[len(series)-1] = append(series[len(series)-1], e)
		selected = len(series)
	}
	st.Series[sha] = series
	return selected, series[selected-1]
}

// seriesForRun returns the series already associated with a durable CI run
// identity. RunID is authoritative when present. RunURL remains a fallback
// for state written before RunID was persisted.
// Preview-started and preview-finished are separate deliveries, and another
// explicit preview can open a newer series between them. Matching identity
// keeps each finish event with the start event from the same run.
func (st *ghState) seriesForRun(sha string, run Entry) int {
	series := st.Series[sha]
	if run.RunID != "" {
		for i := len(series) - 1; i >= 0; i-- {
			for _, entry := range series[i] {
				if entry.RunID == run.RunID {
					return i + 1
				}
			}
		}
	}
	if run.RunURL == "" {
		return 0
	}
	for i := len(series) - 1; i >= 0; i-- {
		for _, entry := range series[i] {
			// Do not merge two known, distinct run IDs merely because their
			// display URL matches. Empty RunID is legacy state.
			if entry.RunURL == run.RunURL && (run.RunID == "" || entry.RunID == "") {
				return i + 1
			}
		}
	}
	return 0
}

// opensSeries reports whether this delivery starts a new plan series.
func opensSeries(p notify.Payload, existing int) bool {
	if p.Event != notify.EventPlanning && p.Event != notify.EventPlan {
		return false
	}
	if existing == 0 {
		return true
	}
	// EventPlan is the same plan EventPlanning already opened; only the
	// starting event of an explicitly requested plan mints a series.
	return p.Event == notify.EventPlanning && p.PR.PlanRequested
}

// GitHubChannel maintains one PR comment per plan series: each event appends
// an entry to the series in blob state (CAS) and rewrites that series' comment
// in place. This makes preview start/finish visible entries -
// GitHub renders comment edits silently, so an edited-in-place dashboard
// alone can't answer "did it even run?".
type GitHubChannel struct {
	name     string
	comments notify.CommentClient
	blob     blob.Store
	events   []notify.Event
	now      func() time.Time
}

// NewGitHub is the registered constructor for `timeline_github`. Skipped
// without a comment client or blob store, matching the framework's
// unmet-optional-dependency convention.
func NewGitHub(_ context.Context, cfg schemas.ChannelYAML, deps notify.Deps) (notify.Channel, error) {
	if deps.Comments == nil || deps.Blob == nil {
		return nil, nil
	}
	events := notify.ParseEvents(cfg.On)
	if len(cfg.On) == 0 {
		events = notify.TimelinePREvents()
	}
	return &GitHubChannel{
		name:     cfg.EffectiveName(),
		comments: deps.Comments,
		blob:     deps.Blob,
		events:   events,
		now:      time.Now,
	}, nil
}

func (s *GitHubChannel) Name() string               { return s.name }
func (s *GitHubChannel) Subscribes() []notify.Event { return s.events }

// Deliver appends the entry to its plan series and upserts that series' comment.
func (s *GitHubChannel) Deliver(ctx context.Context, p notify.Payload) error {
	if p.PR == nil || p.PR.PR <= 0 {
		return nil
	}
	in := *p.PR
	entry := newEntry(p, s.now())
	series, entries, err := s.appendEntry(ctx, in.PR, entry, p)
	if err != nil {
		return err
	}
	body := renderComment(in.CommitSHA, series, entries)
	return s.comments.UpsertComment(ctx, in.PR, body, SeriesMarker(in.CommitSHA, series))
}

// appendEntry persists the entry with compare-and-swap and returns the full
// entry list for its SHA. On conflict a concurrent run wrote first: reload
// its state and re-append, so no writer's entries are lost.
func (s *GitHubChannel) appendEntry(ctx context.Context, pr int, e Entry, p notify.Payload) (int, []Entry, error) {
	sha := shortSHA(e.SHA)
	for attempt := 0; attempt < 3; attempt++ {
		st, etag, err := s.loadState(ctx, pr)
		if err != nil {
			return 0, nil, err
		}
		// Decided inside the CAS loop: whether this delivery opens a series
		// depends on what is already stored, so a concurrent writer that
		// won the race is accounted for on retry. A finish event targets the
		// series opened by its own CI run even if another plan started later.
		targetSeries := 0
		newSeries := opensSeries(p, len(st.Series[sha]))
		if p.Event == notify.EventPlan {
			targetSeries = st.seriesForRun(sha, e)
			// A finish with an unknown identity must not be attached to the
			// newest series: overlapping plans make that guess unsafe. Isolate
			// it in a recovery series instead.
			if targetSeries == 0 && len(st.Series[sha]) > 0 {
				newSeries = true
			}
		}
		if p.Event == notify.EventPlanning && p.PR.PlanRequested {
			if existing := st.seriesForRun(sha, e); existing > 0 {
				newSeries = false
				targetSeries = existing
			}
		}
		series, entries := st.appendTo(sha, e, newSeries, targetSeries)
		data, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return 0, nil, err
		}
		_, err = s.blob.PutIfMatch(ctx, stateKey(pr), strings.NewReader(string(data)), etag)
		if err == nil {
			return series, entries, nil
		}
		if !errors.Is(err, blob.ErrPreconditionFailed) {
			return 0, nil, fmt.Errorf("save timeline state for pr %d: %w", pr, err)
		}
	}
	return 0, nil, fmt.Errorf("save timeline state for pr %d: too many conflicts", pr)
}

// loadState reads the per-PR timeline. Missing object = fresh state; any
// other failure propagates so an outage cannot silently drop history.
func (s *GitHubChannel) loadState(ctx context.Context, pr int) (*ghState, string, error) {
	st, etag, err := s.loadStateObject(ctx, pr, stateKey(pr))
	if err == nil {
		return st, etag, nil
	}
	if !errors.Is(err, blob.ErrNotFound) {
		return nil, "", err
	}

	// Import pre-series state without reusing its ETag: the next append must
	// create the independent v2 object, leaving the old key downgrade-safe.
	st, _, err = s.loadStateObject(ctx, pr, legacyStateKey(pr))
	if err == nil {
		return st, "", nil
	}
	if errors.Is(err, blob.ErrNotFound) {
		return &ghState{Series: map[string][][]Entry{}}, "", nil
	}
	return nil, "", err
}

func (s *GitHubChannel) loadStateObject(ctx context.Context, pr int, key string) (*ghState, string, error) {
	rc, meta, err := s.blob.Get(ctx, key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return nil, "", blob.ErrNotFound
		}
		return nil, "", fmt.Errorf("load timeline state for pr %d: %w", pr, err)
	}
	defer rc.Close()
	var st ghState
	if err := json.NewDecoder(rc).Decode(&st); err != nil {
		return nil, "", fmt.Errorf("decode timeline state for pr %d: %w", pr, err)
	}
	st.normalize()
	etag := ""
	if meta != nil {
		etag = meta.ETag
	}
	return &st, etag, nil
}

// renderComment rebuilds one series' full timeline comment from its entries.
func renderComment(sha string, series int, entries []Entry) string {
	var b strings.Builder
	b.WriteString(SeriesMarker(sha, series) + "\n")
	fmt.Fprintf(&b, "### 🛰️ reeve · deployment timeline · commit `%s`", shortSHA(sha))
	if series > 1 {
		// Only later series are numbered: the first comment on a commit
		// reads the same as it did before series existed.
		fmt.Fprintf(&b, " · plan %d", series)
	}
	b.WriteString("\n\n")
	for _, e := range entries {
		b.WriteString(e.markdownLine() + "\n")
	}
	return b.String()
}
