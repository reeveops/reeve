package breakglass

import (
	"errors"
	"fmt"
	"strings"
)

// Source names, recorded in the audit entry as the source that granted.
const (
	SourceInternalList = "internal_list"
	SourceCodeowners   = "codeowners"
	SourceAnyone       = "anyone"
	SourceVCSBypass    = "vcs_bypass"
)

// ErrNotConfigured is returned when break-glass is invoked but no
// break_glass block exists in shared.yaml. The command surface stays off
// unless explicitly configured.
var ErrNotConfigured = errors.New(
	"break-glass is not configured for this repository: add a `break_glass:` block with `authorized:` sources to .reeve/shared.yaml to enable emergency applies")

// Config is the resolved break_glass block from shared.yaml (as of the PR
// HEAD - authorization is head-resolved by design).
type Config struct {
	// Configured is true only when a break_glass block was present.
	Configured bool

	// InternalList holds explicit logins and "org/team" slugs.
	InternalList []string
	// Codeowners grants anyone CODEOWNERS makes an owner of a changed path.
	Codeowners bool
	// Anyone grants any actor (still audited, still justification-gated).
	Anyone bool
	// VCSBypass would grant GitHub ruleset bypass actors. Not yet
	// supported: configuring it is a hard error at authorization time.
	VCSBypass bool
	// Groups holds phase-2 external identity group sources
	// ("<provider>:<name>" or "group:<provider>:<name>"). Parsed and
	// rejected with a clear error until phase 2 lands.
	Groups []string

	// OverrideFreeze: break-glass also overrides freeze windows. Defaults
	// to true when the block is configured but the key omitted.
	OverrideFreeze bool

	// RejectSelfAuthorization, when true, denies break-glass on any PR that
	// modifies its own authorizing files (a .reeve config or CODEOWNERS).
	// Authorization is otherwise head-resolved by design (an emergency
	// responder may add themselves); this opt-in trades that availability
	// for a hard fail-closed against same-PR self-authorization. Default
	// false preserves the flag-and-audit behavior.
	RejectSelfAuthorization bool
}

// HasSource reports whether at least one authorization source is configured.
func (c Config) HasSource() bool {
	return len(c.InternalList) > 0 || c.Codeowners || c.Anyone || c.VCSBypass || len(c.Groups) > 0
}

// Inputs is everything the authorization decision needs, resolved by the
// caller at the edges (VCS fetches, team expansion).
type Inputs struct {
	// Actor is the login invoking break-glass (no leading @).
	Actor string
	// OwnedPaths is changed-path → owners from CODEOWNERS resolution
	// (owners keep their leading @, as written in the file).
	OwnedPaths map[string][]string
	// TeamMembers maps "org/team" slugs to member logins, pre-expanded by
	// the caller. Missing slugs fail closed (never match).
	TeamMembers map[string][]string
	// AuthorizingPathsTouched lists the authorizing files (.reeve config or
	// CODEOWNERS) this PR modifies, as computed by AuthorizingPathsTouched.
	// Consumed only when Config.RejectSelfAuthorization is set; otherwise
	// the caller still records it in the audit trail.
	AuthorizingPathsTouched []string
}

// Decision is the authorization outcome. Trace explains every source
// consulted so a denial is diagnosable from the run output alone.
type Decision struct {
	Authorized bool
	// Source is the first source that granted (SourceInternalList,
	// SourceCodeowners, or SourceAnyone).
	Source string
	Trace  []string
}

