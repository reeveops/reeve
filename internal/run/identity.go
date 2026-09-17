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
