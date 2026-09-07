# Query suites as configuration

**Status:** tiers bind to suites as stored configuration, editable while the server runs, and
both lanes now run.

## Why this exists

CodeQL is a reprogrammable but deterministic inspection engine: the same query at the same
version against the same database gives the same answer. That makes the query set the natural
place for this project to grow, and it means the set cannot be compiled in. A query added next
year must run without a release.

Today `--expand-suite` names one suite for every repository at every depth. That is the whole
configuration surface, and it cannot express "broad pass on what we own, minimum on what we
reach through three intermediaries".

## Done

- Suites resolved at run time via `codeql resolve queries`, expanded once per round rather than
  per repository — resolving shells out, and across thousands of repositories that would cost
  more than the queries.
- Each query routed by **its own metadata** (`codeql resolve metadata`), never by filename: a
  renamed query must not change how its results are read.
- Four result shapes, and anything else refused loudly rather than guessed: `#select` edges,
  `#select` observations, `@kind graph`, SARIF findings. A silently mis-ingested result is worse
  than one that never arrives.
- `abom-dependency-graph.qls` holds AbomEdges, AbomSurface, AbomStepConditions.
- Provenance already exists and is sufficient: `abom_query_run(query_id, query_version,
  pack_version, repository, db_commit, ran_at)`, plus `QueryRunExists` for the determinism skip.
- **Tier → suite binding in `abom_tier_suite`**, seeded at migration and rewritable over
  `PUT /api/discovery/suites/{tier}` without a restart. A batch is planned by the shallowest
  depth it holds, so a repository reached both directly and through an intermediary earns the
  broader pass. A suite two tiers share is resolved once per round, because resolving shells out.
  `--expand-suite` and `--query` still win when given, which is how an operator runs something
  once without editing configuration; `serve` no longer defaults `--query`, so the binding is
  what a normal crawl uses.
- **The findings lane runs during a crawl.** A bound suite's findings-shaped queries go to
  `database analyze`; everything else goes to the BQRS lane, from the same resolved plan. A suite
  that is *entirely* findings is passed as a suite, because that is what analyze is built to take
  and a suite carries selection its expanded file list does not; only a suite that mixes lanes is
  named query by query. A binding that names findings queries with no
  analyzer configured is now refused before the round starts, because absorbing it quietly is
  the exact defect this replaced.

## What the reconciliation found

Every piece of this lane already existed and was tested: `runner.AnalyzeDatabase`,
`ingest.FindingsFromSARIF`, and `internal/archive` wiring the two together. The gap was one
`continue` in `expand.plan`. `internal/archive` was not reachable from the binary at all —
`go list -deps ./cmd/codeql-abom` did not contain it and nothing outside the package called it —
so its findings path had never run in a crawl. Findings in the corpus arrived through `build`
ingesting SARIF produced elsewhere. **It has since been deleted; see below.**

Two assumptions turned out wrong on the way:

- **A findings run does not need an identity ABOM invents.** The first attempt recorded the
  SARIF under a made-up `abom/findings-lane` query id. Attribution was already in the data:
  every result carries its rule, and `abom_finding.rule_id` is `NOT NULL`. The query run now
  records only what was *invoked*, matching what `archive` already did.
- **The edges result was attributed to a flag.** `consume` named the run from `--query-id`,
  which is empty once a crawl runs from a binding, and wrong whenever a suite supplied the
  edges query. It is now named by the query that actually produced the result.

## What the empty binding cost

"Bound to nothing" is a legitimate instruction — *do not analyze this tier* — and it collides
with seeding defaults at migration: an empty set and an unconfigured one look identical in the
table, so every restart would have seeded the defaults back over a deliberate choice. That needed
a second table, `abom_tier_configured`, recording that a tier was decided at all. Verified on the
reference corpus rather than argued: binding `direct` to `[]`, restarting the server, and reading
it back still returns `[]`.

This is the same distinction the rest of ABOM keeps between an absent measurement and a measured
zero, arriving in configuration.

## Done: `internal/archive` is deleted, and the class of mistake is now asserted

The entry offered a command or deletion. **Deleted**, and not on tidiness grounds — adopting it
would have reintroduced a defect the ledger exists to prevent.

`archive.consumeFindings` recorded `store.QueryRun{QueryID, Repository, DBCommit}` and nothing
else: no pack, no pack digest, no CLI version. Every row it wrote would have joined the **1,555
pre-identity rows** `db/analysis-ledger.md` states can never converge, because a row that does not
say what produced it never matches a current run key and has to be analyzed again forever. It
also passed no `CLIVersion` to `FindingsFromSARIF`, so the `tool.driver.semanticVersion` check —
the one thing stopping SARIF being attributed to an analyzer that did not produce it — was
skipped. Its query lane still called `RunQuery` once per query, the pattern just removed
everywhere else.

Nothing is lost. The capability was "run ABOM's queries over database archives already on disk",
and the extension point for that already exists and says so: `pipeline.DatabaseSource` is
documented as "satisfied by `catalog.Client` and by any mrva-system or mrvahepc adapter added
later". A directory of archives is an adapter, not a parallel `Runner` with its own pool,
options and prepare/consume. `sarifin.ReadProvenance` keeps a production caller in
`ingest/bulk.go`, so the deletion orphaned nothing.

**The guard matters more than the deletion.**
`TestEveryInternalPackageIsReachableFromABinary` compares `go list ./internal/...` against
`go list -deps <module>/cmd/...` and fails naming anything no command reaches. Written first, it
failed on exactly one package. Reading dead code answers "does this work" and never "does this
run", and no build, test run or review diff tells the two apart — which is why this lane looked
implemented for as long as it did.

## Pending

1. **`analyzed_suite` and `analyzed_suite_version` per frontier row**, so "re-process with an
   updated suite" is expressible and staleness is visible per suite rather than per tier.
2. **Skip what has not changed.** `QueryRunExists` makes this cheap: only queries or databases
   that actually changed need to run again. Nothing consumes it yet.
3. **Graph result sets.** `internal/bqrs` already parses every named result set, so `nodes`/
   `edges` are readable; only a `StreamResultSet(name)` sibling to `StreamSelect` is missing.
4. **`ingest.FindingsFromDirectory` is the same mistake one level down.** Deleting
   `internal/archive` left it with no caller outside its own tests, and the package guard cannot
   see it because `ingest` is reachable. It ingests a directory of SARIF against a
   `RepoResolver`, which was the other half of the archive acquisition model. Delete it or give
   it a caller — but it is a public ingest entry point, so removing it is a decision about what
   the ingest API offers rather than a tidy-up.
5. **A control for the binding.** `ui/tier-suites.md` — the endpoints exist and nothing renders
   them.

## Starting defaults, when the binding exists

Source = dependency-graph + ast-cfg + findings. Direct = dependency-graph + findings.
Transitive = dependency-graph only, on volume grounds. These are defaults, not constraints — the
whole point is that they are editable.

## Verification

The acceptance test for the mechanism, not the queries: **add a throwaway query to a suite and
confirm its results land, attributed, with no Go change.** If that ever requires code, the design
has failed. `TestAQueryNobodyWroteCodeForCanStillContributeFacts` guards the ingest half.
