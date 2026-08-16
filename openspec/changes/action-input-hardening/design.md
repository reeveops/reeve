# Design

## Expression boundary

GitHub expressions appear only in action metadata fields such as `env:`,
`with:`, and `if:`. No `${{ ... }}` expression appears inside a `run:` block.

The main step reads inputs from named environment variables. Command and extra
argument strings are tokenized into arrays and every execution expands them as
quoted argv.

## Supply-chain pins

Every `uses:` reference in `action.yml` and `.github/workflows` is a full
40-character commit SHA. A trailing comment preserves the release tag used to
select that commit.

## Binary verification

The composite action installs cosign from a pinned action before attempting a
prebuilt download. Installer failure is non-fatal because the fetch helper then
rejects the binary and selects the source-build path.

The fetch helper requires both a matching checksum and a valid keyless bundle.
It never caches a binary authenticated by checksum alone.
