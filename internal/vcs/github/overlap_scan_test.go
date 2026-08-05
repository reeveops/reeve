package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/core/approvals"
)

func TestListOpenPRsTouchingPathsPartialFailure(t *testing.T) {
	// PR 1's file listing fails; PR 2 overlaps. The failed PR must surface
	// in an OverlapScanError, never silently read as "no overlap".
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[
			{"number": 1, "head": {"sha": "aaa"}, "user": {"login": "alice"}, "base": {"ref": "main"}},
			{"number": 2, "head": {"sha": "bbb"}, "user": {"login": "bob"}, "base": {"ref": "main"}}
		]`)
	})
	mux.HandleFunc("/repos/o/r/pulls/1/files", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	mux.HandleFunc("/repos/o/r/pulls/2/files", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"filename": "projects/api/main.go"}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := newFakeClient(t, srv)

	prs, err := c.ListOpenPRsTouchingPaths(context.Background(), []string{"projects/api"})
	if len(prs) != 1 || prs[0].Number != 2 {
		t.Fatalf("want partial result [PR 2], got %+v", prs)
	}
	var ose *approvals.OverlapScanError
	if !errors.As(err, &ose) {
		t.Fatalf("want OverlapScanError, got %v", err)
	}
	if len(ose.Unchecked) != 1 || ose.Unchecked[0] != 1 {
		t.Fatalf("want unchecked [1], got %v", ose.Unchecked)
	}
	if !strings.Contains(ose.Error(), "#1") {
		t.Fatalf("error must name the unchecked PR: %v", ose)
	}
}

func TestListOpenPRsTouchingPathsAllHealthy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"number": 3, "head": {"sha": "ccc"}, "user": {"login": "carol"}, "base": {"ref": "main"}}]`)
	})
	mux.HandleFunc("/repos/o/r/pulls/3/files", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[{"filename": "unrelated/file.txt"}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := newFakeClient(t, srv)

	prs, err := c.ListOpenPRsTouchingPaths(context.Background(), []string{"projects/api"})
	if err != nil {
		t.Fatalf("healthy scan must not error: %v", err)
	}
	if len(prs) != 0 {
		t.Fatalf("no overlap expected, got %+v", prs)
	}
}
