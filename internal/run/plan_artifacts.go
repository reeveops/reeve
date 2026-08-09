package run

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/reeveops/reeve/internal/blob"
	"github.com/reeveops/reeve/internal/config/schemas"
	"github.com/reeveops/reeve/internal/iac"
)

// maxPlanArtifactBytes caps what reeve will carry from preview to apply. A
// plan this large is a repo-shaped problem, not a reeve one, and silently
// streaming hundreds of megabytes per stack into the operator's bucket on
// every preview is worse than falling back to a re-plan at apply time.
const maxPlanArtifactBytes = 64 << 20 // 64 MiB

// PlanLockingEnabled reports whether apply should execute the plan artifact
// its preview saved rather than compute a fresh change set. Default on; a
// nil engine config counts as on so the safe behavior does not depend on
// config being present.
//
// This answers only "was it asked for". Whether it can happen also needs
// EngineSupportsSavedPlans - config states intent, the adapter states
// ability.
func PlanLockingEnabled(e *schemas.Engine) bool {
	if e == nil {
		return true
	}
	return e.Engine.PlanLockingEnabled()
}

// EngineSupportsSavedPlans reports the engine's SupportsSavedPlans
// capability. The run pipeline holds engines through narrow interfaces
// (run.Engine is enumerate+preview+name), so the capability is read through
// an assertion rather than by widening those interfaces for one feature.
// An engine that does not expose Capabilities is treated as unable, which
// degrades to today's re-plan-at-apply behavior.
func EngineSupportsSavedPlans(e any) bool {
	c, ok := e.(interface{ Capabilities() iac.Capabilities })
	return ok && c.Capabilities().SupportsSavedPlans
}

// PlanArtifactKey is where one stack's saved plan lives for a run. It sits
// under the run's own prefix next to manifest.json, so the age-based
// retention sweep in gc.go prunes plans with the run that produced them -
// no separate lifecycle to forget about.
func PlanArtifactKey(pr int, runID, stackRef string) string {
	if pr == 0 {
		return fmt.Sprintf("runs/local/%s/plans/%s.plan", runID, blob.SlugComponent(stackRef, "stack"))
	}
	return fmt.Sprintf("runs/pr-%d/%s/plans/%s.plan", pr, runID, blob.SlugComponent(stackRef, "stack"))
}

// PutPlanArtifact uploads the engine's plan file and returns its blob key.
//
// The artifact is the engine's own binary/opaque plan, NOT the redacted
// summary stored in the manifest: it necessarily contains the resource
// attribute values the plan would write, so it inherits the sensitivity of
// the state backend it came from. It lands in the operator's own bucket
// under the run prefix and ages out with the run.
func PutPlanArtifact(ctx context.Context, store blob.Store, pr int, runID, stackRef, path string) (string, error) {
	if store == nil || path == "" {
		return "", nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if fi.Size() == 0 {
		return "", fmt.Errorf("plan artifact %s is empty", path)
	}
	if fi.Size() > maxPlanArtifactBytes {
		return "", fmt.Errorf("plan artifact is %d bytes, over the %d byte limit", fi.Size(), maxPlanArtifactBytes)
	}
	// #nosec G304 -- path is the plan file this process just asked the engine
	// to write into a temp file it created; it is never user-supplied
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	key := PlanArtifactKey(pr, runID, stackRef)
	if _, err := store.Put(ctx, key, f); err != nil {
		return "", err
	}
	return key, nil
}

// FetchPlanArtifact downloads a saved plan to a temp file for the engine to
// consume, returning its path and a cleanup the caller must always run.
//
// The file is created 0600 and removed by cleanup: a plan on a CI runner's
// disk is readable by anything else sharing that runner, so its lifetime is
// scoped to the single apply that needs it.
func FetchPlanArtifact(ctx context.Context, store blob.Store, key string) (path string, cleanup func(), err error) {
	noop := func() {}
	if store == nil || key == "" {
		return "", noop, nil
	}
	rc, _, err := store.Get(ctx, key)
	if err != nil {
		return "", noop, err
	}
	defer rc.Close()

	f, err := os.CreateTemp("", "reeve-locked-plan-*")
	if err != nil {
		return "", noop, err
	}
	name := f.Name()
	cleanup = func() { _ = os.Remove(name) }
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		cleanup()
		return "", noop, err
	}
	// LimitReader bounds a hostile or corrupt object: the +1 lets an
	// over-limit body be detected rather than silently truncated into a
	// plan file the engine would then reject with a confusing parse error.
	n, err := io.Copy(f, io.LimitReader(rc, maxPlanArtifactBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return "", noop, err
	}
	if n > maxPlanArtifactBytes {
		cleanup()
		return "", noop, fmt.Errorf("saved plan %s exceeds the %d byte limit", key, maxPlanArtifactBytes)
	}
	if n == 0 {
		cleanup()
		return "", noop, fmt.Errorf("saved plan %s is empty", key)
	}
	return name, cleanup, nil
}
