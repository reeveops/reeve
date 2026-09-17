package run

import "fmt"

// runIdentity returns the durable identity used for artifacts and audit
// records. It uses the full commit SHA so distinct commits cannot share an
// artifact prefix.
func runIdentity(op string, runNumber, runAttempt int, commitSHA string) string {
	if runAttempt > 0 {
		return fmt.Sprintf("%s-%d-%d-%s", op, runNumber, runAttempt, artifactSHA(commitSHA))
	}
	return fmt.Sprintf("%s-%d-%s", op, runNumber, artifactSHA(commitSHA))
}

func artifactSHA(commitSHA string) string {
	if commitSHA == "" {
		return "unknown"
	}
	return commitSHA
}

// lockIdentity stays stable across reruns of one provider run. A retry must
// be able to resume a lock left by its cancelled earlier attempt or by a
// Reeve version that used the legacy short-SHA identity.
func lockIdentity(op string, runNumber int, commitSHA string) string {
	return fmt.Sprintf("%s-%d-%s", op, runNumber, shortSHA(commitSHA))
}
