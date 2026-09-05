# Query suites as configuration

**Status:** suites run; binding them to tiers is still a command-line flag.

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

## Pending

1. **Tier → suite binding as stored configuration**, editable at run time. A tier binds to a
   *set* of suites, so no combined per-tier suite artifact is ever needed.
2. **`analyzed_suite` and `analyzed_suite_version` per frontier row**, so "re-process with an
   updated suite" is expressible and staleness is visible per suite rather than per tier.
3. **Skip what has not changed.** `QueryRunExists` makes this cheap: only queries or databases
   that actually changed need to run again. Nothing consumes it yet.
4. **Findings lane for upstream repositories.** `abom-findings.qls` (35 upstream security
   queries) runs through `database analyze`, which cannot execute `@kind graph` or table
   queries. The two lanes must stay separate; a suite may legitimately mix them and the BQRS
   lane skips what belongs to the other.
5. **Graph result sets.** `internal/bqrs` already parses every named result set, so `nodes`/
   `edges` are readable; only a `StreamResultSet(name)` sibling to `StreamSelect` is missing.

## Starting defaults, when the binding exists

Source = dependency-graph + ast-cfg + findings. Direct = dependency-graph + findings.
Transitive = dependency-graph only, on volume grounds. These are defaults, not constraints — the
whole point is that they are editable.

## Verification

The acceptance test for the mechanism, not the queries: **add a throwaway query to a suite and
confirm its results land, attributed, with no Go change.** If that ever requires code, the design
has failed. `TestAQueryNobodyWroteCodeForCanStillContributeFacts` guards the ingest half.
