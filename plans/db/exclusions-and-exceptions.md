# Hiding something is a decision somebody signs

**Status:** exclusions and exceptions are modelled, validated and auditable for a policy verdict,
and reach nothing else. Nothing an operator can do from a screen hides a result or stops work.

## Why this exists

Every real organization carries things it has decided not to look at and things it has decided not
to act on yet. Both are legitimate. Neither may be silent, and neither may be permanent by default.

The two are different decisions and must not be one control:

| | Means | Effect | Required shape |
| --- | --- | --- | --- |
| **Exclusion** | Do not do this work | Nothing is analyzed, nothing is recorded | Enumerated |
| **Exception** | Do the work, do not show me the result | Analysis runs and records; the result is hidden from the interface | Enumerated, and expires |

An exception that hid the work as well would destroy the evidence that the decision was ever
reviewable. An exclusion that merely hid results would spend the analysis budget anyway. The
distinction is the point.

```mermaid
flowchart LR
  REC[("the governed record<br/>kind · reason · author · expiry")]
  REC --> POL["a policy verdict"]
  REC -. "no scope beyond one policy" .-> EST["the estate"]
  EST -. .-> EXCL["exclusion:<br/>the queue declines the work"]
  EST -. .-> EXCP["exception:<br/>work runs, result hidden"]
  EST -. "no screen writes one" .-> UI["create · review · expire"]
  style EST fill:#3d2d1d
  style EXCL fill:#3d2d1d
  style EXCP fill:#3d2d1d
  style UI fill:#3d2d1d
```

`abom_policy_waiver` already carries the shape — the two kinds, a required reason, who granted it
and when, and an expiry that is required for an exception and read at evaluation time so it lapses
on its own. What it does not have is any scope beyond one policy, any effect beyond a verdict, or
any way to create one.

## Pending

1. **A scope beyond a policy.** The record is keyed to the policy it excuses. An operator excluding
   a repository from analysis is not writing a policy, and needs the same governed record without
   one. Depends on nothing.
2. **An exclusion stops work.** The frontier declines to queue it and says *excluded by decision* —
   which is not `unavailable`, a fact about the world rather than a choice somebody made. Depends
   on 1.
3. **An exception hides results at the read layer only.** The analysis runs, the findings are
   recorded, and the interface does not show them. Every surface that hides something states how
   much, the way a filtered count already does. Depends on 1.
4. **A screen to create one**, refusing to proceed without a reason and, for an exception, a review
   date. Reachable from the selection an operator was already looking at. Depends on 1.
5. **A review surface**: what is excluded, what is excepted, why, by whom, what has lapsed and what
   is about to. Depends on 1.

## The constraints that must not be broken

**Neither is ever a pattern.** A pattern cannot be audited, and it silently covers a repository
created after it was written — a waiver nobody granted. Enumerated, both of them, always.

**An exception expires.** A hidden result with no review date is a permanent hole that nobody
decided to dig. The expiry is read when the decision is applied, so it lapses on time rather than
when somebody remembers to run a sweep.

**Hiding is never destruction.** An exception filters a read. It does not tombstone, delete, or stop
the work that produced what it hides — those are different operations with different consequences,
and pruning stays what it is: removing what the declared closure no longer reaches.

**What is hidden is counted where it is hidden.** A surface applying an exception says how many
results it is not showing. Silence about an exclusion reads as coverage; silence about an exception
reads as a clean estate.

**A reason is not optional.** The record refuses to be written without one, because the reason is
the only thing that makes a review possible later.

## Verification

- An excluded repository never enters the queue, and the interface says why rather than reporting it
  as unavailable.
- An excepted finding is still in the findings table, still counted by the API, absent from the
  screen, and the screen says how many it is not showing.
- An exception past its review date stops hiding without anything being run.
- Every exclusion and exception is listable in full at any moment, with its reason and its author.
