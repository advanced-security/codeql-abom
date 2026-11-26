---
name: upgrade-gh-actions-lock
description: 'Move codeql-abom to a new gh-actions-lock release: the audited commit every workflow builds, the Go parser ABOM reads lockfiles with, the verification categories it recognizes, and the lockfile itself. Use when gh-actions-lock or github.com/github/actions-lock publishes a release, when make pins or the self audit reports a schema it cannot read, or when a verification reports a category ABOM shows as inconclusive.'
---

# Upgrade gh-actions-lock

The tool's contract has four readers, and missing one fails quietly: a schema left behind reads as
an unsupported lockfile, and a category left out reads as inconclusive.

## Procedure

1. **The audited commit.** `.github/actions/install-gh-actions-lock/action.yml` holds
   `ACTIONS_LOCK_COMMIT`, its version comment, and `GOTOOLCHAIN` set to the `go` directive of the
   tool's own `go.mod`. `setup-go` exports `GOTOOLCHAIN=local` for this repository's older toolchain,
   which refuses to build a tool that needs a newer one. Every workflow that reads the lockfile uses
   that action, so this is the one place.
2. **The parser.** `github.com/github/actions-lock` in `go.mod` and `parserVersion` in
   `internal/lockfile/reader.go`, kept equal by a test. Changing either requeues every commit with a
   lockfile on the next start, which costs a re-read of each.
3. **Categories.** `knownCategories` in `internal/lockverify/verify.go`, from the release's
   `checks.Category` constants, and whatever its JSON report adds.
4. **The lockfile.** Regenerate `.github/workflows/actions.lock` with the new CLI, never by hand, then
   `gh actions-lock --verify-local --no-fix --no-interactive` and `--verify`.
5. **Your own install.** Build the same commit locally the way the action does; `make pins` runs
   whatever `gh actions-lock` is installed, and a different one reads the lockfile differently.

## Before calling it done

- `make pins` and `make verify` pass with the new tool installed.
- A lockfile in each schema version the parser claims to read still reads.
- Dependabot's `github_actions` ecosystem runs its own CLI and writes the newest schema whatever the
  pinned CLI writes, so the parser must read what Dependabot writes.
