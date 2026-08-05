package iac

import (
	"fmt"
	"sort"
	"sync"

	"github.com/FynxLabs/reeve/internal/config/schemas"
)

// Constructor builds an engine from its config body.
type Constructor func(cfg schemas.EngineBody) (Engine, error)

var (
	regMu    sync.RWMutex
	registry = map[string]Constructor{}
)

// Register makes an engine type available to New. Engines call it from
// init(); importing an engine package (internal/iac/all imports the default
// set) is what compiles it in. Registering a duplicate type panics.
func Register(typ string, c Constructor) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[typ]; dup {
		panic(fmt.Sprintf("iac: duplicate engine type %q", typ))
	}
	registry[typ] = c
}

// Registered returns the sorted list of registered engine types.
func Registered() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// New resolves cfg to its registered constructor, purely by the engine.type
// string (core never branches on engine identity). An unregistered type is
// an error naming the registered set.
func New(cfg schemas.EngineBody) (Engine, error) {
	regMu.RLock()
	ctor, ok := registry[cfg.Type]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown engine type %q (registered: %v)", cfg.Type, Registered())
	}
	e, err := ctor(cfg)
	if err != nil {
		return nil, fmt.Errorf("engine %s: %w", cfg.Type, err)
	}
	return e, nil
}
