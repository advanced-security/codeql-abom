# A verifiable analysis ledger

**Status:** Shipped, with the last item blocked outside this repository. CodeQL invocations are
serialized machine-wide and measured at 1 during a `--concurrency 4` crawl. Every run records the
pack, its content digest and the CodeQL that evaluated it; a repository whose whole plan is
already on record is settled without being analyzed, and without being cloned, because the
reference is resolved remotely first; a reference that moved without changing anything the
extractor reads is settled without a rebuild, because content stands in for the commit; every
finding carries a content hash that two consecutive rebuilds reproduce exactly; re-processing can
be selected by identity or aimed at one repository, from the command line and from the Discovery
page; and both the small commands and the BQRS lane run against JVMs that stay up. The findings
lane still starts a process per repository and cannot safely stop doing so, overlay databases are
unavailable because the actions extractor declares no overlay support, and the identity columns
cannot be tightened while the corpus holds pre-identity rows.

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
   "the same database". This is the reason a *content* hash of a database directory can never
   enter `run_key` — see the reprocessing section below, where it has a different job.
2. **SARIF bytes vary between identical runs.** We hash a normalized projection; hashing raw
   SARIF would differ every run and be worse than useless.
3. **The CLI is identified by a version string, not a digest.** A decision, recorded as one.
4. **`resolved_sha` is a git SHA-1** for most repositories — the identity GitHub serves, and no
   stronger one is on offer.

## Done: one CodeQL invocation at a time

`runner.Lock`, taken by `database create`, `database analyze` and the BQRS lane — which means
`RunQueries` whichever route it takes, the query-server session included, because a warm
evaluator still uses the machine's cores and the compilation cache. Two halves,
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

## Done: re-processing is proportional to what moved

`RequeueStale(cliVersion, packDigests)` puts back only the done items whose repository was not
analyzed at that identity, and `RequeueRepository(name)` puts back the dependencies one
repository answers. Both are reachable: `expand-all --requeue-stale` and `--requeue owner/name`.

The skip already made an unchanged analysis free, but the checkout in front of it is not, so a
whole-tier requeue still clones every repository in the tier. Measured on the real corpus:

| | Items requeued |
| --- | --- |
| Whole tier | 1,117 |
| `--requeue-stale` | 1,110 |
| `--requeue-stale` after editing `AbomEdges.qll` | 1,117 |

The 7 excluded are exactly the items already analyzed at the current identity. That is a small
saving today and says nothing against the mechanism: 1,555 of 1,571 ledger rows predate the
identity, so almost everything genuinely is out of date. The pack-edit row is the one that shows
the chain working — moving one `.qll` moves its pack digest, and the 7 current repositories become
stale with everything else.

**The identity must come from a fresh resolution, not from the store.** `Expander.CurrentIdentity`
resolves the bound suites rather than reading what was recorded, because after a CodeQL upgrade
the most recent recorded version *is* the old one — selecting against stored state would report
the corpus as current at precisely the moment all of it went stale.

An empty identity is refused rather than matching nothing, since a caller that failed to resolve
its packs would otherwise requeue the corpus as its way of reporting a bug.

## Done: a request that does not wait its turn

`abom_frontier.expedited_at`, honored ahead of `score` in `Claim` and in the queue view.
`--requeue owner/name` sets it, which is the plan's "filter plus a pin" as one control.

On the real corpus a repository scoring **0.0** was analyzed first, ahead of **93** pending items
topping out at **2.0**, and the ranking resumed immediately after it. Among expedited items the
score still decides, because expediting everything is expediting nothing.

`Complete` clears the mark. Leaving it would let something expedited once jump the queue every
time a tier requeue put it back.

### It is not called `pinned_at`

The plan asked for `pinned_at`. In this repository a *pinned* reference already means a SHA-pinned
uses: clause — it is what `actions/unpinned-tag` reports and what `pinning_coverage.go` measures —
so a `pinned_at` on the queue would have read as "when this reference was pinned to a commit" to
every reader who met it. The behavior is the one the plan asked for; only the word changed.

## Done: the small commands stopped paying for a JVM

`runner.CLIServer` keeps one `codeql execute cli-server` alive and routes `resolve metadata`,
`resolve queries` and `bqrs decode` through it. Measured per call:

