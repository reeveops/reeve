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

	"github.com/FynxLabs/reeve/internal/vcs"
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
	// statuses at all - that must NOT block.
	srv := checksServer(t, noCheckRuns, `{"state": "pending", "statuses": []}`)
	c := newFakeClient(t, srv)
	green, failing, err := c.ChecksGreen(context.Background(), "abc1234", vcs.ChecksGreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !green {
		t.Fatalf("no statuses at all must pass, got failing=%v", failing)
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
