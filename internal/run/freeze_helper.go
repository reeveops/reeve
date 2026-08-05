package run

import (
	"time"

	"github.com/FynxLabs/reeve/internal/core/freeze"
)

func freezeActiveFor(cfg freeze.Config, ref string, now time.Time) (string, bool, error) {
	return freeze.ActiveFor(cfg, ref, now)
}