| | Own process | Through the server |
| --- | --- | --- |
| `resolve metadata` | 0.75 s | 0.004 s |
| `bqrs decode` | 0.62 s | 0.004 s |

**The plan said 2-5 s of JVM start and that was wrong** — it is about 0.7 s. The saving is
nonetheless larger than the entry implied, because the warm call is not "faster" but roughly 190
times faster, and the decode is paid once per graph query per repository rather than once a round.

On a real crawl of 15 repositories that is 45 decode process starts eliminated, about 28 s, plus
38 metadata starts a round at about 28 s each round. Verified in production rather than inferred:
60 samples during a live crawl saw **zero** `bqrs decode` or `resolve` subprocesses, with the
server up. The round itself took 311 s for 15 repositories against 109 s for 4 in an earlier run,
which is consistent but not a controlled comparison — different repositories, different clones.

### A rejected command takes the server down with it

This is the thing that shaped the design, and nothing predicted it. Asking the server for a query
that does not exist returns an **empty answer** and the process **exits 2**; the next write gets a
broken pipe. Confirmed with stderr drained, so it is the command that kills it and not a full pipe.

So on this protocol "your command was bad" and "the JVM died" are the same event, and no error
from the server can be trusted to mean either one. Every failure is `ErrServerUnavailable`, the
process is dropped, and the caller re-runs the command as its own process — which both recovers
from a genuine crash and produces the real diagnostic for a genuinely bad command. That is only
safe because everything routed here is read-only; it is why `database create` and
`database analyze` stay where they are, quite apart from the reference implementation excluding
them. The BQRS lane has since moved to a server of its own — `query-server2`, a different
protocol with the same fallback discipline — which is the section below.

### The decode buffers where it used to stream

`bqrs.DecodeToJSON` streams the CLI's stdout to a file so a result set larger than memory stays
possible. Through the server the answer is one NUL-delimited message, so it is held whole.

Affordable here on measurement, not on assumption: across the corpus the decoded graph results are
**1.3 KB mean, 5.3 KB largest**. The streaming form stays for anything that is not that.

One-shot commands are deliberately not wired to it. `build` and `selfcheck` would pay a JVM start
they have no second call to amortize.

## Done: the selection reached the interface

`POST /api/discovery/requeue-stale` and `POST /api/discovery/requeue-repository`, behind an
`out of date` button beside the tier buttons and a repository field below them. Both guarded like
Start, for the reason Start is guarded: they spend the machine's time on clones.

The selecting itself moved into `expand.RequeueStale` and `expand.RequeueRepository`, which
`expand-all` now calls too. Two implementations of "what is out of date" would disagree the moment
either grew a field, and the disagreement reads as a crawl doing too much rather than as a bug.

Measured on the real corpus — 2,800 settled dependencies, 3,917 of 5,472 ledger rows carrying an
identity:

| | Dependencies put back |
| --- | --- |
| Every tier | 2,800 |
| Out of date | 987, against CodeQL 2.26.4 and 2 packs |
| `github/codeql-action` | 218, with 221 moved ahead of the ranking |

**The section above says "that is a small saving today", and it is now wrong.** It was measured
when 1,555 of 1,571 ledger rows predated the identity, so `--requeue-stale` excluded 7 items out
of 1,117. With most of the corpus re-analyzed since, it excludes **1,813 of 2,800** — two thirds
of the clones a tier sweep would pay for. Nothing about the mechanism changed; the measurement was
taken before there was anything for it to select on.

The count carries the identity it was judged against, on the wire and on screen. `1,742 requeued`
is a number nobody can check; `against CodeQL 2.26.4 and 2 packs` is a claim anybody can. There is
deliberately **no count on the `out of date` button**, unlike the tier buttons beside it: how much
is stale is only knowable by resolving the bound packs through CodeQL, and pricing a button on
every poll would spend a subprocess a minute. It is a subset of the tiers it sits beside, and what
it selected is reported the moment it runs.

### The corpus found a third answer nobody had written

Asking for `github/codeql-action` straight after a stale sweep reported **0 requeued**, which was
correct — all 221 of its rows were already `pending`, and `RequeueRepository` only moves `done`
ones. The interface then said *nothing analyzed for it to requeue*, which is wrong twice over: the
repository is known, and the request — run this next — could still be answered by expediting rows
that are already queued. Expediting now runs whether or not anything was put back, and
`already queued, N moved ahead of the ranking` is a third answer distinct from both. Unit tests saw
none of this; the two states are indistinguishable until a real corpus has both.

