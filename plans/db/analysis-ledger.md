# A verifiable analysis ledger

**Status:** CodeQL invocations are serialized machine-wide and measured at 1 during a
`--concurrency 4` crawl. Every run records the pack, its content digest and the CodeQL that
evaluated it; a repository whose whole plan is already on record is settled without being
analyzed; and every finding carries a content hash that two consecutive rebuilds reproduce
exactly. Selection and pinning are pending, and the identity columns cannot be tightened while
the corpus still holds pre-identity rows.

## Why this exists

Every unit of analysis work is **(database identity x query identity)**, and both should be
content-addressed. If the pair is already on record, CodeQL is deterministic and nothing needs to
run. Skipping, granular re-analysis and provenance are then one mechanism seen from three sides:

| Question | Becomes |
| --- | --- |
| Re-analyze what has not run since pack X vY | rows where `pack_version < Y` |
| Re-analyze after a suite or pack bump | the bump changes query identity, so the pair is unseen |
| Re-run only the queries that changed | only changed queries have a new identity |
| Skip an unchanged query on an unchanged database | the pair is already on record |
| Re-analyze one dependency I own | filter plus a pin, not a second pipeline |

## The chain

```text
source        commit SHA                              already in abom_frontier.resolved_sha
database      H(repo, commit, cli_version, flags)
query         H(rule_id, pack, pack_version, pack_digest)
analysis      H(database, query)                      abom_query_run.run_key is this shape
result        H(normalized result)                    abom_finding.result_hash
```

The result link deliberately does **not** fold in the analysis, which an earlier draft of this
line had it do. That version contradicted the identity rule two sections down: an analysis hash
contains the repository, so a result hash built on it could never be shared across repositories,
and the group-by it exists to enable would have been impossible. Provenance is not lost by leaving
it out — every finding already carries `query_run_id`, which is the analysis.

`store.QueryRunExists` already implements the lookup and its comment already states the rationale.
It is now consumed by `expand.alreadyRecorded`, which is what the identity was recorded for.
What remains is selection and pinning.

## Identity rules

**Hash the pack directory, with no exceptions.** `pack_digest` over the sorted
`(relative path, H(contents))` of every `.ql`, `.qll`, `.qls` and `.yml`.

An earlier draft split this — `pack@version` for published packs, a content hash only for local
ones, since registry versions are immutable. That asymmetry was weaker: `codeql-pack.lock.yml`
records a version and **no digest**, so immutability would be trusted rather than verified. One
rule catches a local edit, which a CLI-pinned pack version cannot, and verifies upstream
immutability instead of assuming it. It over-invalidates — editing `AbomEdges.qll` re-runs all
three graph-extraction queries — which is sound, cheap and simpler than resolving transitive
imports.

**`cli_version` is inside the identity**, so a CLI upgrade re-analyzes the corpus. Correct, and
affordable while nothing is published. The update automation's pull request has to say so.

**Result hashes are per result and deliberately repository-independent**, stored as
`(repository, result_hash)`. The same defect reached through many repositories then shares one
hash, which turns "how far does this finding reach" into a group-by rather than a traversal. On
the real corpus that is **115 groups covering 307 rows, the widest spanning 10 repositories** —
the earlier text guessed twelve, which was the right order and the wrong number.

Nothing run-specific may enter the hash: no absolute paths — the work directory is
`/tmp/abom-work/<owner>_<repo>/db/...` and would poison every one — no timestamps, no run ids, no
`automationDetails`.

The line **number** is out too, and the message with it. A finding does not become a different
finding because unrelated lines were inserted above it, and CodeQL's `primaryLocationLineHash`
already identifies the location by its content; all 4,788 findings in the corpus carry one. The
message is a rendering of the finding rather than part of it, and for Actions rules it embeds the
workflow and job names, which are exactly the repository-specific detail the hash must exclude —
including it cost 9 of the 115 groups.

## What cannot be hashed

Without this the phrase "end to end" oversells.

1. **A CodeQL database is not byte-reproducible.** Timestamps and parallel extraction vary, so we
   hash the *inputs*. The claim is "built from the same source at the same CLI version", not
   "the same database".
2. **SARIF bytes vary between identical runs.** We hash a normalized projection; hashing raw
   SARIF would differ every run and be worse than useless.
3. **The CLI is identified by a version string, not a digest.** A decision, recorded as one.
4. **`resolved_sha` is a git SHA-1** for most repositories — the identity GitHub serves, and no
   stronger one is on offer.

## Done: one CodeQL invocation at a time

`runner.Lock`, taken by `database create`, `database analyze` and `query run`. Two halves,
because neither suffices alone: a mutex serializes this process's workers, and an advisory
`flock` under the user cache directory serializes a second abom process. `flock` rather than an
`O_EXCL` lock file because the kernel releases it when a process dies.

Waiting is cancellable, which `sync.Mutex` alone is not: pausing discovery must not have to wait
out a database build.

It does **not** make `make ql`, `make selfcheck` or the CodeQL MCP server cooperate — they take
no lock. That stays a documented rule; this covers what the pipeline controls.

