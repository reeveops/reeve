package timeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FynxLabs/reeve/internal/blob"
	"github.com/FynxLabs/reeve/internal/config/schemas"
	"github.com/FynxLabs/reeve/internal/notify"
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

// stateKey is the per-PR blob object holding the accumulated entries.
// Preview and apply are separate CI processes, so the entry history must be
// persisted - a comment is re-rendered whole from state on every event.
func stateKey(pr int) string { return fmt.Sprintf("notifications/pr-%d/timeline.json", pr) }

// ghState is the persisted timeline, entries grouped by short SHA.
type ghState struct {
	Entries map[string][]Entry `json:"entries"`
}

// GitHubChannel maintains one PR comment per commit SHA: each event appends an
// entry to the SHA's group in blob state (CAS) and rewrites that SHA's
// comment in place. This makes preview start/finish visible entries -
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

// Deliver appends the entry to its SHA group and upserts that SHA's comment.
func (s *GitHubChannel) Deliver(ctx context.Context, p notify.Payload) error {
	if p.PR == nil || p.PR.PR <= 0 {
		return nil
	}
	in := *p.PR
	entry := newEntry(p, s.now())
	entries, err := s.appendEntry(ctx, in.PR, entry)
	if err != nil {
		return err
	}
	body := renderComment(in.CommitSHA, entries)
	return s.comments.UpsertComment(ctx, in.PR, body, CommentMarker(in.CommitSHA))
}

// appendEntry persists the entry with compare-and-swap and returns the full
// entry list for its SHA. On conflict a concurrent run wrote first: reload
// its state and re-append, so no writer's entries are lost.
func (s *GitHubChannel) appendEntry(ctx context.Context, pr int, e Entry) ([]Entry, error) {
	sha := shortSHA(e.SHA)
	for attempt := 0; attempt < 3; attempt++ {
		st, etag, err := s.loadState(ctx, pr)
		if err != nil {
			return nil, err
		}
		st.Entries[sha] = append(st.Entries[sha], e)
		data, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return nil, err
		}
		_, err = s.blob.PutIfMatch(ctx, stateKey(pr), strings.NewReader(string(data)), etag)
		if err == nil {
			return st.Entries[sha], nil
		}
		if !errors.Is(err, blob.ErrPreconditionFailed) {
			return nil, fmt.Errorf("save timeline state for pr %d: %w", pr, err)
		}
	}
	return nil, fmt.Errorf("save timeline state for pr %d: too many conflicts", pr)
}

// loadState reads the per-PR timeline. Missing object = fresh state; any
// other failure propagates so an outage cannot silently drop history.
func (s *GitHubChannel) loadState(ctx context.Context, pr int) (*ghState, string, error) {
	rc, meta, err := s.blob.Get(ctx, stateKey(pr))
	if errors.Is(err, blob.ErrNotFound) {
		return &ghState{Entries: map[string][]Entry{}}, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("load timeline state for pr %d: %w", pr, err)
	}
	defer rc.Close()
	var st ghState
	if err := json.NewDecoder(rc).Decode(&st); err != nil {
		return nil, "", fmt.Errorf("decode timeline state for pr %d: %w", pr, err)
	}
	if st.Entries == nil {
		st.Entries = map[string][]Entry{}
	}
	etag := ""
	if meta != nil {
		etag = meta.ETag
	}
	return &st, etag, nil
}

// renderComment rebuilds one SHA's full timeline comment from its entries.
func renderComment(sha string, entries []Entry) string {
	var b strings.Builder
	b.WriteString(CommentMarker(sha) + "\n")
	fmt.Fprintf(&b, "### 🛰️ reeve · deployment timeline · commit `%s`\n\n", shortSHA(sha))
	for _, e := range entries {
		b.WriteString(e.markdownLine() + "\n")
	}
	return b.String()
}