The 221 against 218 is the same thing seen from the other side: three rows were already queued
before the call, and expediting covers what the repository answers rather than only what this call
put back.

### A cycle that only skipped moved no number at all

`Stats.Processed` excludes skips by construction, so a re-analysis that finds everything on record
left `Analyzed` at zero, `Edges` at zero and every queue count still. The cycle log has reported
`skipped N already on record` since the skip landed; `supervisor.Status` had nowhere to put it, so
the page showed a loop that appeared hung for as long as the re-analysis ran. `Status.Skipped`
carries it now and the state sentence says so, absent when it is zero — a standing "0 already on
record" is noise on a first crawl.

### Done: the skip is per lane, because the cost said it had to be

`pendingWork` asks the ledger about each lane separately and `prepare` runs only what is
outstanding. All-or-nothing was a deliberate deferral, justified at the time by "the database
build dominates anyway". **Measured, that was wrong.** One repository:

| Phase | Time | Share |
| --- | --- | --- |
| `database create` | 3.3 s | 13% |
| 3 x graph `query run` | ~10 s | 40% |
| `database analyze`, 35 findings queries | 11.8 s | 47% |

The build is an eighth of it. The lanes are 87%, they come from different packs with different
digests, and nothing about running one requires running the other.

**These shares are the ones that justified the split and are no longer the current ones.** The
BQRS lane has since gone from three processes to a warm session, so its 40% is largely gone and
the remaining split has not been re-measured. The argument the table was making — that the lanes
dominate the build — survives; any new claim about which phase is now largest does not, and needs
its own measurement.

On the real corpus, editing one line of `AbomEdges.qll` and re-running 10 repositories already on
record: **30 graph queries re-run, 0 findings analyses**. The four findings analyses in that pass
were all repositories being seen for the first time, checked individually rather than assumed.
Before, all 10 would have re-run the 35-query analyze — about **118 s of ~250 s** burned on
queries whose digest had not moved. An upstream `actions-queries` bump saves the mirror image.

The database is still built whenever *any* lane has work, which is the 13%. That is not avoidable:
a lane that has to run needs something to run against.

Skipping the edges lane leaves a round with findings and no BQRS, so `consume` treats a missing
`ResultPath` as ordinary rather than as a failure. That was the only thing making the split
non-trivial.

### The pre-identity backlog is not this, and is not fixable

Separately, **1,555 of 1,631** ledger rows predate the identity, covering **818** repositories
that therefore cannot skip until each has been analyzed once more. That one is not an accepted
inefficiency but an unavoidable one: those rows record that a query ran against a commit and not
what produced it, and no amount of design recovers information that was never written. Fabricating
a plausible digest would be exactly the kind of claim this subsystem exists to refuse.

## Done: the BQRS lane costs one process, then none

Two changes, in that order, because the second is only worth measuring against the first.

`Runner.RunQueries` replaced the per-query `query run` with one `database run-queries` for the
whole lane, and `expand.QueryRunner` takes the batch rather than a single query. Then
`runner.QueryServer` moved the evaluation itself into a `codeql execute query-server2` session
that outlives the repository, so the surviving process goes too.

| | Wall clock, one repository's three graph queries |
| --- | --- |
| Three `query run` processes | **11.76 s** |
| One `database run-queries` | **4.34 s** |
| Evaluation alone, as the evaluator reports it | 1.47 s |
| `codeql version`, the process-and-JVM floor | 0.72 s |

The entry that asked for this priced it at "3 x 0.7 s a repository", on the arithmetic that a JVM
start is what a fresh `codeql` costs. That was wrong by about five times: the JVM is 0.72 s of the
~3.4 s each process wastes, and the rest is library-path resolution, pack resolution, opening the
dataset and tearing the evaluator down. All of it is per *process*, which is why one invocation
recovers most of it and a session recovers the rest. It is also serialized time — `runner.Lock`
holds CodeQL to one invocation machine-wide — so this comes off the critical section four workers
queue behind, and three lock acquisitions a repository become one.

Results are byte-equivalent across all three routes: 43 / 84 / 7 tuples.

### The protocol is IDE plumbing, so it is written down and it degrades

