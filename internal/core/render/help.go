package render

import "strings"

const HelpMarker = "<!-- reeve:help -->"

func BuildHelpComment(autoReady bool) string {
	var b strings.Builder
	b.WriteString(HelpMarker + "\n")
	b.WriteString("## reeve commands\n\n")
	b.WriteString("| Command | Description |\n")
	b.WriteString("|---|---|\n")
	b.WriteString("| `/reeve preview` or `/reeve plan` | Re-run plan for this PR |\n")
	b.WriteString("| `/reeve apply` or `/reeve up` | Apply all planned stacks for this PR |\n")
	b.WriteString("| `/reeve apply --force` | Re-apply even if this commit was already applied |\n")
	b.WriteString("| `/reeve apply --refresh` | Reconcile state with live infrastructure, then apply. Turns plan locking off for that run: the change set is computed after the refresh, so it is not the plan reviewed here |\n")
	b.WriteString("| `/reeve refresh [--dry-run] [--all]` | Reconcile state with live infrastructure. Changes no infrastructure - a \"delete\" means the resource was already gone and was dropped from state. `--dry-run` reports without writing; `--all` covers every declared stack, not just this PR's |\n")
	b.WriteString("| `/reeve ready` | Mark PR as ready for approval, notify Slack |\n")
	b.WriteString("| `/reeve breakglass \"<justification>\" apply` | Emergency apply: overrides approvals (and freeze unless disabled); never bypasses locks or checks. Requires `break_glass:` config; loudly audited |\n")
	b.WriteString("| `/reeve unlock [project/stack] [--force]` | Free this PR's stack locks; --force if it holds one mid-apply |\n")
	b.WriteString("| `/reeve help` | Show this help message |\n")
	b.WriteString("\n")
	if autoReady {
		b.WriteString("> `auto_ready` is enabled - when this PR is converted from draft to ready for review and a plan has succeeded, reeve will automatically notify for approval.\n\n")
	}
	b.WriteString("_reeve runs automatically on PR open/push (preview) and on comment commands above._\n")
	return b.String()
}
