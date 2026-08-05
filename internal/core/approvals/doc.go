// Package approvals resolves approval rules against PR reviews and CODEOWNERS.
// Pure: each consumer defines the minimal ApprovalSource / reviewLister /
// teamResolver interfaces it needs. Rules merge layered: per-stack
// overrides on top of the default rule set.
package approvals
