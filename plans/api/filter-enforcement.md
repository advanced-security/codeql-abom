# A term is enforced or refused, and today one is neither

**Status:** every term in the language is applied by the server, applied by the client, or refused
by name, on both the set path and the graph. Every endpoint that can be bounded to a repository
grouping is. What remains is a second narrowing mechanism inside policies.

## Why this exists

A consumer that accepts a predicate and applies part of it enforces something wider than the reader
saw. The set path did exactly that, and the widest version of a predicate is the one that blocks a
pull request.

The eleven endpoints that read their own parameters now refuse a term they do not apply. Eight apply
`properties`; the two that refuse it do so for a reason stated below.

## Pending

1. **Retire `abom_policy.scope_kind` and `scope_value`.** They are a second narrowing mechanism,
   evaluated by different code, for what a predicate now says directly — and two narrowings that
   must agree are how two policy engines start. Existing unit-scoped policies migrate into
   predicates. `plans/api/policy-language.md` once chose the other direction, when a predicate could
   not name a property key; it can now. Depends on nothing.

**Two endpoints refuse it for a reason and should keep refusing.** `/api/properties` enumerates the
groupings, so bounding it by a grouping is circular; `/api/frontier` reports crawl queue state for
repositories that may not be owned or even known yet, and so have no properties to match. Neither is
an oversight.

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

**A narrowed answer says what it excluded.** `PropertyBreakdown.WithoutValue` already counts the
owned repositories carrying no value for a key. Those repositories vanish from every filtered answer,
and a reader who is not told believes they scoped when they truncated. This is the same rule the
simulation already follows by reporting what it could not attribute.

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

**A security verdict still comes only from a rule id.** Choosing which findings matter and where is
what a filter does. Inventing one is not, and there is no filter-shaped route around that.

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
