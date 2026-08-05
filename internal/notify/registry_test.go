package notify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FynxLabs/reeve/internal/config/schemas"
)

type fakeChannel struct {
	name     string
	events   []Event
	grouping string
	fn       func(ctx context.Context, p Payload) error
}

func (f *fakeChannel) Name() string        { return f.name }
func (f *fakeChannel) Subscribes() []Event { return f.events }

// GroupingMode makes fakeChannel a notify.Grouper. Default "" == none, so
// tests that don't set grouping keep the ungrouped per-stack behavior.
func (f *fakeChannel) GroupingMode() string { return f.grouping }
func (f *fakeChannel) Deliver(ctx context.Context, p Payload) error {
	if f.fn != nil {
		return f.fn(ctx, p)
	}
	return nil
}

func TestRegistryBuildResolvesByType(t *testing.T) {
	Register("test_reg_a", func(_ context.Context, cfg schemas.ChannelYAML, _ Deps) (Channel, error) {
		return &fakeChannel{name: cfg.EffectiveName(), events: ParseEvents(cfg.On)}, nil
	})
	channels, err := Build(context.Background(), []schemas.ChannelYAML{
		{Type: "test_reg_a", Name: "one", On: []string{"applied"}},
		{Type: "test_reg_a"}, // name falls back to type
	}, Deps{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("want 2 channels, got %d", len(channels))
	}
	if channels[0].Name() != "one" || channels[1].Name() != "test_reg_a" {
		t.Fatalf("names: %q %q", channels[0].Name(), channels[1].Name())
	}
	if got := channels[0].Subscribes(); len(got) != 1 || got[0] != EventApplied {
		t.Fatalf("subscriptions: %v", got)
	}
}

func TestRegistryUnknownTypeErrors(t *testing.T) {
	_, err := Build(context.Background(), []schemas.ChannelYAML{{Type: "no_such_channel"}}, Deps{})
	if err == nil || !strings.Contains(err.Error(), `unknown notification channel type "no_such_channel"`) {
		t.Fatalf("want unknown-type error, got %v", err)
	}
}

func TestRegistrySkipsDisabledAndNilChannels(t *testing.T) {
	off := false
	Register("test_reg_skip", func(context.Context, schemas.ChannelYAML, Deps) (Channel, error) {
		return nil, nil // unmet optional dependency
	})
	channels, err := Build(context.Background(), []schemas.ChannelYAML{
		{Type: "test_reg_skip"},
		{Type: "no_such_channel", Enabled: &off}, // disabled: type not even resolved
	}, Deps{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("want 0 channels, got %d", len(channels))
	}
}

func TestRegistryConstructorErrorIsLabeled(t *testing.T) {
	boom := errors.New("boom")
	Register("test_reg_err", func(context.Context, schemas.ChannelYAML, Deps) (Channel, error) {
		return nil, boom
	})
	_, err := Build(context.Background(), []schemas.ChannelYAML{{Type: "test_reg_err", Name: "mine"}}, Deps{})
	if err == nil || !errors.Is(err, boom) || !strings.Contains(err.Error(), "mine") {
		t.Fatalf("want labeled constructor error, got %v", err)
	}
}

func TestParseEventsDropsUnknown(t *testing.T) {
	got := ParseEvents([]string{"applied", "bogus", "drift_detected"})
	if len(got) != 2 || got[0] != EventApplied || got[1] != EventDriftDetected {
		t.Fatalf("ParseEvents: %v", got)
	}
}

func TestEventNamesMatchSchemas(t *testing.T) {
	// TimelinePREvents is the full PR-flow set (core lifecycle + planning +
	// break_glass); with the drift events it must cover schemas exactly.
	all := append(TimelinePREvents(), DriftEvents()...)
	if len(all) != len(schemas.ValidChannelEvents) {
		t.Fatalf("event count mismatch: notify %d vs schemas %d", len(all), len(schemas.ValidChannelEvents))
	}
	for _, e := range all {
		if !schemas.IsValidChannelEvent(string(e)) {
			t.Fatalf("event %q missing from schemas.ValidChannelEvents", e)
		}
	}
}

func TestTimelinePREventsSupersetOfCore(t *testing.T) {
	// The legacy default set must stay exactly the core lifecycle: widening
	// it would silently change existing channels' subscriptions.
	core := PREvents()
	if core[0] != EventPlan || len(core) != 7 {
		t.Fatalf("core lifecycle changed: %v", core)
	}
	tl := TimelinePREvents()
	if tl[0] != EventPlanning || tl[len(tl)-1] != EventBreakGlass || len(tl) != len(core)+2 {
		t.Fatalf("timeline events: %v", tl)
	}
}
