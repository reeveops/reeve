# Design

## Permission resolution

Reeve authenticates with a token that already reads the repository. The REST
collaborator-permission endpoint
(`GET /repos/{owner}/{repo}/collaborators/{username}/permission`) reports the
actor's effective permission (`admin`, `maintain`, `write`, `read`, `none`)
in one request. Resolution is keyed on the login and cached per invocation:
one lookup per distinct comment author, reused across the dispatch gate and
the approval source.

Failure modes, fail-closed:

- The permission API errors or is unavailable → the actor is treated as
  below the required level. The denial says which check failed.
- The actor is unknown to the relation (404) → permission `none`. There is
  no actor-level bypass for bot accounts: the classifier's self-trigger
  guard already refuses bot dispatch before permission matters.
- `pull_request_review` dispatch resolves the review author the same way.

## Ordering with existing gates

Association still filters first: an actor outside `allowed-associations` is
denied exactly as today (same message, no permission lookup). The permission
check runs second, so `allowed-associations` keeps its documented
semantics as a coarse prefilter and repos that never set it keep their
defaults (`OWNER`, `MEMBER`, `COLLABORATOR`). A denial after a passing
association names both facts: the association that passed and the permission
that did not.

`required-permission` accepts `write` (default), `maintain`, or `admin` -
naming the minimum level that grants. Levels below `write` are rejected at
action-input validation: gating `reeve apply` on a permission that cannot
reach the locked resources would re-instate the proxy problem this change
removes.

## The pr_comment approval source

`ListCommentApprovals` applies the same two-stage gate per comment author:
association allowlist (unchanged), then the permission bar. A comment whose
author fails the permission bar is refused with the same trace style the
source already uses ("ignored ... reason ... next step") rather than
dropped silently.

## Trust boundary note

The permission lookup uses the action's token against the source repository;
it adds no credentials, no new sinks, and no caller-controlled inputs beyond
the `required-permission` enum, which must be one of three exact strings.
A malicious PR cannot influence the check: it happens before any workload
checkout or engine process.