### The measurement was wrong three times before it was right

Peak concurrency first read as **3**, which looked like the lock failing. It was not.

- `pgrep -f "codeql (database|query) "` **matches the sampling shell itself**, whose argv
  contains the pattern.
- Rewriting it as a script fixed that and still read 3, because **one invocation is a chain of
  three processes**: the `gh` extension shim execs the `codeql` wrapper script, which execs the
  JVM, and all three inherit the same argv.

Counting only chain roots — a matched process whose parent is not itself matched — gives **1**
during a `--concurrency 4` crawl. `scripts/sample-codeql-concurrency.sh` keeps the corrected
method, because the naive version is convincing and wrong.

The before is 4 by construction rather than by measurement: all four CodeQL calls sit inside
`work.Pool.Prepare`, which runs `Concurrency` workers.

## Done: every run says what produced it

`abom_query_run` carries `pack`, `pack_version`, `pack_digest` and `cli_version`, and `run_key`
hashes all of them. `runner.PackDigest` walks the pack directory; `runner.DigestCache` keeps one
hash per pack per process, since the 35-query findings suite would otherwise re-walk
`actions-queries` for every repository. The suite plan carries the identity, so the edges lane,
the extra BQRS queries and the findings lane all record the same thing.

Measured on the real corpus: the three graph queries record
`advanced-security/abom-actions-graph-extraction-src@2.26.4`, the findings lane records
`codeql/actions-queries@0.6.34`, all four carry `cli_version 2.26.4`, and the digests agree with
what `abom_suite_query` recorded at plan time. The same query against the same repository now has
two run keys — the pre-identity one and the current one — so those pairs re-run once. That is the
right outcome: nothing verified them.

`database analyze` runs the whole findings suite as one invocation, so no single query owns that
row. It is identified by the packs it draws rules from: the one pack when there is one, and the
set hashed together when there is more than one. Leaving it blank would have meant a
`codeql/actions-queries` upgrade left the run key unchanged, and the skip declining to re-analyze
against rules that had in fact changed.

### A column added to the schema is not a column an existing database has

`pack_digest` went into `CREATE TABLE abom_suite_query` and not into `addedColumns`. Every unit
test passed, because they all build a fresh database. The real corpus — which is upgraded in
place, being hours of crawling — failed at the first write with `table abom_suite_query has no
column named pack_digest`.

`TestAnUpgradedDatabaseAcceptsResolvedSuiteContents` and its sibling pin this by writing the
legacy `CREATE TABLE` out longhand, migrating it, and then calling the real store methods. The
legacy DDL is checked in rather than derived from the current schema, which is the whole point:
the two have to be able to disagree.

### A merge that names fields drops the ones nobody remembered

`plansByTier` folds each bound suite into a tier by appending. It appended the two query slices
and nothing else, so `cliVersion` — read once per round by `plan` — never reached the tier, and
every row landed with a NULL `cli_version`.

What made it slow to find is that the row beside it looked right: `pack` and `pack_digest` travel
*inside* `plannedQuery`, so they survived the append while the plan-level field did not. Every
test of `plan` passed, because `plan` was never the broken half. `absorb` now names every field in
one place, and `TestMergingSuitesIntoATierKeepsTheRunIdentity` fails if a new one is forgotten.

The first attempt hid this: `plan` swallowed a `Version` error and carried on with an empty
string. It was not in fact erroring, but the swallow is why the symptom read as "the CLI call
fails" for several rounds. Reading a version now fails the round, which is the only honest
answer — a ledger row that omits what evaluated it cannot be told from one produced by a
different CodeQL, and the skip this is all for would trust it.

## Done: work already on record is not done twice

`expand.alreadyRecorded` asks `QueryRunExists` for every row `tierPlan.runs` would write. All of
them present means the repository is settled without a database build, a query or an analysis.

Both the skip and the record are built from `tierPlan.runs`, deliberately. Assembled separately
they would drift the moment either gained a field, and the failure mode is silent: the skip simply
stops firing and everything still looks correct.

Measured on the real corpus, four repositories re-analyzed immediately after being analyzed:

| | Analyze | Re-analyze |
| --- | --- | --- |
| Wall clock | 109 s | 28 s |
| CodeQL invocations | 20 | 0 |
| Ledger rows written | 16 | 0 |
| Nodes, edges, findings | unchanged | unchanged |

The graph did not move on either pass, which is correct and worth stating: those repositories were
already in the corpus from before the identity existed, so the first pass re-ran them once to earn
a current key and found the same edges, which `edge_key` deduplicated. That is the one-off cost
the identity section predicts, now visible as a number.

### The checkout is not avoidable, and must not be claimed as avoided

The skip runs after `Fetch` and before `CreateDatabase`, because the run key includes the commit
and the commit is only known once the source is in hand. `abom_frontier.resolved_sha` would have
allowed skipping earlier and would have been wrong exactly when it mattered: it is the *last known*
sha, so a moved ref would skip the re-analysis that the move is the reason for.

