package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/FynxLabs/reeve/internal/blob"
)

// PRState is the per-PR Slack message state persisted at
// notifications/pr-{n}/slack.json. It is shared between the dashboard slack
// channel (which owns the main message) and the timeline_slack channel (which
// threads timeline entries under it) so both agree on ONE anchor per PR.
type PRState struct {
	Channel  string `json:"channel"`
	MainTS   string `json:"main_ts"`
	ThreadTS string `json:"thread_ts,omitempty"`
	// ThreadOwner names the channel that owns the anchor's thread. When set,
	// the dashboard channel suppresses its courtesy thread entries so the
	// timeline channel's entries are the only replies (no double posting).
	ThreadOwner string `json:"thread_owner,omitempty"`
}

// PRStateKey is the blob key for a PR's Slack message state.
func PRStateKey(pr int) string { return fmt.Sprintf("notifications/pr-%d/slack.json", pr) }

// StateStore loads and CAS-saves PRState in the blob store. It is consumed
// by both slack-facing channels; the zero value is not usable (Blob required).
type StateStore struct {
	Blob blob.Store
}

// Load reads the per-PR message state plus its ETag for the
// compare-and-swap on save. A missing object is a legitimately fresh state;
// any other failure is propagated - swallowing it here made the channel believe
// no message existed and post a duplicate.
func (ss StateStore) Load(ctx context.Context, pr int) (*PRState, string, error) {
	rc, meta, err := ss.Blob.Get(ctx, PRStateKey(pr))
	if errors.Is(err, blob.ErrNotFound) {
		return &PRState{}, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("load slack state for pr %d: %w", pr, err)
	}
	defer rc.Close()
	var st PRState
	if err := json.NewDecoder(rc).Decode(&st); err != nil {
		return nil, "", fmt.Errorf("decode slack state for pr %d: %w", pr, err)
	}
	etag := ""
	if meta != nil {
		etag = meta.ETag
	}
	return &st, etag, nil
}

// Save writes the state conditionally on the ETag observed at load
// (empty ETag = create-only). On a conflict, another run raced us: if it
// recorded the same main message, retry over its version (keeping its
// thread TS and owner); if it recorded a different message, keep the first
// writer's state and report the conflict.
func (ss StateStore) Save(ctx context.Context, pr int, st *PRState, etag string) error {
	for attempt := 0; attempt < 3; attempt++ {
		data, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return err
		}
		_, err = ss.Blob.PutIfMatch(ctx, PRStateKey(pr), strings.NewReader(string(data)), etag)
		if err == nil {
			return nil
		}
		if !errors.Is(err, blob.ErrPreconditionFailed) {
			return fmt.Errorf("save slack state for pr %d: %w", pr, err)
		}
		remote, remoteETag, lerr := ss.Load(ctx, pr)
		if lerr != nil {
			return fmt.Errorf("save slack state for pr %d: conflict, then: %w", pr, lerr)
		}
		if remote.MainTS != "" && remote.MainTS != st.MainTS {
			// A concurrent run created its own message first. Keep its
			// state (first writer wins) and surface the duplicate.
			slog.Warn("slack state conflict: concurrent message exists, keeping it",
				"pr", pr, "ours", st.MainTS, "theirs", remote.MainTS)
			return fmt.Errorf("slack state for pr %d: concurrent update created message ts=%s (ours ts=%s discarded)", pr, remote.MainTS, st.MainTS)
		}
		if st.ThreadTS == "" {
			st.ThreadTS = remote.ThreadTS
		}
		if st.ThreadOwner == "" {
			st.ThreadOwner = remote.ThreadOwner
		}
		etag = remoteETag
	}
	return fmt.Errorf("save slack state for pr %d: too many conflicts", pr)
}

// loadPRState / savePRState keep the dashboard channel's call sites (and their
// tests) reading naturally; they delegate to the shared StateStore.
func (s *Channel) loadPRState(ctx context.Context, pr int) (*PRState, string, error) {
	return StateStore{Blob: s.blob}.Load(ctx, pr)
}

func (s *Channel) savePRState(ctx context.Context, pr int, st *PRState, etag string) error {
	return StateStore{Blob: s.blob}.Save(ctx, pr, st, etag)
}