`query-server2` is JSON-RPC with LSP framing, and the parameters go **inside a `body` envelope**
alongside a `progressId`. Sending them bare deserializes to a null and the server answers
`Internal error` with a `NullPointerException` — a message that says nothing about the shape being
wrong, and which no unit test can reach. That is why `make itest` exists: it builds a database and
runs the protocol against a real server, because nothing else can.

Every failure is `ErrServerUnavailable` and falls back to `database run-queries` in its own
process, the same discipline the cli-server established. A verdict the evaluator returns is *not*
a transport failure and does not fall back: a query it rejected would be rejected again, at the
cost of a JVM start to learn nothing.

The results directory is cleared before either route, and again before a fallback. It belongs to
the database rather than to a run, so without that a query that reported success and wrote
nothing would be answered with whatever the previous invocation left.

### Two queries with one file name are refused rather than guessed

`run-queries` names its output after the query file, so `a/Unpinned.ql` and `b/Unpinned.ql` are
indistinguishable in the results tree. The batch is refused before anything is evaluated. The
graph lane has three distinct names and the findings lane does not use this mapping, so nothing
is lost today — but attributing one pack's results to another is the failure this exists to make
impossible.

## Not doing: evaluating the findings lane through the session

The fork this opened was to evaluate through `query-server2` and interpret separately, removing
`database analyze` from the crawl. It splits cleanly — `database analyze` **is**
`database run-queries` plus `database interpret-results`, and running them separately over the
same tree produces the same SARIF, all **35 rules and 12 results**, with CodeQL still assembling
it. So no rule metadata would have to be built by hand, which is what the fork was priced on.

It fails on something else. `interpret-results` finds results by their **pack-relative path**
under `<db>/results`, and the session writes wherever it is told. Given a layout it does not
recognize it **exits 0, prints "Exported results to SARIF", and writes a SARIF with zero rules and
zero results.** Measured, by flattening a tree that had just produced 12 findings. Every
repository would have come back clean and the crawl would have looked healthy.

Reproducing CodeQL's internal layout from pack paths is the only way through, and getting it
wrong is silent. That is not a trade worth making for one process per repository on the lane that
carries every security verdict. The saving is also the smallest of the three: `database analyze`
is already one invocation for 35 queries.

What the attempt did produce is a guard. `FindingsFromSARIF` now refuses a run that names no
rules at all: searching and finding nothing is a result, searching for nothing is not, and the two
are the same row once stored.

## Done: reprocessing proportional to what changed

The skip already refuses to re-run an unchanged query against an unchanged *identity*. What it
cannot do is avoid the two costs in front of it — the checkout and the database build — because
both happen before anything is known. Closing that needs three things kept apart, and the whole
design turns on not conflating them.

### Three questions, three hashes, and one that must never be reused

| Question | Answer | Where it belongs |
| --- | --- | --- |
| Would rebuilding produce an equivalent database? | `H(repo, commit, cli_version, flags)` | `run_key`, as today |
| Is the artifact on disk still the one we analyzed? | `H(database directory contents)` | a `database_digest` column, never `run_key` |
| Has anything in scope changed since that commit? | `git diff` of the extracted paths | a pre-check before the checkout |

**The content hash must not enter `run_key`.** A database is not byte-reproducible — the section
above says so and it is why the run key hashes inputs — so two builds of the same source at the
same CLI produce different content hashes. Putting one in the run key means every rebuild
invalidates every ledger row it touches, the skip silently stops firing, and nothing looks broken:
the crawl just quietly costs what it did before the ledger existed. Its job is the narrower one
the run key cannot do — *this stored base is still the base I think it is* — which is what an
overlay has to be able to assert about the thing it sits on.

### A database digest cannot be taken over the whole directory

Analysis writes into the database. Measured on a database that had just been queried, the entries
newer than `codeql-database.yml` are **`results`, `log`, `diagnostic`, `db-actions` and
`src.zip`** — so a hash over everything changes after every analysis, and any comparison by mtime
is worthless. The digest covers the evidence and not the output: `codeql-database.yml`,
`baseline-info.json`, `db-actions/` and `src.zip`. `results/`, `log/` and `diagnostic/` are
excluded by name, and `results/` in particular is ours — `RunQueries` clears it on every pass.

`runner.PackDigest` is already this shape, sha256 over sorted `(relative path, content hash)`
pairs, and reusing it is the point: two directory-hashing rules would eventually disagree about
what a change is.

