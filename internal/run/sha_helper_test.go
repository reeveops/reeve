package run

import (
	"context"
	"errors"
	"testing"

	"github.com/FynxLabs/reeve/internal/vcs"
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
		},
		{
			name:     "matching HeadSHA returns sha unchanged",
			vcs:      &fakePRReader{headSHA: "same-sha"},
			prNumber: 1,
			sha:      "same-sha",
			wantSHA:  "same-sha",
		},
		{
			name:     "differing HeadSHA overrides to PR head",
			vcs:      &fakePRReader{headSHA: "pr-head-sha"},
			prNumber: 1,
			sha:      "merge-commit-sha",
			wantSHA:  "pr-head-sha",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolvePRHeadSHA(ctx, tt.vcs, tt.prNumber, tt.sha)
			if got != tt.wantSHA {
				t.Errorf("resolvePRHeadSHA = %q, want %q", got, tt.wantSHA)
			}
		})
	}
}
