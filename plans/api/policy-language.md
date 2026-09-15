# A policy can only say what the filter language can say

**Status:** a policy is a saved view with consequences, and it now enforces exactly the terms its
text carries. What it cannot do is say what the rules are mostly for.

## Why this exists

`abom_policy.predicate` holds a serialized `view.Spec` — the same text the Explorer writes into the
address bar and `abom_saved_view` stores under a name. That is the right design and it is built:
one language, one evaluation, read by the set endpoint and by the gate alike, so a rule and the
screen it was written from cannot drift.

**It cannot say what an Actions rule is for.** An operator's rule is mostly about an advisory, a
finding, and whether either is reached from something they own. The language offers rule ids, a
provider and a severity. There is no advisory term at all, and `minReach` is a drawing floor rather
than exposure — so *carries a live advisory* and *is reached transitively from a repository I own*
cannot be written down. Those are the questions this project exists to answer, and they are exactly
the ones a policy cannot ask.

## Pending

1. **An advisory term.** `abom_advisory` and `abom_exposure` are already read by Remediation and are
   unreachable from the language. The fork is what it asserts: any live advisory, one at or above a
   severity, or one with a published fix. The third is the most actionable and sits closest to the
   line advisories are not allowed to cross, so it is a decision rather than a detail. A new term
   must be added to the enforceable declaration in `internal/view`, or the set path refuses it.
   Depends on nothing.
2. **A reachability term bounded to ownership.** `abom_exposure` holds `hops` and a route per
   repository. The fork: direct versus transitive as a flag, a hop ceiling, or *reached from an
   owned repository at all*. The last is the honest minimum and matches what the rules are for.
   Depends on nothing.

## The constraints that must not be broken

**A term is enforced or refused, never accepted and ignored.** This is the existing rule about
`units` on the graph, applied where it does more damage: a drawing term silently dropped from a
policy changes what a pull request is blocked for. The interface may show a term it cannot enforce,
but it must say so before the policy is adopted, not after it refuses something.

**An advisory term reports an advisory; it never adjudicates one.** A policy may say *this has a
live advisory* because that is a fact about what GitHub published. It may not say *this is
vulnerable*, because whether an observed reference falls inside a published range is a judgement
ABOM does not make and a policy cannot borrow. The `actions/*` queries make that call.

**A security verdict still comes only from a rule id.** A policy chooses which findings matter and
where; it never invents one. There is no policy-shaped route around the absence of a `pin_status`
column.

**Direct and transitive stay distinct in the language, not only in the report.** A rule that
collapses them refuses a repository for something it cannot fix, which is the fastest way to have a
gate switched off.

**A rule is never about a specific reference, and never about what the operator does not own.**
Pinning is `actions/unpinned-tag`'s call. A repository outside the declared scope is somebody else's
to fix, and a policy that reaches it is enforcing against a stranger.

## Verification

- A policy written as *advisory, reached from an owned repository* selects the same repositories that
  `/api/match` and the Remediation screen already agree on for the same advisory, against a crawled
  organization. If the three disagree, the term is measuring something else.
- A new term is added to the enforceable declaration in the same change that adds it to the language,
  or the set path refuses every predicate carrying it.
- `TestASimulationAndTheMatchEndpointAgreeOnTheSameInputs` still passes: one evaluation, never two.
- A simulation still reports what it could not see. A policy about advisories is worth less than
  nothing if the unattributed count is not beside its refusal count.
