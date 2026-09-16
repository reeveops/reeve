# Stack Discovery

## Split

- **Engine owns:** `EnumerateStacks(ctx, root)` returning `(project, path, name, raw)` tuples; `ValidateStack(ctx, stack)`.
- **Core owns:** pattern matching (doublestar), include/exclude filtering,
  change mapping, module dependency resolution, the orchestrating pipeline,
  and `reeve stacks` / `reeve stacks discover` CLI logic.

## Runtime behavior

Always explicit. reeve acts only on stacks that are either declared
literally or match a declared pattern. There is no runtime auto-discovery.

## Pattern language

Doublestar glob. `re:` regex escape hatch is **not shipped in v1** (cut
per plan appendix). Revisit only if a user files a concrete need.

## Pipeline

1. **Declare** - literal project entries and `pattern:` globs from engine config.
2. **Include** - if any include rules exist, only matching entries pass.
3. **Exclude** - drop matching entries.
4. **Resolve** - engine verifies each remaining stack exists.
5. **Map to changes** - drop skippable files, match the rest to stacks; unmapped
   files broaden to all stacks under `scope: auto`.

## Change-mapping order

1. **Scope** - convert VCS paths to paths relative to the configured root and drop files outside that root.
2. **Skip** - drop files matching default skip globs + `ignore_changes`.
3. **Docs-only** - if every scoped file is skippable, run nothing; report "Documentation/asset-only changes".
4. **Match** - remaining files map to stacks by path / per-stack config / `extra_triggers`.
5. **Broaden** - files matching no stack are "unmapped". `scope: auto` (default) previews/applies all stacks and reports why; `scope: pulumi_only` ignores them.

## Configured-root path scope

VCS providers return repository-relative changed paths. Reeve MUST strip the configured root prefix before change mapping and pre-approval config checks.

#### Scenario: Nested root maps one stack

- **GIVEN** the configured root is `tf`
- **AND** a stack path is `envs/lifecycle`
- **WHEN** the VCS reports `tf/envs/lifecycle/main.tf`
- **THEN** only that stack is selected

#### Scenario: Change outside nested root

- **GIVEN** the configured root is `tf`
- **WHEN** the VCS reports only `app/main.go`
- **THEN** no stack is selected

#### Scenario: Nested security config change

- **GIVEN** the configured root is `tf`
- **WHEN** the VCS reports `tf/.reeve/notifications.yaml`
- **THEN** the pre-approval notification gate treats `.reeve/notifications.yaml` as modified

## Default skip globs

Non-load-bearing files, merged with `ignore_changes`:

- `*.md`, `*.markdown`, `*.adoc`, `*.asciidoc`, `*.rst`, `*.txt`, `LICENSE`.
- Images: `*.png`, `*.jpg`, `*.jpeg`, `*.gif`, `*.svg`, `*.webp`.
- `docs/` directories are NOT skipped - they can hold config or program-read data.

## Shared-directory change mapping

One directory can hold many stacks (shared `Pulumi.yaml` + one `Pulumi.<name>.yaml` per stack). Matching is per-file:

- `Pulumi.<name>.yaml` change - affects only stack `<name>`.
- Sibling `Pulumi.<other>.yaml` - ignored for this stack.
- Shared `Pulumi.yaml`, program code, nested files - affect every stack in the directory.

## `reeve stacks discover`

Dev-time tool (not runtime). Walks the repo, clusters discovered stacks by
shared-prefix, generates suggested pattern entries. With `--write`, mutates
engine config via a comment-preserving YAML writer. Same command supports
modules via `reeve stacks discover --kind modules`.

`reeve stacks discover --engine <engine>` is the canonical surface for
discovery; per-engine subcommands (`reeve <engine> find`) are not supported.
