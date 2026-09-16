package run

import (
	"context"
	"errors"
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
	ctx := context.Background()

	tests := []struct {
		name     string
		vcs      prHeadReader
		prNumber int
		sha      string
		wantSHA  string
		wantPR   bool
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, pr := resolvePR(ctx, tt.vcs, tt.prNumber, tt.sha)
			if got != tt.wantSHA {
				t.Errorf("resolvePR SHA = %q, want %q", got, tt.wantSHA)
			}
			if (pr != nil) != tt.wantPR {
				t.Errorf("resolvePR metadata present = %v, want %v", pr != nil, tt.wantPR)
			}
		})
	}
}