### The diff scope is not `.github/**`, and the code already knew

**Measured on `actions/checkout`: the extractor indexes `action.yml` at the repository root**,
alongside seven files under `.github/workflows/`. That root file is the *only* thing defining the
action itself, so a scope of `.github/**` would have missed every change to the definition of the
most-used action in the corpus.

The plan proposed deriving that scope from a database's `src.zip`. **That was wrong twice.** The
scope already exists as `expand.sparsePatterns` — `/.github/`, `action.yml`, `action.yaml` — which
decides what `Fetch` sparse-checks out, and it already covers the root manifest. And `src.zip`
only exists *after* a build, which is the cost the diff is meant to avoid, so it could never have
been the runtime source.

What was missing is that the filter and the extractor agreeing was an assumption. If the filter is
narrower, the database is built from an incomplete tree and every query answers from it —
silently, because a partial database is still a valid one.
`TestTheSparseFilterCoversEverythingTheExtractorIndexes` now asserts it against a database CodeQL
actually built.

**The check was nearly worthless, and catching that is the point.** Against the `AbomEdges`
fixture it passes even with the `action.yml` pattern removed, because that tree has nothing
outside `.github/` — exactly the shape that makes `.github/**` look correct. It only fails
against a source with a root manifest, verified by narrowing the rule and watching it name
`action.yml`. So `make itest` builds a second database from `internal/expand/testdata/sparse`, and
the test refuses a database that cannot exercise the rule rather than passing quietly.

### A moved reference cannot be diffed against its predecessor for free

The plan said the diff would compare the two commits before the checkout. `Fetch` is already
`clone --filter=blob:none --no-checkout --depth=1` plus a sparse checkout, so it fetches one
commit's trees for the paths above and nothing else. There is no predecessor tree to diff
against, and obtaining one is a second fetch — likely dearer than the one fetch being avoided.

So the diff belongs *after* the fetch, not before it, and what it protects is not the ~0.5 s
checkout but the database build and the query lanes behind it. The primitive that works without a
predecessor is a digest over the indexed content at analysis time: recompute it on the next pass
and an equal value means the extractor would build the same database. That is a claim about
analysis identity rather than about the queue, so it belongs with the digest work below and not
ahead of it.

### The pre-check comes before the checkout, which the skip could not

**Done.** `Expander.Probe` resolves the reference remotely, `pendingWork` is asked about that
commit, and a batch with nothing outstanding is settled without a clone. Measured on the real
corpus over the same twelve rows and the same database, requeued after being analyzed:

| | Repositories fetched | Wall clock |
| --- | --- | --- |
| Probe unset | **12** | **11.25 s** |
| Pre-check on | **0** | **4.48 s** |

Both settled twelve items and ingested nothing, which is the point: identical outcome, no clones.

This is not the trap the ledger's earlier note describes. Refusing on the *stored* sha would skip
precisely when a ref had moved; the probe asks the remote what the reference points at now, so a
moved ref produces a key the ledger has never seen and is analyzed. Not knowing is never the
answer “nothing outstanding”: a failed probe falls back to fetching, and so does a reference
mistaken for a commit, because both produce a key that is not on record.

A reference that is already a commit is not probed at all — **803 of 2,800** settled rows, which
cost nothing to decide. There is no resolution to compare against at this point, which is what
`Resolution.Mutable()` uses, so syntax is the only signal available; both ways of being wrong
degrade to fetching.

### The drift recheck was probing the wrong reference

Found while generalizing `Resolve`, and latent rather than active. `Rechecker.Run` resolved
**HEAD** for every candidate while `Recheck.Requested` — the reference the row is actually for —
sat unused beside it. A row asking about `v4` was therefore compared against the default branch:
two unrelated commits, so an unmoved tag reads as drifted, re-analyzes to the identical result,
and is due again next pass.

The corpus has not paid for it yet, and checking why was worth more than the fix. `checked_at` is
set by `Complete` at analysis time, so it does not mean “probed” — the honest signal is
`checked_at > analyzed_at`, which is **0 rows across all 2,800**. The recheck is wired and gated
on a drained queue plus a 24-hour TTL, and has simply never run here. It would have hit **800 of
the 1,997 mutable rows** on a permanent 24-hour treadmill, indistinguishable from real drift.

