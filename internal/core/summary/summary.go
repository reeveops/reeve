package summary

// Counts is the per-stack change count tuple.
type Counts struct {
	Add     int
	Change  int
	Delete  int
	Replace int
}

// Total is the sum across all fields - handy for "no-op" detection
// (Total() == 0).
func (c Counts) Total() int { return c.Add + c.Change + c.Delete + c.Replace }

// Status tracks the lifecycle of a per-stack preview entry in the PR
// comment.
type Status string

const (
	StatusPlanned Status = "planned"
	StatusNoOp    Status = "noop"
	StatusBlocked Status = "blocked"
	StatusError   Status = "error"
)

// StackSummary is what the render package consumes to build the preview
// comment. Populated by the run/preview pipeline.
type StackSummary struct {
	Project           string
	Stack             string
	Env               string
	Counts            Counts
	Status            Status
	BlockedBy         int         // PR number, 0 if none
	Error             string      // non-empty if Status == StatusError
	FullPlan          string      // raw JSON preview output (redacted)
	PlanSummary       string      // human-readable short summary (+/-/~/± per resource)
	PlanDiff          string      // pulumi preview --diff output (redacted)
	DurationMS        int64       // apply duration (preview may be 0)
	Gates             []GateTrace // rendered as "🔐 apply gates" section
	RequiredApprovers []string    // approvers required before apply
	// PlanKey is the blob key of the engine plan artifact this preview
	// saved (plan locking). Empty means no artifact was stored - either
	// locking is off, the engine cannot save plans, or the upload failed -
	// and apply will re-plan instead of executing this exact change set.
	PlanKey string
}

// GateTrace is one line of the per-stack "apply gates" trace.
type GateTrace struct {
	Gate    string
	Outcome string // "pass" | "fail" | "warn" | "skipped"
	Reason  string
}

// Ref returns "project/stack".
func (s StackSummary) Ref() string { return s.Project + "/" + s.Stack }
