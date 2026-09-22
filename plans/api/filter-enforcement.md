# A term is enforced or refused, and today one is neither

**Status:** every term in the language is applied by the server, applied by the client, or refused
by name, on both the set path and the graph. Every endpoint that can be bounded to a repository
grouping is. A control reads the values a term can take rather than asking for them to be typed.
What remains is a second narrowing mechanism inside policies, a term the language cannot spell, a
consumer that applies more than it asks for, four list terms whose members nothing declares, and a
value list that does not say what it leaves out.

## Why this exists

A consumer that accepts a predicate and applies part of it enforces something wider than the reader
saw. The set path did exactly that, and the widest version of a predicate is the one that blocks a
pull request.

The eleven endpoints that read their own parameters now refuse a term they do not apply. Eight apply
`properties`; the three that do not are below, and one of them has no stated reason.

## Pending

1. **Retire `abom_policy.scope_kind` and `scope_value`.** They are a second narrowing mechanism,
   evaluated by different code, for what a predicate now says directly — and two narrowings that
   must agree are how two policy engines start. Existing unit-scoped policies migrate into
   predicates. `plans/api/policy-language.md` once chose the other direction, when a predicate could
   not name a property key; it can now. Depends on nothing.
2. **The language cannot say that a list is empty.** An absent term and a deliberately emptied one
   write identically, so a consumer whose default for a term is non-empty has to read silence as
   its default — and most links into a surface name three terms out of seventeen. The Explorer
   therefore cannot share "every finding flag off". Either an empty list gets a spelling, or the
   rule that silence means the consumer's default is stated and every consumer follows it. Both
   halves are `internal/view` before they are anything else. Depends on nothing.
3. **Name the side that applies each term, per consumer.** `graphTerms` says the graph applies
   `origins`, `minReach`, `ref`, `flags` and `boundary`, and it does — when it is sent them. The
   Explorer sends none of them, filtering the one payload itself so an interaction costs no round
   trip. **The two agree**: on the crawled organization the server and the browser select identical
   node sets for `minReach` at 5 and 20, `origins=seed`, `ref=tag` and `ref=sha` — 0 rows either way.
   So this is a declaration to write down rather than a divergence to repair, and a test that
   re-measures it is what keeps it true. Depends on nothing.
4. **A list term either declares its members or cannot offer them, and four terms are stuck between.**
   `kinds`, `origins`, `flags` and `edges` are lists whose members nothing checks, which is the same
   fact seen from two sides: the server accepts `kinds=banana` and narrows to nothing, and a control
   has no list to show because no declaration says what the members are. `rules` and `severity` were
   solved by reading the corpus, and `kinds` now is too — but a corpus-derived list is *what has been
   used*, never *what is allowed*, so it cannot be the thing a refusal is checked against. The fork:
   declare a vocabulary for all four in `internal/view` and refuse a member outside it, which makes a
   saved view naming a retired kind start failing; or state that these four are deliberately open and
   that a control offers the corpus as a convenience. Whether partial validity in a list refuses or
   warns is the same decision, and `origin` from [the query catalog](db/query-catalog.md) inherits it.
   Depends on nothing.
5. **A corpus-derived value list says what it covers, or it overstates it.** `provider` offers one
   value, `CodeQL`, counting **884 of 11,806 findings**: the rest predate the column and are
   unattributed, and are correctly left out rather than relabelled. Both numbers are honest and the
   screen shows only the first, so filtering by the only provider on offer silently answers for a
   fourteenth of the corpus. This is the rule already stated below for property keys — *every
   filtered answer reports the owned repositories that carry no value for the key it filtered on* —
   applied to a term whose values come from findings rather than from repositories, and it wants
   stating once for both rather than twice. The count of rows a term cannot speak for is the
   measurement to surface; the store leaves it out of the list on purpose, so it has to travel
   beside it. Depends on nothing.

**Two endpoints refuse it for a reason and should keep refusing.** `/api/properties` enumerates the
groupings, so bounding it by a grouping is circular; `/api/frontier` reports crawl queue state for
repositories that may not be owned or even known yet, and so have no properties to match. Neither is
an oversight.

**`/api/graph` is the third, and nobody wrote down why.** The focus walk takes `focus`, `hops`,
`max`, `direction`, `granularity` and `expand`, and refuses the property terms — while
`/api/graph/full`, which parses the shared predicate, applies them. *Show me what this node reaches,
within my business unit* is a question somebody will ask. Either the reason is stated here or the
terms are added, and finding out which is part of item 3.

## The constraints that must not be broken

**The restriction belongs before the aggregation, never after it.** A report whose rows are
repositories narrows by dropping rows. A report whose rows are advisories, with counts of affected
repositories, narrows by *recounting* — and a restriction applied to the finished rows leaves each
count spanning the whole organization while the row list looks correctly bounded. Measured on the
crawled organization: bounding remediation to one grouping takes an advisory from 9 affected
repositories to 3. A version reading 9 would have been wrong in a way nothing but arithmetic catches.

**An empty answer is an empty list, never nil.** A bounded answer can be legitimately empty, which an
unbounded one over a populated corpus never was — so a slice left at its zero value serializes as
`null` and the interface throws instead of rendering nothing. The filter did not introduce this; it
made it reachable.

**Refusing is not a fallback to matching everything.** An empty selector matches nothing, deliberately:
a gate whose policy failed to parse would otherwise fail every pull request instead of none. A
refusal added here must not become the widest possible answer by another route.

**A term is applied by the server, applied by the client, or refused.** There is no fourth state,
and the third is what `repositories` was in: serialized into shareable links by the interface,
applied by nothing, answered with the whole corpus. Client-applied is a real category — the finding
terms sever 74% to 99% of their own match's edges when applied per node, so the Explorer filters one
payload instead — but a caller that is not the Explorer has to be told it was handed the work.

**One evaluation, never two.** `TestASimulationAndTheMatchEndpointAgreeOnTheSameInputs` exists
because a simulation and the gate acting on it have to be the same code. Retiring the policy scope
must not reintroduce a second path.

## Verification

- A predicate carrying a term its consumer cannot enforce is refused, and the message names the term.
- The declaration is read rather than restated: a term deleted from it changes every consumer's
  behavior, proven by a test that iterates the declaration instead of listing terms again.
- Every narrowed endpoint returns a strict subset of its unnarrowed answer, on the crawled
  organization. **This alone does not prove the filter landed** — it passes for a version whose row
  list is bounded and whose counts still span the organization, which is the defect that has now
  appeared twice. Pair it with the recount check below.
- Every narrowed endpoint that aggregates recounts within the bound: an advisory's affected-repository
  counts fall when the answer is bounded, rather than being carried over from the whole organization.
  The corpus makes this sharp — it holds a single owner, so a bounded owner row that still reads 712
  could not have been produced by dropping rows.
- A bound that matches no repository returns an empty list rather than the whole estate or `null`.
- A bound asked for against a derivation that has not been run is **refused by name**, never answered
  with an empty result — an absent measurement is not a measurement of zero.
- The set of endpoints that apply the term is declared once and read by both the test that they
  accept it and the test that everything else refuses it, so the pair cannot come to disagree.
- Every filtered answer reports the owned repositories that carry no value for the key it filtered on.
- Migrated unit-scoped policies select the same repositories before and after `scope_kind` is retired.
- The same term, applied by the server and applied by the browser over the unfiltered payload,
  selects the same nodes on the crawled organization. Run per term, because one agreeing proves
  nothing about the others.
- A term deliberately emptied survives a reload, or the consumer says which default it fell back to.