### An overlay pays for a moved tag, not for a different major version

The reasoning below still holds and is kept because it is what to build *if* the blocker below
lifts. `database create --overlay-changes=<file>` takes JSON with a top-level `"changes"` entry
listing relative paths changed since the base — which is exactly the diff above, so the two
halves fit without a translation layer.

Where it pays is the recheck loop: same repository, same ref name, a commit or two of drift. Where
it does not is the queue's normal shape, because the queue is keyed `(node_key, ref)` and `v1` and
`v4` of an action may share almost nothing — an overlay listing every file is a rebuild with extra
steps. So the base is per repository at the default branch, overlays are for drift, and a
first-seen reference still builds its own database.

Two further constraints are already known. `codeql database create` refuses to write into an
existing directory, so a rebuild replaces rather than updates. And `QueryServer` keeps a dataset
registered while it evaluates, so a base must be deregistered before it is replaced or removed;
`Evaluate` already deregisters per batch, which is what makes that safe.

### Overlays are blocked by the extractor, and the CLI does not say so

**The actions extractor cannot build an overlay base on CodeQL 2.26.4.** Overlay support is
declared per extractor by `overlay_support_version` in `codeql-extractor.yml`. Seven of the
bundled extractors declare `20250626` — cpp, csharp, go, java, javascript, python, ruby. `actions`
does not, and neither do rust, swift, html, yaml, properties, xml or csv.

The flag is accepted anyway. `codeql database create --language=actions --overlay-base` exits **0**
and prints *Successfully created database*, then writes:

```yaml
overlayBaseDatabase: false
overlayDatabase: false
```

The same command against a javascript source writes `overlayBaseDatabase: true` and an
`overlayBaseSpecifier: 20250626:…`. That pair is the positive control: the flag works, the
extractor is the gate, and for actions the flag is a silent no-op.

Two details cost a round of guessing and are recorded so they are not re-derived. `--overlay-base`
is a **boolean** — it marks the database being created, it does not point at one — so the plan's
reading of it as a path was wrong. And neither overlay option appears in `database create --help`;
the only overlay text there belongs to `--cache-cleanup`. `--overlay-base-database`,
`--overlay-database`, `--overlay-base-path` and `--overlay` are all rejected as unknown options.

`TestTheActionsExtractorStillCannotBuildAnOverlayBase` asserts this under `make itest`, against a
database built with the flag. It was verified by pointing it at the javascript base and watching
it fail. **That test failing is good news** — it means the blocker has lifted.

### The build is a fifth of a repository, not an eighth

The question the plan left open. Measured on `actions/checkout`, three runs per phase, warm
figures for the two lanes because that is what a crawl pays after the first repository:

| Phase | Median | Share |
| --- | ---: | ---: |
| Fetch | 3.68 s | 24.1% |
| Database build | **3.11 s** | **20.3%** |
| Graph lane | 3.95 s | 25.8% |
| Findings lane | 4.56 s | 29.8% |
| Total | 15.30 s | |

The build was **3.3 s of 25 s (13%)** when the lane split was first measured. It has not got
slower; everything around it got faster, so its share nearly doubled. That makes overlays worth
more than the original ranking assumed, and worth nothing at all until the extractor declares
support.

The two mechanisms that did land attack the other 80%. The pre-check removes the fetch — 24.1% —
when nothing is outstanding. Content equivalence removes the build and both lanes — 75.9% — when
a reference moved without touching anything indexed.

### Content stands in for the commit

Done. `abom_commit_content` records, per repository and commit, a sha256 over every file the
fetch produced — path-prefixed and length-delimited, `.git` excluded because its packfiles differ
between two clones of one commit and would make every digest unique and the check inert.
`QueryRunExistsForContent` matches every part of the run identity *except* the commit, joined on
that digest.

The argument is the one `QueryRunExists` already makes, with the commit replaced by the thing the
commit was standing for. An action's release moves a tag over a rebuilt `dist/`, which the sparse
fetch does not take, so the new commit's database is byte-identical to the old one's and CodeQL
is deterministic.

Nothing is written for the new commit. Copying the ledger rows forward would record a run that
did not happen, and putting the digest into `run_key` would invalidate all 5,472 existing rows to
express something the join expresses for free.

