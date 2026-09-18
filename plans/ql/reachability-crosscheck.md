# Reach is computed one way, and nothing checks it

**Status:** Blocked on [`ql/closure-database.md`](closure-database.md).

## Why this exists

Reach orders the crawl, sizes every node the Explorer draws, and is half of the arithmetic
remediation is prioritized by. It is produced by exactly one thing: an in-memory breadth-first walk
in `internal/graph` over the edge table. If that walk is wrong, every number downstream is wrong in
the same direction, and nothing anywhere would disagree with it.

That is the same gap [`api/dependency-graph-crosscheck.md`](../api/dependency-graph-crosscheck.md)
exists to close for extraction — our reading of the first hop has never been compared with anybody
else's. This is the same argument one level up: our reading of the *closure* has never been
compared with anything either.

An owned repository's closure database makes a second derivation available for the first time. The
same edges, walked by CodeQL rather than by Go, over a database that already holds the whole closure
— a genuinely independent path to the same number, sharing no code with the first.

**One closure per owned repository is a better shape for this than one database for the account**,
and not only because the account variant is refused. A closure is pinned at the references its root
repository actually uses, so the two walks are comparing the same version of the same supply chain.
A whole-account database would collapse **132 of 225** dependencies to a single reference and then
disagree with the Go walk for a reason that is about the layout rather than about reach.

## Pending

1. **A `@kind graph` query producing the closure.** Over one owned repository's closure database,
   one result set per closure. Depends on `closure-database`.
2. **Compare, and name what disagrees.** Per node: dependents according to `abom_blast_radius`,
   dependents according to the query, and the set difference in both directions. Depends on 1.
3. **Report the comparison where a reader can see it.** A disagreement nobody looks at is not a
   check. Depends on 2.

## The constraints that must not be broken

**The crosscheck never writes.** `abom_blast_radius` and `abom_node_reach` are produced by the
aggregate pass and by nothing else. A second derivation that silently corrects the first leaves two
readings and no way to tell which is on screen, and the disagreement — the only thing this plan is
for — disappears at the moment it is found.

**Rows that disagree are named, never reconciled.** Carried from
[`db/edge-facts.md`](../db/edge-facts.md), which states it for the same reason: the interesting
output of a comparison is the disagreement, and a pass that quietly picks a winner deletes it.

**A difference is a question, not a defect.** The two walks do not see the same corpus. CodeQL sees
what was extracted; the Go walk sees every edge ever ingested, including from repositories no longer
fetched. Coverage has to be subtracted before a difference means anything, and a crosscheck that
reports its own coverage gap as an error will cry wolf until nobody reads it. Two narrower sources
of the same kind of gap are known and named rather than investigated each time:
[`ql/closure-database.md`](closure-database.md) stages one reference per dependency, so a dependency
named twice inside one closure is walked once; and a staged dependency's own `./` local references
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
