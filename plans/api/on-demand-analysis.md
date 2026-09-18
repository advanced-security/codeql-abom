# Run these queries, against this scope, now

**Status:** The only way to run anything is to requeue a whole tier and wait for the crawl to reach
it. There is no way to ask for a specific query against a specific set.

## Why this exists

What ABOM runs is decided by `abom_tier_suite`: three tiers, each bound to a set of suites, applied
by the crawl to whatever it claims next. That is the right shape for keeping an organization
current, and it is the wrong shape for every question a person actually arrives with — *does this
pattern exist anywhere in the payments repositories*, *did last week's change to this rule alter
what it finds*, *what would this new query do*.

Those are the questions [`ql/query-authoring.md`](../ql/query-authoring.md) is about creating, and
they are unanswerable while a query can only reach a database by being bound to a tier and waiting
for a cycle. A person writing a query needs to run it, look at what came back, change it and run it
again — and a crawl is not a feedback loop.

The seam already exists. [`api/scope-to-databases.md`](scope-to-databases.md) turns a predicate into
the databases it implies, [`db/query-catalog.md`](../db/query-catalog.md) makes a query a thing that
can be named and selected, and `internal/expand` already knows how to build a database, run a plan
over it and ingest what comes back. What is missing is a caller that is not the crawl.

**This is not blocked on the closure database.** The databases it runs against are whatever
[`api/scope-to-databases.md`](scope-to-databases.md) resolves, which today is the regular
per-repository shape and is enough to be useful.

## Pending

1. **A run is a record before it is an execution.** Selected queries, resolved scope, requested
   mode, who asked, and the resolved database set with its coverage gap — written down first, so a
   run that dies halfway is a run somebody can see the remains of. `abom_query_run` records what an
   analysis *did*; this is the thing that asked for it. Depends on the scope resolution and the
   catalog.
2. **Execution over the existing lanes, not beside them.** The BQRS and findings lanes in
   `internal/expand` resolve a plan, run it and ingest the results; an on-demand run supplies a
   different plan and a different database set to the same code. **A second execution path is the
   failure mode to avoid** — it would drift from the crawl's, and the two would disagree about what
   a query means with nothing to say which was right. Depends on 1.
3. **It competes with the crawl rather than racing it.** Every `codeql` invocation shares one local
   compilation cache and `internal/runner` already takes a machine-wide lock. An on-demand run that
   starves the crawl, or that a crawl starves indefinitely, is a worse experience than one that
   queues visibly. Depends on 2.
4. **The action belongs to the selection.** *Run these queries against these* joins the exports and
   *show these in the graph* above the Inventory unit strip, because it is the same verb over
   whatever a reader has just narrowed to, and not a destination of its own. It states the size and
   the cost of what it is about to do before it does it. Depends on 3.
5. **A run is watchable and stoppable.** Minutes of cloning and database building that nothing can
   take back, which is the same warning [`ui/tier-suites.md`](../ui/tier-suites.md) already carries
   for tier re-analysis. Depends on 4.

## The constraints that must not be broken

**An on-demand run writes to the same tables as a crawl, with the same keys.** A result that lands
somewhere separate would need every reader taught about a second source, and the first reader that
was not taught would show a number missing half its inputs. The run is recorded as the thing that
asked; the results are ordinary results.

**Resumability is not weakened.** The crawl's queue and ledger stay per `(dependency, reference)`.
An on-demand run does not claim frontier rows and does not mark anything analyzed that the crawl
would otherwise reach — it is a reader with a different plan, not a second writer to the queue.

**Nothing leaves the machine.** No result of an on-demand run is uploaded, submitted or posted
anywhere. Findings stay in ABOM; the route out is publishing a pack, which
[`ql/pack-promotion.md`](../ql/pack-promotion.md) owns and which a person performs deliberately.

**The skip decision still applies, and is still visible.** A run over a database whose analysis is
already recorded is a run that says so rather than one that silently repeats the work or silently
returns nothing. `expand.pendingWork` already decides this; an on-demand run that ignored it would
make re-running a query the only way to be sure it ran.

**A run nobody asked for is a defect.** No schedule, sweep or retry may start one. The crawl is the
thing that runs on its own; this is the thing a person presses.

## Verification

- A query added to the catalog runs against a chosen scope and lands attributed results, with no Go
  change — the same acceptance test [`api/query-suites.md`](query-suites.md) sets for the mechanism,
  now reachable from the interface.
- The run record accounts for every database in the resolved set: analyzed, skipped, failed or
  unavailable, summing to the set size.
- A run and a crawl cycle overlapping produce the same rows either would have produced alone,
  asserted on a fixture corpus rather than by observation.
- Killing the process mid-run leaves a run record a reader can interpret, and leaves the frontier
  exactly as it was.
- Cost stated before a run is compared to the cost it took, once, so the estimate is calibrated.
