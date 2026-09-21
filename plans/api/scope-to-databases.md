# A scope names a set of databases, not only a set of nodes

**Status:** The filter language resolves to nodes and findings. Nothing resolves a scope to the
databases that would have to be analyzed to answer a question about it.

## Why this exists

One predicate already means the same thing on three surfaces: `internal/view.Spec` is parsed once,
and the graph path, the set path behind `/api/match`, and each Inventory listing apply the terms
they can enforce. A policy binds to the same language. A saved view and a gate policy are the same
object with different consequences.

What the language cannot express is the other direction. *These repositories* is answerable;
*the databases that hold these repositories' code* is not, and that is the question every
re-analysis, every new query and every simulation actually asks. Today the only way to run
something over a chosen set is to requeue a whole tier —
[`ui/tier-suites.md`](../ui/tier-suites.md) records this as its item 2, *re-analyze a selection,
not only a whole tier*, and it has no mechanism to build on.

The gap is concrete. `store.NodesMatching` returns nodes and findings. Getting from there to "run
query Q against this" needs a repository, a commit and a database that exists or can be built, and
nothing assembles those three from a `Spec`.

**This does not wait on the closure database.** A regular per-repository database is a database; the
resolution is the same work, and the answer today is a set of `(repository, commit)` pairs that the
existing pipeline already knows how to build. [`ql/closure-database.md`](../ql/closure-database.md)
adds a second kind of database to the same resolution later, selected by the `mode=` term, and
nothing here is shaped around waiting for it.

## Pending

1. **Resolve a `Spec` to a database set.** A sibling to `store.NodesMatching`, returning the
   `(repository, commit, mode)` triples a scope implies, with each one marked as already analyzed,
   analyzable, or unavailable. Depends on nothing.
2. **State the coverage of the answer.** A scope that names forty repositories of which nine have
   never been analyzed resolves to thirty-one databases and **says so**. A set that quietly omits
   what it could not reach is the absent-is-not-zero failure arriving in a new place, and it is
   exactly the number a reader needs before pressing anything expensive. Depends on 1.
3. **Cost, in the units a reader waits in.** The resolution knows how many databases exist and how
   many would have to be built; the cycle timings are already logged and surfaced nowhere.
   [`ui/tier-suites.md`](../ui/tier-suites.md) records the same requirement for tiers — *a count
   says how much work there is, never how long a reader will be waiting for it* — and this is where
   the number comes from. Depends on 1.
4. **The term for which kind.** `mode=regular` and `mode=extended` already
  [select recorded evidence](../../docs/architecture.md#analysis-modes); the same term selects which databases a
   scope resolves to. A scope asking for `extended` where no closure database exists resolves to a
   stated gap rather than silently falling back to `regular`, because the two answer different
  questions. Depends on 1.

## The constraints that must not be broken

**One language, or it is not the same scope.** A separate syntax for selecting databases would make
*what am I looking at* and *what am I about to run this against* two different questions that look
alike. Whatever terms a listing already enforces, this enforces the same way or refuses them the
same way.

**Resolution never builds anything.** This answers *which databases*, and builds none of them.
[`api/on-demand-analysis.md`](on-demand-analysis.md) is what acts; keeping them apart is what lets
a reader see the size of a thing before it happens, and what lets a simulation resolve a scope
without spending a minute of cloning.

**A database that does not exist is a stated absence, not an exclusion.** Returning only what is on
disk makes a scope silently mean something narrower than it says, and the narrowing is invisible
precisely where it matters most — a repository nobody has analyzed is the one a reader most needs
told about.

**A term the resolution cannot enforce is refused, not dropped.** The rule
[`api/filter-enforcement.md`](filter-enforcement.md) is about, arriving on a fourth side. This side
is declared with the others or it will drift from them.

## Verification

- The same predicate resolves to a node set and a database set whose repositories agree, checked by
  comparing them rather than by construction.
- A scope naming repositories with no analysis reports them as a gap, and the gap plus the resolved
  set accounts for every repository the scope named.
- The database set is stable across a delta: resolving twice with nothing changed returns the same
  triples.
- `mode=extended` against a corpus with no closure database returns an empty set and a stated
  reason, never the `regular` set.
- Cost guidance is compared against a real run's duration once, so that the estimate is calibrated
  rather than asserted.
