package drift

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/FynxLabs/reeve/internal/blob"
)

// Suppression is an active drift suppression for a stack.
type Suppression struct {
	Project string    `json:"project"`
	Stack   string    `json:"stack"`
	Until   time.Time `json:"until"`
	Reason  string    `json:"reason"`
	Creator string    `json:"creator,omitempty"`
}

// Active reports whether the suppression is still in effect at now.
func (s Suppression) Active(now time.Time) bool {
	if s.Until.IsZero() {
		return false
	}
	return now.Before(s.Until)
}

// SuppressionStore persists active suppressions at
// drift/suppressions/{project}/{stack}.json.
type SuppressionStore struct{ Blob blob.Store }

func (s *SuppressionStore) key(project, stack string) string {
	return fmt.Sprintf("drift/suppressions/%s/%s.json", project, stack)
}

// Get returns the active suppression for a stack, or (zero, false).
func (s *SuppressionStore) Get(ctx context.Context, project, stack string) (Suppression, bool, error) {
	rc, _, err := s.Blob.Get(ctx, s.key(project, stack))
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return Suppression{}, false, nil
		}
		return Suppression{}, false, err
	}
	defer rc.Close()
	var sup Suppression
	if err := json.NewDecoder(rc).Decode(&sup); err != nil {
		return Suppression{}, false, err
	}
	return sup, true, nil
}

// Set persists a suppression.
func (s *SuppressionStore) Set(ctx context.Context, sup Suppression) error {
	data, err := json.MarshalIndent(sup, "", "  ")
	if err != nil {
		return err
	}
	_, err = s.Blob.Put(ctx, s.key(sup.Project, sup.Stack), bytes.NewReader(data))
	return err
}

// Clear removes a suppression.
func (s *SuppressionStore) Clear(ctx context.Context, project, stack string) error {
	return s.Blob.Delete(ctx, s.key(project, stack))
}

// List walks the suppressions/ prefix and returns active suppressions.
func (s *SuppressionStore) List(ctx context.Context, now time.Time) ([]Suppression, error) {
	keys, err := s.Blob.List(ctx, "drift/suppressions")
	if err != nil {
		return nil, err
	}
	var out []Suppression
	for _, k := range keys {
		rc, _, err := s.Blob.Get(ctx, k)
		if err != nil {
			continue
		}
		var sup Suppression
		if err := json.NewDecoder(rc).Decode(&sup); err != nil {
			rc.Close()
			continue
		}
		rc.Close()
		if sup.Active(now) {
			out = append(out, sup)
		}
	}
	return out, nil
}
