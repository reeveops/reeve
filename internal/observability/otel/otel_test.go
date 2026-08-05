package otel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// collector is a minimal OTLP/HTTP endpoint that records which signal
// paths were posted to.
type collector struct {
	mu    sync.Mutex
	paths []string
}

func (c *collector) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.paths = append(c.paths, r.URL.Path)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
}

func (c *collector) sawPath(p string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, got := range c.paths {
		if got == p {
			return true
		}
	}
	return false
}

func TestNewEmitsToConfiguredEndpointAndShutsDown(t *testing.T) {
	col := &collector{}
	srv := httptest.NewServer(col.handler())
	defer srv.Close()

	ctx := context.Background()
	p, err := New(ctx, Options{
		Endpoint:         srv.URL,
		ServiceName:      "reeve-test",
		StackCardinality: CardinalityAllow,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Emit one of everything so both signals have data to flush.
	runCtx, endRun := p.StartRunSpan(ctx, "plan", 7, "0000000")
	stackCtx, endStack := p.StartStackSpan(runCtx, "proj", "dev", "dev", "plan")
	p.RecordPreconditionFailure(stackCtx, "approvals")
	p.RecordPolicyViolation(stackCtx, "no-deletes")
	p.RecordStackChanges(stackCtx, "proj", "dev", 1, 2, 0, 0)
	p.RecordDriftDetection(stackCtx, "proj", "dev", "dev", "drifted")
	p.RecordDriftDuration(stackCtx, "proj", "dev", "dev", 1.5)
	p.RecordDriftRun(stackCtx, "ok")
	p.RecordStacksInDrift(stackCtx, "dev", 3)
	p.RecordOngoingDuration(stackCtx, "proj", "dev", 4.5)
	endStack("ok", 1.25)
	endRun("ok")

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if !col.sawPath("/v1/traces") {
		t.Errorf("no trace export reached the endpoint; got %v", col.paths)
	}
	if !col.sawPath("/v1/metrics") {
		t.Errorf("no metric export reached the endpoint; got %v", col.paths)
	}
}

func TestNewExpandsEnvInEndpointAndHeaders(t *testing.T) {
	col := &collector{}
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		col.handler().ServeHTTP(w, r)
	}))
	defer srv.Close()

	t.Setenv("REEVE_TEST_OTLP_ENDPOINT", srv.URL)
	t.Setenv("REEVE_TEST_OTLP_KEY", "example-key")

	ctx := context.Background()
	p, err := New(ctx, Options{
		Endpoint: "${env:REEVE_TEST_OTLP_ENDPOINT}",
		Headers:  map[string]string{"X-Api-Key": "${env:REEVE_TEST_OTLP_KEY}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, end := p.StartRunSpan(ctx, "plan", 1, "sha")
	end("ok")
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if !col.sawPath("/v1/traces") {
		t.Fatalf("endpoint env expansion failed; paths %v", col.paths)
	}
	if gotHeader != "example-key" {
		t.Errorf("header env expansion failed: %q", gotHeader)
	}
}

// TestNilProviderIsNoOp: disabled observability hands a nil *Provider to
// every helper; all of them must be zero-cost no-ops.
func TestNilProviderIsNoOp(t *testing.T) {
	var p *Provider
	ctx := context.Background()

	if err := p.Shutdown(ctx); err != nil {
		t.Errorf("nil Shutdown = %v", err)
	}
	gotCtx, endRun := p.StartRunSpan(ctx, "plan", 1, "sha")
	if gotCtx != ctx {
		t.Error("nil StartRunSpan must return the caller's context")
	}
	endRun("ok")
	gotCtx, endStack := p.StartStackSpan(ctx, "p", "s", "e", "plan")
	if gotCtx != ctx {
		t.Error("nil StartStackSpan must return the caller's context")
	}
	endStack("ok", 1)
	p.RecordPreconditionFailure(ctx, "g")
	p.RecordPolicyViolation(ctx, "p")
	p.RecordStackChanges(ctx, "p", "s", 1, 1, 1, 1)
	p.RecordDriftDetection(ctx, "p", "s", "e", "ok")
	p.RecordDriftDuration(ctx, "p", "s", "e", 1)
	p.RecordDriftRun(ctx, "ok")
	p.RecordStacksInDrift(ctx, "e", 1)
	p.RecordOngoingDuration(ctx, "p", "s", 1)
}

func TestStackLabelCardinality(t *testing.T) {
	cases := []struct {
		name string
		mode CardinalityMode
		want func(t *testing.T, got string)
	}{
		{"allow keeps full name", CardinalityAllow, func(t *testing.T, got string) {
			if got != "proj/dev" {
				t.Errorf("got %q, want proj/dev", got)
			}
		}},
		{"drop yields empty", CardinalityDrop, func(t *testing.T, got string) {
			if got != "" {
				t.Errorf("got %q, want empty", got)
			}
		}},
		{"hash yields stable 16-hex digest", CardinalityHash, func(t *testing.T, got string) {
			if len(got) != 16 {
				t.Fatalf("got %q, want 16 hex chars", got)
			}
			if got == "proj/dev" {
				t.Error("hash mode must not expose the raw name")
			}
			again := (&Provider{cardinality: CardinalityHash}).stackLabel("proj", "dev")
			if got != again {
				t.Error("hash must be deterministic")
			}
		}},
		{"unknown mode defaults to hash", CardinalityMode("bogus"), func(t *testing.T, got string) {
			hashed := (&Provider{cardinality: CardinalityHash}).stackLabel("proj", "dev")
			if got != hashed {
				t.Errorf("unknown mode = %q, want the hash %q", got, hashed)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Provider{cardinality: tc.mode}
			tc.want(t, p.stackLabel("proj", "dev"))
		})
	}
}
