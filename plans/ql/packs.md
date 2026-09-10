# Packs as the versioned unit

**Status:** four scoped packs under `ql/`, pinned and checked by `make check-versions` inside
`verify`. Suites are pack-qualified, and each round records which pack supplied each query. The
pinning rules are in `.github/instructions/codeql-queries.instructions.md`.

## Why this exists

The version that matters is the pack's. A suite is not a thing with a version — it is a program
over packs, and what it contains is only knowable by asking `codeql resolve queries`. Two of the
four packs own no queries at all: their version tracks *which upstream queries ABOM selected*.

## Pending

1. **`abom_query_run.query_version` and `pack_version` are never written.** Both columns exist and
   the pack is in hand where a run is recorded. Until they are filled, "what has not run since
   pack X vY" can only be answered by re-resolving every suite.

## Deferred by decision

**Publishing to GHCR under `advanced-security`**, after `release-codeql.yml`. Nothing outside this
repository consumes these packs yet, and naming and versioning landed early so it is never blocked.

## Verification

- `codeql pack ls` finds every pack at the version in `.codeql-version`.
- `codeql resolve queries` per suite returns the same counts across any move or rename: a rename
  must not change what runs.
- A real round records an upstream pack version for findings and the ABOM version for the graph.
