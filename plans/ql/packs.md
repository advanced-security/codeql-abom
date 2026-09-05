# Packs as the versioned unit

**Status:** the restructure, the naming, the exact pins, the drift check, pack-qualified suite
identity and per-query pack provenance have shipped. The interface and the CI remain.

## Why this exists

The version that matters is the pack's. A suite is not a thing with a version — it is a program
over packs, whose `query` / `queries` / `qlpack` instructions locate, `include` / `exclude`
filter in order, and `import` / `apply` compose. What a suite contains is only knowable by asking
`codeql resolve queries`. So the intermediate level between "suite" and "query" is the **pack**,
and that is what carries a version anyone can act on.

Two things followed from taking that seriously. Pack names have to be scoped before anything is
published, because a rename after publication is not a rename. And the suites had to stop being
identified by a path in this repository, since a path survives neither a directory move nor
publication.

## Done

**Layout, mirroring `server/ql/<lang>/<purpose>/{src,test}` from codeql-development-mcp-server:**

```text
ql/actions/graph-extraction/src   advanced-security/abom-actions-graph-extraction-src   3 queries + dependency-graph suite
ql/actions/graph-extraction/test  advanced-security/abom-actions-graph-extraction-test
ql/actions/findings/src     advanced-security/abom-actions-findings-src     suite only
ql/actions/selfcheck/src    advanced-security/abom-actions-selfcheck-src    suite only
```

Two of the four packs own no queries at all. That is the point: their version tracks *which
upstream queries ABOM selected*, independently of the extraction queries, and ABOM still authors
no security finding of its own.

**Versioning.** A pack tracks the CodeQL CLI it is compiled for, so every pack is `2.26.4`,
matching `.codeql-version`. Iteration between CLI releases uses `-next.N` — attached to the
*upcoming* version, because semver orders `2.26.4-next.1` **before** `2.26.4`, so a pre-release
of a version already published would be treated as older. `codeql pack publish` needs
`--allow-prerelease` for any hyphenated version.

**Exact pins.** The selection suites name `version: 0.6.34` on their `from:`, not a range.
Without it the CLI resolves the most recent pack, so the set of rules ABOM attributes could
change with no commit here.

**`make check-versions`.** Exact pinning writes the same upstream version in three places — the
suite, the pack manifest, the lock file — and `codeql pack upgrade` rewrites two of them and
never the one inside a `.qls`. The check asserts all three agree and that every pack matches
`.codeql-version`. It runs in `verify`, second only to `check-corrupt`.

**Pack-qualified suite identity.** Suites are `<pack>:codeql-suites/<name>.qls` everywhere: the
tier bindings, `DefaultTierSuites`, `runner.DefaultSuite`, the selfcheck target.
`runner.AvailableSuites` walks the pack manifests under `ql/` and reads the two fields a
reference needs, so a suite is offered by being dropped into a pack — no release, and no CLI call
on the request path.

**A migration that rewrites rather than reseeds.** `migrateSuiteNames` runs before
`seedTierSuites`, so a binding that only needed renaming still counts as configured and the
defaults are not written over a deliberate choice. A binding naming something unrecognized is
left verbatim, because dropping it would silently change what a crawl runs.

**Provenance per query.** `abom_suite_query` records what each suite last resolved to and which
pack supplied each query, written by the expander in the round that already paid to resolve it.
`runner.PackOf` attributes a query by walking up to the nearest manifest, so provenance for a
whole suite costs a few file reads rather than a process per query. `/api/discovery/suites`
serves it grouped by pack; a suite nobody has resolved is absent rather than reported as zero.

## Why "graph-extraction", not "extraction"

*Extraction* already means something specific in CodeQL — turning source into a database, done by
an extractor. A pack directory called `extraction` reads as though it holds one. These queries
extract the ABOM *graph* from a database that already exists, which is the opposite end of the
pipeline. Renamed before the name could spread into a published pack.

## What the checks caught

- The `while read` loop in `check-versions.sh` was first written behind a pipe, which runs it in
  a subshell: every mismatch would have been printed and the script would still have exited 0.
  Proved by injecting a wrong pin and watching it fail, rather than by reading the code.
- `UPDATE abom_tier_suite SET suite = ?` alone cannot converge when a tier already holds the new
  name, because `(tier, suite)` is unique — the update fails instead of being a no-op. The stale
  row is deleted first.
- **A `<pack>:<suite>` reference does not resolve for a pack that has never been published.** The
  CLI looks it up in the package cache and reports it as misspelled. Every CLI call that takes a
  reference now passes `--additional-packs ql`. Unit tests could not have found this: they resolve
  nothing. The first real round failed outright.
- **The registry writes `qlpack.yml`; this repository writes `codeql-pack.yml`.** `PackOf` read
  only the second, so all 35 upstream queries — the ones whose provenance matters most, because
  they are the rules ABOM attributes findings to — recorded an empty pack and an empty version.
  Visible only because the corpus check printed `@` where a version belonged.

## Verification performed

- `codeql pack ls` finds all four packs at `2.26.4` under the new names.
- `codeql resolve queries` per suite: 3 / 35 / 28, unchanged by the move. A rename must not
  change what runs, and findings is still exactly the same 35 queries.
- The migration against a copy of the reference corpus rewrote all three stored bindings, lost
  none and duplicated none.
- A real expansion round recorded `codeql/actions-queries@0.6.34` for the findings suite's 35
  queries and `advanced-security/abom-actions-graph-extraction-src@2.26.4` for the graph suite's
  3 — matching the exact pins and `.codeql-version` respectively.
- `make verify` green, with `check-versions` in the chain.

## Pending

1. **`abom_query_run.query_version` / `pack_version`.** The columns exist and have never been
   written; the pack is in hand at the point the run is recorded.
2. **The Discovery control box.** Shipped as one box with per-suite disclosure; the layout
   re-measure and screenshot pass are outstanding. `ui/discovery-config.md`
3. **CI: CodeQL CLI update automation** — `update-codeql.yml` and `scripts/bump-pack-versions.sh`
   are written and exercised locally, but have never run in Actions.
4. **CI: publishing to GHCR under `advanced-security`**, after `release-codeql.yml`. Deliberately
   deferred until much later; the naming and versioning landed now so that publishing is never
   the thing blocked.
