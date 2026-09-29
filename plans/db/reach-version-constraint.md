# Reach counts a repository that pins a version without the dependency

**Status:** exposure stays inside the pinned version; reach does not, and reach is what orders the
crawl.

## Why this exists

Three walks answer one question from different ends. The submission closure asks what a repository
depends on and constrains a dependency's children to the reference that repository pinned, matching
`abom_frontier.resolved_sha` to the run that produced the edge. Exposure asks which repositories
reach an affected action and now carries the same constraint. **Reach asks how widely a dependency
is depended on, and carries none of it**: it walks dependents through `container` with no notion of
which version a route was read from, so a repository pinning v4 counts toward a dependency that only
enters at v5.

This is not a corner. **322 of 377 dependencies on the crawled organization are analyzed at more
than one reference, and 300 resolved to more than one distinct commit.** Every one of those is a chance
for a dependent count to include somebody who does not have the dependency.

What makes it worth attention rather than a footnote is where the number goes. Commonality decides
the order the crawl claims work in — `Queue.Rescore` reads it, `Claim` orders by it within a depth —
and it reaches the interface as `QueueScore` and the Explorer's *used by ≥* filter. An inflated
count does not show up as a wrong answer; it shows up as the crawl spending its budget in the wrong
order, which nothing would ever report.

## Pending

1. **Decide whether reach should be version-constrained at all.** This is a real fork, not an
   oversight to correct. Reach answers a different question from exposure, and for *crawl ordering*
   the union across versions may be the right reading: deciding whether to analyze a dependency at
   all is a question about the dependency, not about one consumer's pin. The alternatives are
   **constrain it**, so every count means "repositories that actually have this"; **leave it
   unconstrained and say so**, renaming what it measures to something honest like breadth of
   reference; or **keep both**, an unconstrained figure for ordering and a constrained one for
   anything shown to a person. The third is the most work and the only one that serves both readers.
2. **Carry the constraint, if item 1 says to.** The mechanism already exists and is proven:
   `model.reverse` carries the producing commit and the reference as written, and `model.resolved`
   maps a dependency's reference to its commit. Reach would adopt the same per-hop rule exposure
   uses. `abom_node_reach` now materializes reach as `(node, repository, hops)` written from the
   same walk, so the third alternative in item 1 — keeping both readings — costs a column rather
   than a second traversal. Depends on 1.
3. **An agreement test covering the third walk.** `exposureagreement_test.go` holds two walks
   together and reach is outside it, which is how this survived the last round. Whatever item 1
   decides, the test states it, so the next person finds a decision rather than an inconsistency.

## The constraints that must not be broken

**A number used for ordering is still a number somebody reads.** "It only feeds the scheduler" stops
being true the moment it appears on a screen, and `QueueScore` already does. Either it means what a
reader will assume, or it is renamed.

**Three walks over one graph must agree or differ on purpose.** Two of them now agree because a test
says so. A third that quietly differs is worse than one that differs loudly: the disagreement is
invisible until somebody compares two figures and cannot tell which is wrong.

**A reference with no recorded commit constrains nothing.** An unanalyzed version is something ABOM
has no reading of, not a version with no children. Any constraint added here inherits that rule, or
it will silently drop real dependents on a corpus that has not finished crawling.

## Verification

- Blast radius rebuilt on a crawled organization, before and after, with the difference attributed
  to specific dependencies rather than reported as a total. Today that table holds **448 rows** with
  a summed dependent count of **155**.
- A dependency analyzed at two references, where a consumer pins the older, is counted once and by
  the right consumer. This is the same shape as `pinnedVersionFixture`.
- Claim order on a real frontier before and after. If ordering does not move at all, either the
  constraint is not binding or commonality was not deciding, and both are worth knowing.
