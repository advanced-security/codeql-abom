# A rule the security team can try before it bites

**Status:** not built. `abom_saved_view` holds named predicates, which is the artifact a policy is
made of; nothing yet drafts, simulates or adopts one.

## Why this exists

A rule nobody can try is a rule nobody turns on. A security team asked to enable enforcement across
an organization has no way to answer the only question that matters first — *what would this have
blocked?* — so the rational move is to leave it off, and a gate that is never enabled protects
nothing.

The answer is already in the database. ABOM holds the closure, the findings, the advisory ranges and
the reach; asking what a rule would have refused is a query, not a crawl. No ingress, no server a
pull request can reach, no published action. **This is the half of enforcement that is startable
now**, and it is the half that decides whether the other half ever gets switched on.

It is also where exceptions live, and real organizations run on exceptions. A policy design with no
account of them gets one bolted on under time pressure, as a pattern, by someone who needs the build
to go green.

## Pending

1. **A policy as a declared artifact**, built from a saved view plus the scope it binds to.
   Organization, business unit, or repository — see
   [`../db/business-units.md`](../db/business-units.md) for where the middle one comes from.
2. **Simulation over stored state.** Given a policy, report what it matches now and what it would
   have refused. The verdict shape belongs to [`verdict-gate.md`](verdict-gate.md); this is the same
   evaluation with nothing at stake. Depends on 1.
3. **Adoption as a recorded transition**, with who adopted it and against which simulation. A policy
   that cannot say what it was expected to block cannot be argued about afterwards. Depends on 2.
4. **Exclusions and exceptions**, with the shapes below. Depends on 1.
5. **An audit answer**: every exclusion and exception in force, listed in full, on demand. This is
   not a report to be generated on request later — it is the thing that makes the whole design
   defensible, and it is a pending item in its own right. Depends on 4.

## The constraints that must not be broken

**A policy nobody simulated is not adopted.** Adoption without a simulation is how a gate gets
enabled on a backlog, blocks everything, and is switched off within a week.

**An exclusion is enumerated. An exception is enumerated and expires.** An exclusion says the rule
does not apply here; an exception says it applies and is waived until a stated time. Both are
listable in full at any moment.

**Neither is ever an open-ended pattern.** A pattern cannot be audited, and it silently covers a
repository created after it was written — a waiver nobody granted. This is the same failure
[`../db/owned-scope.md`](../db/owned-scope.md) refused for discovery scope, and it is refused here
for the same reason. The counter-argument is that enumeration does not scale; the answer is that
business-unit scope is the thing that is supposed to scale, and a waiver is supposed to be
uncomfortable.

**Enablement and advisories may be referenced, never inherited.** A rule may say "refuse when the
audit reports scanning disabled". That is the operator adjudicating on an attributed statement,
which is allowed. ABOM deciding it on its own is not.

## Verification

- A simulation over the stored graph and the gate's evaluation of the same policy agree on the same
  inputs. If they can disagree, there are two policy engines and one of them is wrong.
- An expired exception stops applying without anybody touching it, and the thing it was waiving is
  refused on the next evaluation.
- The audit answer lists every exclusion and exception with its scope, its reason and, for an
  exception, its expiry — and an attempt to write one as a pattern is refused with the reason.
- A policy adopted without a simulation is refused, and the refusal names what is missing.
- Simulation latency against the whole organization. If it cannot be answered from stored state in
  seconds, the design is wrong rather than slow.
