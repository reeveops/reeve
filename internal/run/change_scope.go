package run

import "github.com/reeveops/reeve/internal/core/discovery"

func scopeChangedFiles(files []string, repoPath string) (scoped []string, allOutside bool) {
	scoped = discovery.ScopeChangedFiles(files, repoPath)
	return scoped, repoPath != "" && repoPath != "." && len(files) > 0 && len(scoped) == 0
}
