# Reach is computed one way, and nothing checks it

**Status:** Unblocked. Closure databases are built (`codeql-abom closure`,
[analysis modes](../../docs/architecture.md#analysis-modes)), and nothing here has started.

## Why this exists

Reach orders the crawl, sizes every node the Explorer draws, and is half of the arithmetic
remediation is prioritized by. It is produced by exactly one thing: an in-memory breadth-first walk
in `internal/graph` over the edge table. If that walk is wrong, every number downstream is wrong in
the same direction, and nothing anywhere would disagree with it.

That is the same gap [`api/dependency-graph-crosscheck.md`](../api/dependency-graph-crosscheck.md)
exists to close for extraction — our reading of the first hop has never been compared with anybody
else's. This is the same argument one level up: our reading of the *closure* has never been
compared with anything either.

An owned repository's closure database makes a second derivation available for the first time: the
same `uses:` lines, walked by CodeQL rather than by Go, sharing no code with the first. A closure
stages only callables — composite actions and reusable workflows — so the CodeQL walk names every
`uses:` reachable through them and stops at each JavaScript or Docker action, which it names and
cannot enter. Those are the Go walk's leaves too, so the comparison is over the same closure; what
it cannot compare is anything beneath a leaf, and nothing ABOM analyzes is.

**One closure per owned repository is a better shape for this than one database for the account.**
A closure is pinned at the commits its root repository actually runs, so the two walks compare the
same version of the same supply chain. A whole-account database also resolves `./` references
across owned trees, and would disagree with the Go walk for a reason about layout rather than
reach.

## Pending

1. **A `@kind graph` query producing the closure.** Over one owned repository's closure database,
   one result set per closure, run by path as the closure integration test runs
   `AbomResolvedCallees.ql`, which pins the resolution this walk follows. Depends on nothing.
2. **Compare, and name what disagrees.** Per node: dependents according to `abom_blast_radius`,
   dependents according to the query, and the set difference in both directions. Depends on 1.
3. **Report the comparison where a reader can see it.** A disagreement nobody looks at is not a
   check. Depends on 2.
4. **Run it continuously, as check 6.** The consistency checker runs the walk over sampled owned
   repositories, on its own pool and CodeQL capacity, and files what disagrees as its other checks
   do: a question with its reproduction, never a correction. `codeql-abom check` already lists
   check 6 as planned here. Depends on 2.

## The constraints that must not be broken

**The crosscheck never writes.** `abom_blast_radius` and `abom_node_reach` are produced by the
aggregate pass and by nothing else. A second derivation that silently corrects the first leaves two
readings and no way to tell which is on screen, and the disagreement — the only thing this plan is
for — disappears at the moment it is found.

**Rows that disagree are named, never reconciled.** The interesting
output of a comparison is the disagreement, and a pass that quietly picks a winner deletes it.

**A difference is a question, not a defect.** The two walks do not see the same corpus. CodeQL sees
what was extracted; the Go walk sees every edge ever ingested, including from repositories no longer
fetched. Coverage has to be subtracted before a difference means anything, and a crosscheck that
reports its own coverage gap as an error will cry wolf until nobody reads it. Two narrower sources
of the same kind of gap are known and named rather than investigated each time: a closure selecting
two commits of one staged composite action or reusable workflow is refused, so that owned
repository has no CodeQL answer at all, and neither has an owned repository the crawl never
analyzed (9 of 28 on the crawled organization); and a staged dependency's own `./` local references
resolve to nothing, so CodeQL's walk stops where the Go walk continues.

**Absence on one side is not zero on the other.** A node no closure database ever extracted has no
CodeQL answer, which is not the answer zero.

## Verification

- On a corpus where both derivations are complete, they agree for every node, and that run is what
  makes a later disagreement worth investigating.
- Seeded disagreement: remove one repository from the extraction set and confirm the comparison
  reports it as a coverage difference rather than as a reach difference.
- The comparison is re-run after the aggregate pass, not during it.
- Cost is recorded. A check that doubles the price of a crawl has to earn it, and if it cannot it
  runs on a period rather than per cycle.
