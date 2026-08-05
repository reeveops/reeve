package run

import (
	"testing"

	"github.com/FynxLabs/reeve/internal/config/schemas"
)

func TestApprovalsDismissOnNewCommitDefault(t *testing.T) {
	yes, no := true, false

	// Unset in YAML → secure default of true.
	s := &schemas.Shared{}
	if got := toApprovalsConfig(s).Default.DismissOnNewCommit; !got {
		t.Fatalf("dismiss_on_new_commit unset should default to true, got %v", got)
	}

	// Explicitly false → honored, not overridden.
	s.Approvals.Default.DismissOnNewCommit = &no
	if got := toApprovalsConfig(s).Default.DismissOnNewCommit; got {
		t.Fatalf("explicit dismiss_on_new_commit=false must be honored, got %v", got)
	}

	// Explicitly true → true.
	s.Approvals.Default.DismissOnNewCommit = &yes
	if got := toApprovalsConfig(s).Default.DismissOnNewCommit; !got {
		t.Fatalf("explicit dismiss_on_new_commit=true, got %v", got)
	}
}
