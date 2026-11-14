# Design

## Permission resolution

Reeve authenticates with a token that already reads the repository. The REST
collaborator-permission endpoint
(`GET /repos/{owner}/{repo}/collaborators/{username}/permission`) answers in
one request with the fields that matter, per GitHub's REST documentation:

- `user.permissions` - the boolean object `admin`, `maintain`, `push`,
  `triage`, `pull`. **This is what the level mapping reads.** GitHub
  documents (collaborators endpoint) that the permissions hash contains the
  BASE role permissions of the collaborator, and repository custom roles are
  defined based on a predefined base role - so the hash is correct for a
  custom-role holder: it reflects the base role the custom role was built
  on. The mapping is one-to-one: `admin` → admin; `maintain` (with `admin`
  false) → maintain; `push` → write; triage/pull-only or none of the
  flags → below the bar.
- `role_name` - the name of the assigned role, including custom repository
  roles. No longer a level source; recorded verbatim in traces and audit
  records. (The boolean hash is the level field - an earlier draft of this
  design read the level from `role_name`, which refused every custom-role
  holder for having a name the mapping did not recognize.)
- `permission` - the LEGACY base string: `admin`, `write`, `read`, `none`,
  with GitHub documenting that maintain is mapped to write here and triage
  to read. NOT used for the level.

A response the mapping cannot interpret FAILS CLOSED, naming what it saw:
a missing `user.permissions` object, a `null` collaborator, or an object
that cannot be parsed produces a refusal naming exactly that (never a
guessed level). Resolution is keyed on the login and cached per invocation:
one lookup per distinct comment author, reused across the dispatch gate and
the approval source.

What the docs state, quoted: the endpoints' "permissions hash returned in
the response contains the base role permissions of the collaborator"; the
`role_name` is "the highest role assigned to the collaborator after
considering all sources of grants"; the /permission endpoint's
`permission` attribute "provides the legacy base roles of admin, write,
read, and none, where the maintain role is mapped to write". The docs do
not provide a live matrix for every custom role; the hash-reflects-the-base
reading above is from the documented sentence, and real-API confirmation on
a repo with a custom role remains an open verification item (stated in the
PR description).

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
