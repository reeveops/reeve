package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	gh "github.com/google/go-github/v66/github"

	"github.com/reeveops/reeve/internal/vcs"
)

// newFakeClient returns a Client whose go-github transport points at the
// given httptest server.
func newFakeClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	g := gh.NewClient(nil)
	u, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	g.BaseURL = u
	return &Client{gh: g, owner: "o", repo: "r"}
}

func checksServer(t *testing.T, checkRuns string, combinedStatus string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/commits/abc1234/check-runs", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, checkRuns)
	})
	mux.HandleFunc("/repos/o/r/commits/abc1234/status", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, combinedStatus)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

const noCheckRuns = `{"total_count":0,"check_runs":[]}`

func TestChecksGreenPendingCommitStatusBlocks(t *testing.T) {
	// The regression: a combined status of "pending" (a status context that
	// has not reported success yet) passed the gate because only
	// failure/error combined states were inspected.
	srv := checksServer(t, noCheckRuns, `{
		"state": "pending",
		"statuses": [
			{"state": "success", "context": "lint"},
			{"state": "pending", "context": "integration-tests"}
		]
	}`)
	c := newFakeClient(t, srv)
	green, failing, err := c.ChecksGreen(context.Background(), "abc1234", vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if green {
		t.Fatal("pending commit status must block ChecksGreen")
	}
	if len(failing) != 1 || !strings.Contains(failing[0], "integration-tests") || !strings.Contains(failing[0], "still running") {
		t.Fatalf("want a 'checks still running' reason naming the pending context, got %v", failing)
	}
}

func TestChecksGreenEmptyPendingStatusPasses(t *testing.T) {
	// GitHub reports state=pending with an empty list when a commit has no
	// statuses at all - that must NOT block. Zero statuses AND zero check
	// runs is now the ZERO-CHECKS CASE: not green unless the operator opts
	// out (a repo where reeve is the only check).
	srv := checksServer(t, noCheckRuns, `{"state": "pending", "statuses": []}`)
	c := newFakeClient(t, srv)
	green, failing, err := c.ChecksGreen(context.Background(), "abc1234", vcs.ChecksGreenOpts{AllowZeroChecks: true})
	if err != nil {
		t.Fatal(err)
	}
	if !green {
		t.Fatalf("opted-out zero checks must pass, got failing=%v", failing)
	}

	// Without the opt-out, zero checks is NOT treated as passing: a silent
	// CI outage, a misconfigured repository, or a repo with no checks at
	// all looked green - the strongest evidence available was nothing.
	green, failing, err = c.ChecksGreen(context.Background(), "abc1234", vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if green {
		t.Fatal("zero checks must not be green by default")
	}
	const refusal = "Required checks: no qualifying check runs found on abc1234. Zero checks is not treated as passing. Wait for CI, or set preconditions.allow_zero_checks if this repo has no other checks."
	if len(failing) != 1 || failing[0] != refusal {
		t.Fatalf("zero-checks denial must carry the full refusal with the SHA and the opt-out key:\nwant: %s\ngot:  %v", refusal, failing)
	}
}

// TestChecksGreenZeroCheckRunsDeniedByDefault pins the empty-repository
// case on its own: an empty check-runs list with an empty status list is
// the zero-checks case, and the refusal names the SHA, the rule, and the
// opt-out key.
func TestChecksGreenZeroCheckRunsDeniedByDefault(t *testing.T) {
	srv := checksServer(t, noCheckRuns, `{"state": "clean", "statuses": []}`)
	c := newFakeClient(t, srv)
	green, failing, err := c.ChecksGreen(context.Background(), "abc1234", vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if green {
		t.Fatal("zero check runs must not be green by default")
	}
	if len(failing) != 1 || !strings.Contains(failing[0], "no qualifying check runs found on abc1234") ||
		!strings.Contains(failing[0], "preconditions.allow_zero_checks") {
		t.Fatalf("refusal must name the SHA and the opt-out key: %v", failing)
	}
}

// TestCheckRunsWithStatusesNotZeroChecks: the legacy combined-status check
// still counts as evidence - zero CHECK RUNS with statuses present takes
// the status path, not the zero-checks refusal.
func TestCheckRunsWithStatusesNotZeroChecks(t *testing.T) {
	srv := checksServer(t, noCheckRuns, `{"state": "success", "statuses": [{"state": "success", "context": "lint"}]}`)
	c := newFakeClient(t, srv)
	green, failing, err := c.ChecksGreen(context.Background(), "abc1234", vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !green {
		t.Fatalf("a passing legacy status is evidence; got failing=%v", failing)
	}
}

func TestChecksGreenFailedCommitStatusBlocks(t *testing.T) {
	srv := checksServer(t, noCheckRuns, `{
		"state": "failure",
		"statuses": [
			{"state": "success", "context": "lint"},
			{"state": "failure", "context": "unit-tests"}
		]
	}`)
	c := newFakeClient(t, srv)
	green, failing, err := c.ChecksGreen(context.Background(), "abc1234", vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if green || len(failing) != 1 || failing[0] != "unit-tests:failure" {
		t.Fatalf("want unit-tests:failure to block, got green=%v failing=%v", green, failing)
	}
}

func TestChecksGreenInProgressCheckRunBlocks(t *testing.T) {
	srv := checksServer(t, `{
		"total_count": 2,
		"check_runs": [
			{"name": "build", "status": "completed", "conclusion": "success"},
			{"name": "e2e", "status": "in_progress"}
		]
	}`, `{"state": "pending", "statuses": []}`)
	c := newFakeClient(t, srv)
	green, failing, err := c.ChecksGreen(context.Background(), "abc1234", vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if green {
		t.Fatal("in-progress check run must block ChecksGreen")
	}
	if len(failing) != 1 || !strings.Contains(failing[0], "e2e") || !strings.Contains(failing[0], "still running") {
		t.Fatalf("want a 'still running' reason naming e2e, got %v", failing)
	}
}

func TestChecksGreenDerivesReusableWorkflowSelfName(t *testing.T) {
	srv := checksServer(t, `{
		"total_count": 5,
		"check_runs": [
			{"name": "infra / Reeve", "status": "completed", "conclusion": "failure", "details_url": "https://github.com/o/r/actions/runs/122/job/1"},
			{"name": "build", "status": "completed", "conclusion": "success", "details_url": "https://github.com/o/r/actions/runs/121/job/2"},
			{"name": "infra / Reeve", "status": "in_progress", "details_url": "https://github.com/o/r/actions/runs/123/job/3"},
			{"name": "deploy", "status": "in_progress", "details_url": "https://github.com/o/r/actions/runs/123/job/4"},
			{"name": "deploy", "status": "completed", "conclusion": "failure", "details_url": "https://github.com/o/r/actions/runs/120/job/5"}
		]
	}`, `{"state": "pending", "statuses": []}`)
	c := newFakeClient(t, srv)
	green, failing, err := c.ChecksGreen(context.Background(), "abc1234", vcs.ChecksGreenOpts{IgnoreRunID: 123})
	if err != nil {
		t.Fatal(err)
	}
	if green || len(failing) != 1 || failing[0] != "deploy:failure" {
		t.Fatalf("only the prior Reeve result should be excluded, got green=%v failing=%v", green, failing)
	}
}

func TestChecksGreenPaginatesCombinedStatuses(t *testing.T) {
	// The failing status context lives on page 2; enumeration must paginate
	// past the first 100 statuses to name it.
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/commits/abc1234/check-runs", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, noCheckRuns)
	})
	var srvURL string
	mux.HandleFunc("/repos/o/r/commits/abc1234/status", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"state":"failure","statuses":[{"state":"failure","context":"page2-check"}]}`)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/commits/abc1234/status?page=2>; rel="next"`, srvURL))
		fmt.Fprint(w, `{"state":"failure","statuses":[{"state":"success","context":"lint"}]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	srvURL = srv.URL

	c := newFakeClient(t, srv)
	green, failing, err := c.ChecksGreen(context.Background(), "abc1234", vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if green {
		t.Fatal("failure on page 2 must block")
	}
	if len(failing) != 1 || failing[0] != "page2-check:failure" {
		t.Fatalf("want the paginated culprit named, got %v", failing)
	}
}

func TestChecksGreenAllGreenPasses(t *testing.T) {
	srv := checksServer(t, `{
		"total_count": 1,
		"check_runs": [{"name": "build", "status": "completed", "conclusion": "success"}]
	}`, `{
		"state": "success",
		"statuses": [{"state": "success", "context": "lint"}]
	}`)
	c := newFakeClient(t, srv)
	green, failing, err := c.ChecksGreen(context.Background(), "abc1234", vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !green {
		t.Fatalf("all green must pass, got failing=%v", failing)
	}
}

// TestChecksGreenSelfRunOnlyIsZeroEvidence pins the filter-first evidence
// count: a repository whose ONLY check run is reeve's own run (current or
// prior) has zero qualifying evidence after the ignore filter - the old
// pre-filter count saw one raw check run and read the commit as green.
func TestChecksGreenSelfRunOnlyIsZeroEvidence(t *testing.T) {
	srv := checksServer(t, `{
		"total_count": 1,
		"check_runs": [
			{"name": "Reeve", "status": "completed", "conclusion": "success",
			 "details_url": "https://github.com/o/r/actions/runs/123/job/456"}
		]
	}`, `{"state": "pending", "statuses": []}`)
	c := newFakeClient(t, srv)

	// The current run's own check (details URL carries the ignored run id).
	opts := vcs.ChecksGreenOpts{IgnoreRunID: 123, IgnoreNames: []string{"Reeve"}}
	green, failing, err := c.ChecksGreen(context.Background(), "abc1234", opts)
	if err != nil {
		t.Fatal(err)
	}
	if green {
		t.Fatal("a self-run-only repository must not read as green")
	}
	const refusal = "Required checks: no qualifying check runs found on abc1234. Zero checks is not treated as passing. Wait for CI, or set preconditions.allow_zero_checks if this repo has no other checks."
	if len(failing) != 1 || failing[0] != refusal {
		t.Fatalf("refusal must name zero qualifying checks and the opt-out:\nwant: %s\ngot:  %v", refusal, failing)
	}

	// The named-self variant without the run-id fragment (a prior run of a
	// reusable caller is ignored by name) is the same zero-evidence case.
	green, failing, err = c.ChecksGreen(context.Background(), "abc1234",
		vcs.ChecksGreenOpts{IgnoreNames: []string{"Reeve"}})
	if err != nil {
		t.Fatal(err)
	}
	if green || len(failing) != 1 || failing[0] != refusal {
		t.Fatalf("name-ignored self run must also be zero evidence: green=%v failing=%v", green, failing)
	}

	// With the opt-out the repository is green as configured.
	green, failing, err = c.ChecksGreen(context.Background(), "abc1234",
		vcs.ChecksGreenOpts{IgnoreRunID: 123, IgnoreNames: []string{"Reeve"}, AllowZeroChecks: true})
	if err != nil {
		t.Fatal(err)
	}
	if !green {
		t.Fatalf("opted-out self-run-only repository must pass: %v", failing)
	}
}