// Authorize decides whether actor may break-glass apply. Pure. Union
// semantics: any configured source granting is enough; evaluation order is
// internal_list, codeowners, anyone (deterministic, most-specific first, so
// the audit record names the narrowest grant).
//
// Fail-closed rules:
//   - unconfigured → ErrNotConfigured
//   - configured with no sources → error
//   - vcs_bypass or groups configured → hard error (not yet supported);
//     they are rejected even when another source would match, so an
//     operator immediately learns the source is inert rather than
//     believing it is active.
//   - empty actor → denied with trace
func Authorize(cfg Config, in Inputs) (Decision, error) {
	if !cfg.Configured {
		return Decision{}, ErrNotConfigured
	}
	if !cfg.HasSource() {
		return Decision{}, errors.New("break_glass.authorized configures no sources: set internal_list, codeowners, or anyone")
	}
	if len(cfg.Groups) > 0 {
		for _, g := range cfg.Groups {
			if _, _, err := parseGroupRef(g); err != nil {
				return Decision{}, err
			}
		}
		return Decision{}, fmt.Errorf(
			"break_glass.authorized.groups (%s): external identity group sources are a phase 2 feature and not yet supported; use internal_list, codeowners, or anyone",
			strings.Join(cfg.Groups, ", "))
	}
	if cfg.VCSBypass {
		return Decision{}, errors.New(
			"break_glass.authorized.vcs_bypass is not yet supported: resolving GitHub ruleset bypass actors to logins requires org-level team/role APIs the VCS client cannot query with repo-scoped credentials; use internal_list or codeowners")
	}

	d := Decision{}
	actor := strings.TrimPrefix(strings.TrimSpace(in.Actor), "@")
	if actor == "" {
		d.Trace = append(d.Trace, "denied: no actor identity supplied")
		return d, nil
	}

	// Self-authorization lockdown (opt-in). A PR that changes its own
	// authorizing files cannot authorize break-glass when this is set, no
	// matter which source would otherwise grant - evaluated before any
	// source so the denial is unambiguous.
	if cfg.RejectSelfAuthorization && len(in.AuthorizingPathsTouched) > 0 {
		d.Trace = append(d.Trace, fmt.Sprintf(
			"denied: reject_self_authorization is set and this PR modifies authorizing files (%s); authorize from a PR that does not change break-glass config or CODEOWNERS",
			strings.Join(in.AuthorizingPathsTouched, ", ")))
		return d, nil
	}

	// internal_list: explicit logins or org/team slugs.
	if len(cfg.InternalList) > 0 {
		for _, entry := range cfg.InternalList {
			e := strings.TrimPrefix(strings.TrimSpace(entry), "@")
			if e == "" {
				continue
			}
			if isTeamSlug(e) {
				if containsLogin(in.TeamMembers[e], actor) {
					d.Trace = append(d.Trace, fmt.Sprintf("internal_list: %s is a member of team %s", actor, e))
					d.Authorized, d.Source = true, SourceInternalList
					return d, nil
				}
				continue
			}
			if strings.EqualFold(e, actor) {
				d.Trace = append(d.Trace, fmt.Sprintf("internal_list: %s listed directly", actor))
				d.Authorized, d.Source = true, SourceInternalList
				return d, nil
			}
		}
		d.Trace = append(d.Trace, fmt.Sprintf("internal_list: %s not listed and not in any listed team", actor))
	}

	// codeowners: actor owns at least one changed path.
	if cfg.Codeowners {
		if path, ok := ownsAny(in.OwnedPaths, actor, in.TeamMembers); ok {
			d.Trace = append(d.Trace, fmt.Sprintf("codeowners: %s owns changed path %s", actor, path))
			d.Authorized, d.Source = true, SourceCodeowners
			return d, nil
		}
		if len(in.OwnedPaths) == 0 {
			d.Trace = append(d.Trace, "codeowners: no changed path has owners (missing CODEOWNERS or unowned paths)")
		} else {
			d.Trace = append(d.Trace, fmt.Sprintf("codeowners: %s owns none of the changed paths", actor))
		}
	}

	// anyone: broadest, evaluated last so the audit names a narrower
	// source when one matched.
	if cfg.Anyone {
		d.Trace = append(d.Trace, "anyone: granted")
		d.Authorized, d.Source = true, SourceAnyone
		return d, nil
	}

	d.Trace = append(d.Trace, fmt.Sprintf("denied: no configured source authorizes %s", actor))
	return d, nil
}

// ownsAny reports whether actor (directly or via team membership) is an
// owner of any changed path. Paths are checked in sorted-stable map
// iteration order; the first owned path found is returned for the trace.
func ownsAny(owned map[string][]string, actor string, teams map[string][]string) (string, bool) {
	for path, owners := range owned {
		for _, o := range owners {
			o = strings.TrimPrefix(o, "@")
			if isTeamSlug(o) {
				if containsLogin(teams[o], actor) {
					return path, true
				}
				continue
			}
			if strings.EqualFold(o, actor) {
				return path, true
			}
		}
	}
	return "", false
}

func containsLogin(members []string, login string) bool {
	for _, m := range members {
		if strings.EqualFold(strings.TrimPrefix(m, "@"), login) {
			return true
		}
	}
	return false
}

// isTeamSlug reports whether s looks like "org/team" rather than a login.
func isTeamSlug(s string) bool { return strings.Contains(s, "/") }

// parseGroupRef validates a phase-2 group source reference. Accepted forms:
// "<provider>:<name>" and "group:<provider>:<name>". Returns the provider
// and name; malformed references are their own (clearer) error.
func parseGroupRef(s string) (provider, name string, err error) {
	ref := strings.TrimPrefix(strings.TrimSpace(s), "group:")
	provider, name, ok := strings.Cut(ref, ":")
	if !ok || provider == "" || name == "" {
		return "", "", fmt.Errorf("break_glass.authorized.groups: malformed group reference %q (want \"group:<provider>:<name>\", e.g. \"group:aws_iam:oncall\")", s)
	}
	return provider, name, nil
}

// AuthorizingPaths are the repo paths whose modification in the same PR is
// flagged in the audit record: the .reeve config directory (where the
// break_glass block lives) and every CODEOWNERS location GitHub honors.
// Self-add is allowed by design; the flag makes it loud, not forbidden.
func AuthorizingPathsTouched(changed []string) []string {
	var out []string
	for _, p := range changed {
		p = strings.TrimPrefix(p, "/")
		switch {
		case strings.HasPrefix(p, ".reeve/") && (strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")):
			out = append(out, p)
		case p == "CODEOWNERS" || p == ".github/CODEOWNERS" || p == "docs/CODEOWNERS":
			out = append(out, p)
		}
	}
	return out
}