Measured against the corpus itself — the 480 commits ABOM has actually built databases for,
across the 30 repositories with more than one:

| | Commits | Distinct content | Rebuilds avoided |
| --- | --- | --- | --- |
| 30 repositories | **480** | **427** | **53 (11.0%)** |

Each avoided rebuild is a database build plus both query lanes; the fetch is still paid, because
the content is not knowable without it. `actions/cache` is 15 commits over 9 distinct trees,
`actions/checkout` 36 over 27, `actions/upload-artifact` 18 over 12. Fourteen of the thirty gain
nothing, which is the honest shape: the saving is concentrated in actions that release often and
change their workflows rarely.

The same measurement over release *tags* rather than analyzed commits runs higher — **16 of 60**
across ten popular actions, with `github/codeql-action` v4.37.7/.8/.9 sharing one tree and five of
six recent `actions/cache` tags sharing another. The corpus number is lower because the refs ABOM
follows spread over more distinct content than consecutive releases do.

The Discovery *suites* column had to move with it. It joined a queue row to runs at its own
`resolved_sha`, and a row settled by equivalence has none — so it would have answered *not
recorded* for a repository that is analyzed, inventing a coverage gap out of an optimization.
`runsForRow` now also matches through the content table, with both content rows constrained to
the same repository so an identical tree in a fork still joins to nothing.

### Order

Everything in this plan that can be built has been. The pre-check, the fetch-scope assertion and
the indexed-content digest are done. Overlays are blocked by the extractor rather than by
sequencing, and the database digest goes with them: its only job is asserting that a stored base
is still the one we analyzed, and nothing stores a base until overlays do — a column with no
consumer is the `internal/archive` mistake with a shorter name.

## Pending

This file has grown to cover two separable concerns: what makes a run attributable, and what
stops one running at all. They are kept together because the second reads the first as an input —
the skip is a question asked of the identity — rather than restating it. Split them when the
identity half gains pending work of its own.

Neither item below is blocked by code. Both are waiting on something outside this repository, and
both have a check that fires when the wait ends.

1. **Tighten the identity columns to `NOT NULL`.** Blocked by data: **1,555 of 5,472** ledger rows
   still predate the identity, so the constraint would reject the corpus outright. The skip cannot
   converge this either — a pre-identity row never matches a current key, so every one of those
   pairs has to be analyzed once more. The share is falling on its own (it was 1,555 of 1,571),
   because the count is of rows that will never gain one rather than of rows that lack one. A
   one-line change after a full re-crawl, and wrong before one.
2. **The database digest and overlay databases**, together for the reason the order section gives.
   Blocked by the extractor: `actions` declares no `overlay_support_version`, so `--overlay-base`
   is a silent no-op. `TestTheActionsExtractorStillCannotBuildAnOverlayBase` fails when that
   changes. The build is now **20.3%** of a repository's cost, so this is worth doing on the day
   it becomes possible.

## Not doing

- **Diff-informed analysis.** It filters only queries that opt in via
  `observeDiffInformedIncrementalMode` in a data-flow configuration. ABOM's own queries are table
  queries with no data flow, and we do not author the upstream `actions-queries` we attribute
  findings to. Revisit only if upstream opts in. This is about *CodeQL's* mechanism and says
  nothing against our own pre-check, which decides whether to run a query at all rather than
  narrowing one that is already running.

## Verification

- Two consecutive full rebuilds produce identical `run_key` sets and identical normalized result
  hashes. This is the only real test of "verifiable".
- An unchanged corpus evaluates zero queries and changes no counts.
- A reference that has not moved is settled **without a checkout**, and the count of clones a
  round performs is the number that has to fall.
- A commit whose indexed content matches one already analyzed is settled without a database
  build, and one whose content differs is not.
- The fetch scope is asserted against a database CodeQL actually built, using a source tree with
  a root `action.yml`, so a filter narrowed to `.github/**` fails rather than passing quietly.
- Editing one `.ql` re-runs exactly the repositories bound to the suite that selects it.

Deferred with the overlay work, since neither can be exercised until the extractor supports it:

- Two consecutive builds of the *same commit* produce **different** database digests and the
  **same** run keys. That pair is the whole guard against the content hash leaking into the
  identity, and it fails loudly in exactly the case that would otherwise retire the skip in
  silence.
- An overlay run and a full run agree on findings; a difference is a bug, not a saving.
