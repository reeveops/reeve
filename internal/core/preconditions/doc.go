// Package preconditions evaluates apply gates in order:
// up-to-date, checks green, preview fresh, preview succeeded, policy passed,
// approvals satisfied, lock acquirable, not in freeze window. Fail-fast,
// returning a structured trace the PR comment renderer can display.
package preconditions
