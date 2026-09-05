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
  `database analyze`; everything else goes to `query run` as before, from the same resolved
  plan. A suite that is *entirely* findings is passed as a suite, because that is what analyze
  is built to take and a suite carries selection its expanded file list does not; only a suite
  that mixes lanes is named query by query. A binding that names findings queries with no
  analyzer configured is now refused before the round starts, because absorbing it quietly is
  the exact defect this replaced.

## What the reconciliation found

Every piece of this lane already existed and was tested: `runner.AnalyzeDatabase`,
`ingest.FindingsFromSARIF`, and `internal/archive` wiring the two together. The gap was one
`continue` in `expand.plan`. `internal/archive` is not reachable from the binary at all —
`go list -deps ./cmd/codeql-abom` does not contain it and nothing outside the package calls it —
so its findings path had never run in a crawl. Findings in the corpus arrived through `build`
ingesting SARIF produced elsewhere.

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

## Pending

1. **`analyzed_suite` and `analyzed_suite_version` per frontier row**, so "re-process with an
   updated suite" is expressible and staleness is visible per suite rather than per tier.
2. **Skip what has not changed.** `QueryRunExists` makes this cheap: only queries or databases
   that actually changed need to run again. Nothing consumes it yet.
3. **Graph result sets.** `internal/bqrs` already parses every named result set, so `nodes`/
   `edges` are readable; only a `StreamResultSet(name)` sibling to `StreamSelect` is missing.
4. **`internal/archive` is dead code.** It is a working, tested pipeline for analyzing a
   directory of archives that no command reaches. Either give it a command or delete it; leaving
   it is how the findings lane looked implemented for as long as it did.
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
