package run

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/vcs"
)

type fakePRReader struct {
	headSHA string
	err     error
}

func (f *fakePRReader) GetPR(_ context.Context, _ int) (*vcs.PR, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &vcs.PR{HeadSHA: f.headSHA}, nil
}

func TestResolvePRHeadSHA(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const expected = "1111111111111111111111111111111111111111"

	tests := []struct {
		name         string
		vcs          prHeadReader
		prNumber     int
		sha          string
		expectedHead string
		wantSHA      string
		wantPR       bool
		wantErr      error
	}{
		{
			name:     "nil vcs returns sha unchanged",
			vcs:      nil,
			prNumber: 1,
			sha:      "env-sha",
			wantSHA:  "env-sha",
		},
		{
			name:     "zero prNumber returns sha unchanged",
			vcs:      &fakePRReader{headSHA: "head-sha"},
			prNumber: 0,
			sha:      "env-sha",
			wantSHA:  "env-sha",
		},
		{
			name:     "GetPR error returns sha unchanged",
			vcs:      &fakePRReader{err: errors.New("api down")},
			prNumber: 1,
			sha:      "env-sha",
			wantSHA:  "env-sha",
		},
		{
			name:     "empty HeadSHA returns sha unchanged",
			vcs:      &fakePRReader{headSHA: ""},
			prNumber: 1,
			sha:      "env-sha",
			wantSHA:  "env-sha",
			wantPR:   true,
		},
		{
			name:     "matching HeadSHA returns sha unchanged",
			vcs:      &fakePRReader{headSHA: "same-sha"},
			prNumber: 1,
			sha:      "same-sha",
			wantSHA:  "same-sha",
			wantPR:   true,
		},
		{
			name:     "differing HeadSHA overrides to PR head",
			vcs:      &fakePRReader{headSHA: "pr-head-sha"},
			prNumber: 1,
			sha:      "merge-commit-sha",
			wantSHA:  "pr-head-sha",
			wantPR:   true,
		},
		{
			name:         "expected head binds matching snapshot",
			vcs:          &fakePRReader{headSHA: expected},
			prNumber:     1,
			sha:          "merge-commit-sha",
			expectedHead: expected,
			wantSHA:      expected,
			wantPR:       true,
		},
		{
			name:         "expected head rejects moved PR",
			vcs:          &fakePRReader{headSHA: "2222222222222222222222222222222222222222"},
			prNumber:     1,
			sha:          expected,
			expectedHead: expected,
			wantErr:      ErrPRHeadMismatch,
		},
		{
			name:         "expected head fails closed on API error",
			vcs:          &fakePRReader{err: errors.New("api down")},
			prNumber:     1,
			sha:          expected,
			expectedHead: expected,
			wantErr:      errors.New("api down"),
		},
		{
			name:         "invalid expected head is rejected",
			vcs:          &fakePRReader{headSHA: expected},
			prNumber:     1,
			sha:          expected,
			expectedHead: "main",
			wantErr:      ErrExpectedPRHeadInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, pr, err := resolvePR(ctx, tt.vcs, tt.prNumber, tt.sha, tt.expectedHead)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("resolvePR error = nil, want %v", tt.wantErr)
				}
				if errors.Is(tt.wantErr, ErrPRHeadMismatch) || errors.Is(tt.wantErr, ErrExpectedPRHeadInvalid) {
					if !errors.Is(err, tt.wantErr) {
						t.Fatalf("resolvePR error = %v, want %v", err, tt.wantErr)
					}
				} else if !errors.Is(err, tt.wantErr) && !strings.Contains(err.Error(), tt.wantErr.Error()) {
					t.Fatalf("resolvePR error = %v, want wrapped %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.wantSHA {
				t.Errorf("resolvePR SHA = %q, want %q", got, tt.wantSHA)
			}
			if (pr != nil) != tt.wantPR {
				t.Errorf("resolvePR metadata present = %v, want %v", pr != nil, tt.wantPR)
			}
		})
	}
}

func TestRevalidatePRHead(t *testing.T) {
	t.Parallel()

	const expected = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tests := []struct {
		name    string
		vcs     prHeadReader
		head    string
		wantErr error
	}{
		{name: "matching live head", vcs: &fakePRReader{headSHA: expected}, head: expected},
		{name: "moved live head", vcs: &fakePRReader{headSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, head: expected, wantErr: ErrPRHeadMismatch},
		{name: "API failure", vcs: &fakePRReader{err: errors.New("api down")}, head: expected, wantErr: errors.New("api down")},
		{name: "no expected head", vcs: &fakePRReader{err: errors.New("must not be called")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := revalidatePRHead(context.Background(), tt.vcs, 7, tt.head)
			switch {
			case tt.wantErr == nil && err != nil:
				t.Fatal(err)
			case tt.wantErr != nil && err == nil:
				t.Fatalf("revalidatePRHead error = nil, want %v", tt.wantErr)
			case errors.Is(tt.wantErr, ErrPRHeadMismatch) && !errors.Is(err, ErrPRHeadMismatch):
				t.Fatalf("revalidatePRHead error = %v, want %v", err, ErrPRHeadMismatch)
			case tt.wantErr != nil && !errors.Is(tt.wantErr, ErrPRHeadMismatch) && !strings.Contains(err.Error(), tt.wantErr.Error()):
				t.Fatalf("revalidatePRHead error = %v, want wrapped %v", err, tt.wantErr)
			}
		})
	}
}
