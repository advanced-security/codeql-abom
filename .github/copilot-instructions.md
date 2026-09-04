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
for f in $(find . -name '*.go'); do [ $(grep -c '^package ' "$f") -gt 1 ] && echo "CORRUPT: $f"; done
gofmt -l . && go vet ./... && go test ./... -count=1 -race
codeql test run ql/actions/abom/test
```

The first line is not optional: this environment intermittently prepends a duplicate `package`
clause when writing Go files, which `gofmt -l` reports as a parse error rather than a diff.

## Comments

State only what the code cannot show, in one line. Explain *why* a constraint exists, never
what the next statement does.

```go
// codeql refuses to write into an existing directory, which re-analysis would otherwise hit.
if err := os.RemoveAll(dbDir); err != nil {
```
