package notify

import (
	"os"
	"strings"
)

// GitHubServerURL returns the web (HTML) base URL of the GitHub instance,
// honoring GITHUB_SERVER_URL (which the Actions runner sets on github.com
// AND GitHub Enterprise Server). Channels building "owner/repo/pull/N"
// links use this instead of hardcoding github.com so GHES links resolve.
func GitHubServerURL() string {
	if u := strings.TrimRight(strings.TrimSpace(os.Getenv("GITHUB_SERVER_URL")), "/"); u != "" {
		return u
	}
	return "https://github.com"
}
