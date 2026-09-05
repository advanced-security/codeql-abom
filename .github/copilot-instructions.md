# codeql-abom

Builds an Actions Bill of Materials for a GitHub organization from CodeQL results. Go 1.25,
single binary, SQLite store. Layering: `store` (SQL) → `graph`/`ingest`/`expand` (derivation) →
`serve`/`cmd` (delivery). Only `store` writes SQL.

## Architectural invariants

Breaking one of these produces data that looks right and is wrong. Each was learned from a real
bug in this repository.

**CodeQL rule IDs are the only source of a security verdict.** ABOM records and attributes
findings; it never derives its own. There is no `pin_status` column and there must never be one
— whether a reference is safely pinned is `actions/unpinned-tag`'s call. `TestSchemaHasNoVerdictColumns`
guards this. Every finding carries its `rule_id`; every claim traces to a query.

**Advisories enrich, they never adjudicate.** A GHSA record is stored as an attributed external
statement, exactly as a CodeQL result is attributed to a rule. Never derive "this action is
vulnerable" from an advisory alone — the observed reference may sit outside the vulnerable
range. Report the advisory, its range and the observed refs *verbatim*, side by side.
This matters because `actions/vulnerable-action` is exact-match only, against a curated
541-entry list, and found nothing across 204 real repositories. Most usage is floating tags no
exact-match model can resolve. The advisory layer covers that gap by declining to judge.

**Seed and upstream repositories are different.** `repository.origin` is `seed` (under review)
or `upstream` (fetched only to resolve what seeds depend on). Upstream repos contribute *edges*
— a composite action's own steps are its callers' supply chain — but never count as
*dependents*. Conflating them inflated blast radius 202 → 209 and made `actions/checkout`
depend on itself. `InsertUpstreamRepository` never demotes an existing seed: `actions/cache` is
genuinely both.

**Direct and transitive exposure are different work.** `hops = 1` means the repository names the
action itself and can fix it. `hops > 1` means it arrives via an intermediary someone else may
own. Never collapse them into one "affected" count.

**Priority is severity × reach, and nothing else.** CVSS and EPSS are reported alongside but
stay out of the arithmetic, so the number remains explainable. Never blend scores.

**Say what is not known.** Coverage, pending frontier items and unanalyzed dependencies are
first-class output, not omissions.

**An absent measurement is not a measurement of zero.** Reach is recomputed wholesale, so a node
ingested since the last rebuild has no `abom_blast_radius` row — and neither does a node nothing
references, whose reach genuinely is zero. Those are different facts and the watermark in
`abom_graph_generation` is what separates them. Drawing an unmeasured node at the smallest size
asserts "nothing depends on this" in a view where size means reach. Same rule as the pinning
split refusing to print `0 yours` with no verified identity.

**The graph tables are append-only.** Nothing deletes from `abom_node`, `abom_edge` or
`abom_finding`; only `abom_blast_radius`, `abom_exposure` and `abom_account` are truncated. Every
delta cursor the interface holds is a row-id watermark, so a `DELETE` would silently hand clients
an update that skips or repeats work. `TestGraphTablesAreAppendOnly` guards this.

**A queue state a reader distinguishes is a state the interface must have.** `pending` with
attempts spent is not the same as untouched, and a pause that takes effect between cycles is not
the same as paused. Collapsing either reads as a control that did nothing.

## Working style

**TDD, strictly.** Write the test, run it, watch it fail for the expected reason, then
implement. Tests are named after the behavior they protect, not the function they call.

**Validate against real data, with a before/after diff.** Unit tests passed through every
architectural bug listed above. Snapshot the real database before a pipeline change and diff
the headline metrics after:

```bash
cp abom.db abom-before.db   # then compare blast radius, node/edge counts, findings per rule
```

**Zero results usually means a coverage gap, not a broken traversal.** Before touching
traversal code, check whether the data supports the result at all — e.g. compare
`blast_radius.dependent_repos` against direct-edge repo counts.

**Never "fix" data that contradicts an assumption.** `actions/download-artifact@v8` looked like
corruption; `git ls-remote --tags` confirmed v1–v8 all exist. Verify the world first.

## Verification

Run before declaring work complete:

```bash
make verify              # check-corrupt, fmt, vet, lint, test -race, js, style, ql, ui, ui-test
make selfcheck           # needs the CodeQL CLI; not in verify because it takes tens of seconds
```

`make verify` is the only definition of "verified", and CI runs the same target. Do not assemble
an equivalent by hand: `check-corrupt` runs first and is not optional, because this environment
intermittently prepends a duplicate `package` clause when writing a Go file, which `gofmt -l`
reports as a parse error rather than a diff.

The interface is checked in three layers, and green in one says nothing about the others.
`ui-test` (bUnit) covers behavior and markup; it has no CSS engine, so a wrapped label or an
overflowing grid passes it. Layout and appearance are the screenshot pass in the
`verify-abom-ui` skill. `js` covers the browser module, which no compiler reads.

The service has one canonical lifecycle, so nobody re-derives it from shell history:

```bash
make start               # build + publish + serve in the background on $(ABOM_ADDR)
make status              # is it up, and what is discovery doing
make logs                # follow it; one line per cycle and per failure
make restart / make stop
make serve               # foreground, for watching it work
```

Override `ABOM_DB`, `ABOM_ADDR`, `ABOM_UI`, `ABOM_WORK` rather than editing the Makefile.
Discovery never starts on its own: `make start ABOM_SERVE_FLAGS=--expand` opts in. Run one
server, not several; two against one database is two writers, which the store forbids.

**Never run `make verify`, `make ql` or `make selfcheck` while discovery is running**, and the
same goes for the CodeQL MCP server's tools. Every `codeql` invocation shares one local
compilation cache, so a query test and a dependency's database build in flight together contend
for that cache as well as for every core. Check with `make status` first, and pause discovery
from the Discovery page or `make stop`. This is also why a timing-sensitive failure during a
crawl is not evidence of anything until it reproduces on a quiet machine.

## Comments

State only what the code cannot show, in one line. Explain *why* a constraint exists, never
what the next statement does.

```go
// codeql refuses to write into an existing directory, which re-analysis would otherwise hit.
if err := os.RemoveAll(dbDir); err != nil {
```
