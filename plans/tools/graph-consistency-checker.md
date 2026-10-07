# Check the graph against the world, continuously

**Status:** the internal checks 7 to 9 run as `codeql-abom check`, filing results, a per-build trend
and a disagreement queue behind `/api/checks`; every check that spends API budget or CodeQL is not
built, and says so. The source-extraction comparison check 1 generalizes is planned in
[`api/dependency-graph-crosscheck.md`](../api/dependency-graph-crosscheck.md).

## Why this exists

Whether ABOM's graph is right is learned days or weeks after a change, by somebody noticing, on an
organization about one percent of the size it is meant for. This week's sample found **149 of 300**
rows sighted at more than one commit naming the wrong target for one of them — found by hand,
months after the model that caused it shipped. Every defect that reached the interface in recent
memory was found by driving the real thing against real data; nothing does that on its own.

ABOM is meant to represent an enterprise's supply chain with full fidelity, at up to 100,000
repositories per organization. "Full fidelity" cannot be shown by sampling, but an error rate can be
bounded and every class of disagreement a check covers can be made to surface within hours of the
build that introduced it. That is what this process is for: **a measurement of ABOM's own
correctness, running beside ABOM, feeding development until the project is ready for that scale.**

## Decisions

- **A separate process inside the same binary**, with its own mode, its own token pool and meter
  ([pools](../../docs/architecture.md#the-pipeline)), reading the store — a replica, on
  PostgreSQL — and writing only its own tables. It never writes anything the graph reads.
- **Report only.** Per-build fidelity trends and a disagreement queue; no release gate.
- **Run logs are transitive ground truth**, sampled under the checker's pool, once it has been
  measured which downloads the runner actually logs.
- **An agent investigates, when a person asks.** A Copilot SDK session started by a person from a
  disagreement, confined as authoring sessions are, with its own credit pool and meter. It leaves a
  reproduction bundle and a draft failing regression fixture in its workspace for a person to review,
  and writes nothing to GitHub.
- **Efficiency is measured on real organizations** as they become available. There is no synthetic
  enterprise; the checker's meters and lag measurements are the instrument when one arrives.

## The checks

Each compares one claim with one independent reading **at the same commit**, and a pair that cannot
be matched is skipped and counted rather than compared.

| # | ABOM's claim | Independent reading | A disagreement can mean |
| --- | --- | --- | --- |
| 1 | What a commit declared | The same commit's files, read by an independent parser | extraction, normalization |
| 2 | An owned repository's direct dependencies | GitHub's dependency graph, at the default-branch head only | coverage, parser, a producer other than GitHub |
| 3 | What a reference resolves to | The reference now | staleness |
| 4 | Which commits of an owned repository are live | Its default and protected branch heads now | staleness, a missed event |
| 5 | A workflow's closure at a commit | The actions that commit's run downloaded, with their SHAs | coverage, resolution, derivation |
| 6 | An owned repository's reach | A CodeQL walk over its closure database, sharing no code with the Go walk ([`ql/reachability-crosscheck.md`](../ql/reachability-crosscheck.md)) | derivation, coverage |
| 7 | Incrementally maintained reach, exposure, closure | The wholesale recompute | derivation |
| 8 | Exposure, closure and reach | Each other, on every owned repository | derivation |
| 9 | Graph-wide invariants | — | derivation: a walk reading unknown content, a live commit with no evidence unreported, units that do not reconcile, a location a starting point reaches whose target changed kind between commits |
| 10 | The freshness each pool's estimate shows | The lag checks 3 and 4 measured | an estimate that is not one |
| 11 | Work put back is done when claimed | The ledger after the cycle that claimed it | two definitions of done that disagree: a loop |

Checks 1–5 spend API budget and check 6 spends CodeQL capacity, so all six are sampled; 7–9 and 11
spend neither and run exhaustively where affordable.
Samples take **what changed most recently first**, so a regression shows within hours of the build
that caused it, and a **stratified random baseline** whose bound is stated: zero disagreements in 300
samples bounds the rate below 1% at 95% confidence, and is reported as that, not as "correct".

Every disagreement keeps a reproduction: repository, commit, path, both readings, ABOM build, reader
versions. It is classified by rule, never by the agent.

## Pending

1. **The checker's pool and meter.** The token pools are built, with a `consistency checker`
   pool in worker-seconds that nothing draws from yet; the checker's reads and evaluations draw from
   it.
2. **The external checks, 1 to 4.** Check 1 is the comparison
   [`api/dependency-graph-crosscheck.md`](../api/dependency-graph-crosscheck.md) plans as a one-off,
   run continuously; check 2 carries its rules about producers and default branches. Depends on
   the checker's pool and meter.
3. **Measure what run logs record, then check 5.** Before any run log is compared, find out which
   downloads job setup logs — nested composite steps, reusable workflows, Docker actions, actions a
   self-hosted runner already holds, steps whose `if:` was false — on consented repositories.
   Whatever it does not log is excluded from the check and named, not treated as absent. Depends
   on the checker's pool and meter. Run logs read with repository Actions read, which the App holds
   for this item. Each completed `workflow_run` is already kept in `abom_workflow_run` with the
   reusable workflows it called at their commits, and `/api/checks` shows that frame on check 5:
   which runs exist to sample, and since when.
4. **Check 6 as a sample, once the reachability crosscheck reports its comparison.** It is that
   plan's walk, run continuously over sampled owned repositories rather than once.
5. **The agent.** A session from a queue entry, under the authoring sessions' confinement, its own
   credit pool, and a workspace holding the reproduction and a draft failing fixture.
6. **The interface.** The queue, the per-build trends and the meters, beside the pools the operator
   sets.
7. **Check 11, from the queue and the ledger alone.** A row put back for a reason and settled
   without a new run, twice, is a loop: two definitions of done that disagree, with the crawl's
   claims spent on them. Re-observation and the skip disagreed about **97 commits for 3.7 hours** on
   the crawled organization — every claim spent on them, nothing ingested, and every cycle logged
   as progress because it settled something. The disagreement is the finding; which definition is
   right is the agent's to investigate, never the check's to repair.

**What the built checks cost**, on a copy of the crawled organization: a run takes about 36 s and
half a gigabyte. Check 7 is one wholesale rebuild on a snapshot (about 21 s) beside the incremental
derivation on another; check 8 is about 14 s, nearly all of it two submission closures per owned
repository at about 245 ms each, so it grows with what is owned and its closures should share one
read of the live commits before an organization of thousands; check 9 is under a second. Checks 7
and 8 read snapshots taken with `VACUUM INTO`, so the checker never writes a graph table and never
migrates the database it reads.

## The constraints that must not be broken

**A disagreement is a question, never a correction.** Neither reading overwrites the other, and
nothing the checker finds is written into a table the graph reads.

**The checker cannot change what it measures.** It spends only its own pool, so a busy checker
cannot starve discovery and make freshness look worse than it is, and it writes nothing a walk reads.

**An unread source is unknown, never agreement on zero.** A dependency graph that is disabled, a log
that expired and a token without the scope all leave the sample unread, and say why.

**Say which checks are not running.** A check family that is off reports as off. Zero disagreements
from a check that never ran reads like health.

**What an agent reports is not evidence, and a session is started by a person.** The agent's
classification is a suggestion beside the rule's; its fixture is a draft until somebody runs it.

## Verification

- Run over the crawled organization's snapshot from before per-sighting content, check 1 or 5 finds
  the `pre-commit/action` divergence found by hand, without being pointed at it.
- A mutant build that drops one kind of declaration is caught by the next sample of recently changed
  commits, and the per-build trend shows when it started.
- With the checker stopped, every other pool's meter is unchanged.
- With events disabled, check 4 measures the lag the 24-hour sweep leaves on owned branches. The
  sweep runs only once nothing reached is unanalyzed, and a copy of the crawled organization queued
  98 dependencies never queued before on its first drained cycle, which held it off.
- Check 10 agrees with checks 3 and 4 per pool on the crawled organization, or names the pool whose
  estimate differs from the lag measured.
