package drift

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/FynxLabs/reeve/internal/blob"
)

// ErrNoRuns is returned by StoredReport when the bucket holds no drift
// runs yet.
var ErrNoRuns = errors.New("no drift runs found")

// StoredReport renders the latest persisted drift run from the bucket.
// format "markdown" (or "") returns report.md verbatim; "json" re-emits
// the stored JSON artifacts - the run manifest plus every per-stack result
// - as one document. Latest = lexically last run ID, which is
// chronological because run IDs embed a UTC timestamp.
func StoredReport(ctx context.Context, store blob.Store, format string) (string, error) {
	switch format {
	case "markdown", "", "json":
	default:
		return "", fmt.Errorf("unknown format %q (markdown|json)", format)
	}
	keys, err := store.List(ctx, "drift/runs")
	if err != nil {
		return "", err
	}
	latest := ""
	for _, k := range keys {
		trimmed := strings.TrimPrefix(k, "drift/runs/")
		if i := strings.Index(trimmed, "/"); i > 0 {
			if id := trimmed[:i]; id > latest {
				latest = id
			}
		}
	}
	if latest == "" {
		return "", ErrNoRuns
	}
	prefix := "drift/runs/" + latest

	switch format {
	case "markdown", "":
		b, err := readAllKey(ctx, store, prefix+"/report.md")
		if err != nil {
			return "", fmt.Errorf("read %s/report.md: %w", prefix, err)
		}
		return string(b), nil

	case "json":
		manifest, err := readAllKey(ctx, store, prefix+"/manifest.json")
		if err != nil {
			return "", fmt.Errorf("read %s/manifest.json: %w", prefix, err)
		}
		var items []json.RawMessage
		for _, k := range keys {
			if !strings.HasPrefix(k, prefix+"/results/") || !strings.HasSuffix(k, ".json") {
				continue
			}
			b, err := readAllKey(ctx, store, k)
			if err != nil {
				return "", fmt.Errorf("read %s: %w", k, err)
			}
			items = append(items, json.RawMessage(b))
		}
		doc := struct {
			RunID    string            `json:"run_id"`
			Manifest json.RawMessage   `json:"manifest"`
			Items    []json.RawMessage `json:"items"`
		}{RunID: latest, Manifest: manifest, Items: items}
		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return "", err
		}
		return string(out), nil

	default: // unreachable; format validated above
		return "", fmt.Errorf("unknown format %q (markdown|json)", format)
	}
}

func readAllKey(ctx context.Context, store blob.Store, key string) ([]byte, error) {
	rc, _, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