### A skip is a result, not an error

The obvious implementation — return `work.ErrSkip` from the parallel phase — strands the item.
Only `Consume` completes frontier items, and only `Consume` may write at all, so an item skipped in
`Prepare` would go back to the queue on the next `ReclaimStale` and be skipped again forever. It
travels as `Prepared.AlreadyRecorded` instead, and `settle` runs for skipped and analyzed
repositories alike.

`settle` also enqueues discoveries on the skip path. The ledger row is written before discoveries
are, so a run interrupted between the two would otherwise skip on the next pass and never enqueue
what it had already paid to find.

### Two callers read "nothing analyzed" as "nothing left to do"

Neither is in the expansion itself, and both would have been silent in production.

`expand-all` ended its round loop on `stats.Processed == 0`, so the first fully skipped round would
have stopped the command with the rest of the queue still in it. `roundDidWork` now names the
condition and counts skips.

The supervisor logged a cycle only when it ingested something, so a long re-analysis would have run
for hours writing nothing to `make logs` — indistinguishable from a hung crawl, which is the one
thing that log exists to rule out. `cycleProgressed` and `cycleIngested` are now separate: the
former decides whether to log, the latter whether to relink findings.

## Done: every finding says what it is, independently of the run

`normalize.ResultHash` over the rule, the repository-relative path and CodeQL's
`primaryLocationLineHash`, stored on `abom_finding.result_hash`.

The test the plan named as the only real one passes on the real corpus: a repository analyzed,
its ledger rows and findings dropped so the skip could not fire, then analyzed again — **15
findings, byte-identical hashes across both rebuilds**.

Without a line hash the region stands in for it, under a different tag so the two forms cannot
collide. No corpus finding takes that path today, but SARIF does not promise the fingerprint, and
falling back to rule-and-path alone would silently group unrelated findings.

### The SARIF says which CodeQL wrote it, so it is checked rather than assumed

`tool.driver.semanticVersion` is compared against the `cli_version` the ledger recorded, and a
disagreement fails that repository's ingestion instead of attributing results to an analyzer that
did not produce them. Verified on the real corpus: the findings run recorded `2.26.4`, the SARIF
said `2.26.4`, zero mismatches. The check is skipped when no version was recorded, which is the
single-query lane — refusing there would stop the crawl over a check nobody asked for.

## Pending

1. **`cli-server` for the small calls.** Verified against the reference implementation in
   `codeql-development-mcp-server` (`server/src/lib/`): `codeql execute cli-server` speaks a
   NUL-delimited protocol and removes 2-5s of JVM start per call. ABOM makes roughly 38
   `resolve metadata` calls per round plus a decode per query per repository. **`database
   analyze`, `database create`, `test extract` and `test run` cannot use it** — its
   `FRESH_PROCESS_SUBCOMMANDS` set, because they spawn extractors, stream multi-event output, or
   are compound orchestration. Its CWD and environment are also fixed at startup.
2. **`query-server2` for the BQRS lane**, replacing per-repository `query run`. JSON-RPC with
   Content-Length headers.
3. **The design fork this opens.** If `database analyze` must be a fresh process anyway, then
   evaluating through `query-server2` and interpreting with `bqrs interpret` — which is not in
   the exclusion set — would remove `database analyze` from the crawl entirely and collapse the
   two lanes into one, at the cost of assembling rule metadata ourselves.
4. **Tighten the identity columns to `NOT NULL`.** Blocked, and not by code: **1,555 of 1,571**
   ledger rows still predate the identity, so the constraint would reject the corpus outright.
   The skip cannot converge this either — a pre-identity row never matches a current key, so
   every one of those pairs has to be analyzed once more. This becomes a one-line change after a
   full re-crawl, and stays wrong before one.
5. **Select and pin**: selection predicates as filters and controls, so "re-analyze what has not
   run since pack X vY" is a query rather than a whole-tier requeue; `abom_frontier.pinned_at`,
   which does not exist yet, honored ahead of `score` in `Claim`. The interface also still reports
   only what was analyzed, so a skip-heavy re-analysis shows a counter that does not move; the
   cycle log says it, the Discovery page does not.
6. **Overlay databases**, guarded. They map onto the recheck loop directly: a base keyed on
   `resolved_sha`, an overlay when the ref moves. Probe `codeql <cmd> -h -vv` and degrade to a
   full build — these are Wizards-only flags that may move without notice.

## Not doing

- **Diff-informed analysis.** It filters only queries that opt in via
  `observeDiffInformedIncrementalMode` in a data-flow configuration. ABOM's own queries are table
  queries with no data flow, and we do not author the upstream `actions-queries` we attribute
  findings to. Revisit only if upstream opts in.

## Verification

- Two consecutive full rebuilds produce identical `run_key` sets and identical normalized result
  hashes. This is the only real test of "verifiable".
- An unchanged corpus evaluates zero queries and changes no counts.
- Editing one `.ql` re-runs exactly the repositories bound to the suite that selects it.
- An overlay run and a full run agree on findings; a difference is a bug, not a saving.
