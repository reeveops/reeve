package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/FynxLabs/reeve/internal/blob"
)

// AppliedState is the durable record written after a fully-successful apply.
// It lets a later preview/apply at the SAME commit detect that there is
// nothing new to do and short-circuit (unless --force is given).
type AppliedState struct {
	CommitSHA string `json:"commit_sha"`
	RunID     string `json:"run_id"`
	RunNumber int    `json:"run_number"`
	AppliedAt string `json:"applied_at"` // RFC3339
	PR        int    `json:"pr"`
}

// appliedStateKey is the blob key for a given PR + commit's applied marker.
// Keyed by SHA so each commit gets exactly one pointer; a re-apply of the
// same SHA overwrites it.
func appliedStateKey(pr int, sha string) string {
	if pr == 0 {
		return fmt.Sprintf("runs/local/applied/%s.json", sha)
	}
	return fmt.Sprintf("runs/pr-%d/applied/%s.json", pr, sha)
}

// writeAppliedState records that sha was fully applied. No-op when store is
// nil (local runs without a configured bucket) or sha is empty.
func writeAppliedState(ctx context.Context, store blob.Store, st AppliedState) error {
	if store == nil || st.CommitSHA == "" {
		return nil
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	_, err = store.Put(ctx, appliedStateKey(st.PR, st.CommitSHA), bytes.NewReader(data))
	return err
}

// readAppliedState returns the applied marker for pr+sha, or (nil, nil) when
// none exists. A missing object is not an error.
func readAppliedState(ctx context.Context, store blob.Store, pr int, sha string) (*AppliedState, error) {
	if store == nil || sha == "" {
		return nil, nil
	}
	rc, _, err := store.Get(ctx, appliedStateKey(pr, sha))
	if err != nil {
		// Missing pointer => not yet applied. Treat any Get failure as
		// "unknown / not applied" so a transient blob error never blocks a
		// legitimate apply; the guard is an optimization, not a gate.
		return nil, nil
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, nil
	}
	var st AppliedState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, nil
	}
	return &st, nil
}
