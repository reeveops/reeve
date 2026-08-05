# toy-stack-terraform

Self-contained Terraform demo for reeve. Two root modules using only the
`random` and `null` providers with a local backend, so no cloud
credentials are required. Works identically with OpenTofu - set
`engine.type: tofu` in `.reeve/terraform.yaml` (and rename it
`tofu.yaml` if you like; the file name is convention, the `engine.type`
is what matters).

## Stack model

A root-module directory is a **project**; a terraform **workspace** is a
**stack**:

- `projects/random-name` declares workspaces `dev` and `prod` - two
  stacks (`random-name/dev`, `random-name/prod`) sharing one directory.
- `projects/null-touch` uses only the default workspace - a dir-per-env
  layout enumerates as `null-touch/default`.

Declared stacks in `.reeve/terraform.yaml` are authoritative: reeve
never needs `terraform init` just to know your stacks, and it creates a
declared-but-missing workspace on first use (never an undeclared one).

## Layout

```
examples/toy-stack-terraform/
├── .reeve/
│   ├── shared.yaml
│   └── terraform.yaml
└── projects/
    ├── random-name/
    │   └── main.tf        # random_pet, workspaces dev + prod
    └── null-touch/
        └── main.tf        # null_resource, default workspace only
```

## Try it

No terraform binary needed for enumeration and lint:

```
cd examples/toy-stack-terraform
reeve lint
reeve stacks
```

Full preview requires `terraform` (or `tofu`) on PATH:

```
reeve plan-run --root . --sha demo
```

reeve runs `init -input=false`, selects the workspace, saves a plan with
`plan -detailed-exitcode -out=...`, and parses `show -json` for the PR
comment. Apply consumes that exact saved plan file. Drift checks use
`plan -refresh-only`, which never mutates your state.
