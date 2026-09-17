package run

import "fmt"

// runIdentity returns the durable identity used for artifacts, locks, and
// audit records. CI providers can rerun one run number, so a positive attempt
// is part of the identity; direct callers without that concept retain the
// legacy shape.
func runIdentity(op string, runNumber, runAttempt int, commitSHA string) string {
	if runAttempt > 0 {
		return fmt.Sprintf("%s-%d-%d-%s", op, runNumber, runAttempt, shortSHA(commitSHA))
	}
	return fmt.Sprintf("%s-%d-%s", op, runNumber, shortSHA(commitSHA))
}

// lockIdentity stays stable across reruns of one provider run. A retry must
// be able to resume a lock left by its cancelled earlier attempt.
func lockIdentity(op string, runNumber int, commitSHA string) string {
	return runIdentity(op, runNumber, 0, commitSHA)
}
