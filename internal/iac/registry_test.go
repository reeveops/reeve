package iac

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/core/discovery"
)

// fakeEngine is a minimal full-contract engine for registry tests.
type fakeEngine struct {
	name   string
	binary string
}

func (f *fakeEngine) Name() string               { return f.name }
func (f *fakeEngine) Capabilities() Capabilities { return Capabilities{} }
func (f *fakeEngine) EnumerateStacks(context.Context, string) ([]discovery.Stack, error) {
	return nil, nil
}
func (f *fakeEngine) Preview(context.Context, discovery.Stack, PreviewOpts) (PreviewResult, error) {
	return PreviewResult{}, nil
}
func (f *fakeEngine) Apply(context.Context, discovery.Stack, ApplyOpts) (ApplyResult, error) {
	return ApplyResult{}, nil
}
func (f *fakeEngine) DriftCheck(context.Context, discovery.Stack, PreviewOpts, bool) (PreviewResult, error) {
	return PreviewResult{}, nil
}
func (f *fakeEngine) Refresh(context.Context, discovery.Stack, RefreshOpts) (RefreshResult, error) {
	return RefreshResult{}, nil
}

func TestRegisterAndNew(t *testing.T) {
	Register("test-fake", func(cfg schemas.EngineBody) (Engine, error) {
		return &fakeEngine{name: "test-fake", binary: cfg.Binary.Path}, nil
	})

	e, err := New(schemas.EngineBody{
		Type:   "test-fake",
		Binary: schemas.EngineBinary{Path: "/opt/fake"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fe, ok := e.(*fakeEngine)
	if !ok {
		t.Fatalf("New returned %T, want *fakeEngine", e)
	}
	if fe.binary != "/opt/fake" {
		t.Errorf("constructor did not receive config: binary = %q, want /opt/fake", fe.binary)
	}

	found := false
	for _, typ := range Registered() {
		if typ == "test-fake" {
			found = true
		}
	}
	if !found {
		t.Errorf("Registered() = %v, missing test-fake", Registered())
	}
}

func TestNewUnknownType(t *testing.T) {
	Register("test-known", func(cfg schemas.EngineBody) (Engine, error) {
		return &fakeEngine{name: "test-known"}, nil
	})

	_, err := New(schemas.EngineBody{Type: "no-such-engine"})
	if err == nil {
		t.Fatal("New with unknown type: want error, got nil")
	}
	if !strings.Contains(err.Error(), `"no-such-engine"`) {
		t.Errorf("error %q does not name the unknown type", err)
	}
	if !strings.Contains(err.Error(), "test-known") {
		t.Errorf("error %q does not list registered engines", err)
	}
}

func TestNewConstructorError(t *testing.T) {
	boom := errors.New("boom")
	Register("test-broken", func(cfg schemas.EngineBody) (Engine, error) {
		return nil, boom
	})

	_, err := New(schemas.EngineBody{Type: "test-broken"})
	if !errors.Is(err, boom) {
		t.Fatalf("New: err = %v, want wrapped boom", err)
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	ctor := func(cfg schemas.EngineBody) (Engine, error) { return &fakeEngine{}, nil }
	Register("test-dup", ctor)
	defer func() {
		if recover() == nil {
			t.Error("duplicate Register did not panic")
		}
	}()
	Register("test-dup", ctor)
}
