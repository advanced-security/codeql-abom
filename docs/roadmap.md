# Roadmap

Where ABOM is going, in themes rather than tasks. For the destination and the reasoning that fixes
it, see [the vision](vision.md). The ranked, startable work is in
[`plans/next.md`](../plans/next.md); each theme below names the plan that holds its reasoning.

## Built

The measuring half works end to end. ABOM discovers repositories in a declared account, analyzes
them with CodeQL, follows their Actions dependencies upstream one measured hop at a time, and
serves the resulting transitive graph with findings, reach and coverage attached. It exports a bill
of materials, submits a dependency snapshot to GitHub, and explains what it has not looked at.

See [how it works](architecture.md) for the current shape.

## An organization that stays current at scale

ABOM can be pointed at a declared organization and left alone: one loop reads the account on a
period, queues what it finds, analyzes and reads again, from an empty database and without a command
being run. Reads are conditional and paced by the budget GitHub reports.

What is not solved is doing that for an organization of twenty thousand repositories. Reading
everything on every period is affordable at small size and not at large, and the three questions an
organization poses have different costs: what is new and what changed can be answered from a sorted
prefix, while **what is gone can only be answered by reading everything**. GraphQL is the efficient
way to ask the first two and to carry whatever else is wanted about a repository, with REST as the
fallback for the organizations that will not permit it. `plans/api/org-enumeration.md`

## Business units, read from the organization

An organization has no single person who can act on a finding in it. Repository custom properties
already carry the dividing line the organization uses, and the distinct values ABOM discovers become
a scoping dimension across discovery, filters, the Explorer, the bill of materials and policy. It is
what makes the remediation phase addressable to somebody. `plans/db/business-units.md`

## Enablement, so a coverage gap names its cause

ABOM reports honestly that a repository has no CodeQL database and cannot say why. Consuming
[`gh-ghas-audit`](https://github.com/advanced-security/gh-ghas-audit) turns that absence into
configuration, execution, freshness or language coverage — an enablement task somebody can do. The
exchange runs both ways: GitHub's language statistics do not report `actions`, so ABOM supplies the
one gap that audit cannot see. `plans/api/ghas-enablement.md`

## From a finding to a fix

**This is the gap that matters most.** ABOM currently holds thousands of findings that terminate on
its own screen. A security team cannot act on a dashboard nobody is assigned to.

Two routes out, in order:

1. **A gate that refuses the pull request** that introduces the exposure, which beats reporting it
   after it merges — ABOM holds the only transitive Actions graph, so it is the only thing that can
   refuse a change on that basis. The policy has a life before it has teeth: drafted, simulated
   against stored state, and adopted only if what it would have blocked is acceptable — which is
   built. `plans/api/verdict-gate.md`, `plans/api/abom-check.md`
2. **Findings that reach GitHub's own alert surface**, so they arrive where developers already
   look and can join a security campaign. Gated on a standing rule rather than on the upload: no
   alert may exist that a person did not choose. `plans/api/code-scanning-upload.md`

Both need a server a pull request can reach, which this project does not yet have.

## Remediation that GitHub can drive

The submitted dependency snapshot already names versions for most commit-pinned dependencies, which
is what lets Dependabot match an advisory at all. Two things are open: whether ABOM should submit
the direct commit-pinned entries it currently withholds, and whether GitHub opens a pull request on
the parent dependency for a transitive Actions alert. The second is an inference from GitHub's
documentation and has to be measured, not asserted. `plans/db/version-resolution.md`,
`plans/api/dependency-submission.md`

## Staying current without being told

The crawl is resumable and budgeted, and it can already detect that a reference has moved. What is
missing is the trigger — reading in response to events rather than on a timer — and the address an
event would arrive at. Until then a periodic sweep is the floor. `plans/api/org-server.md`,
`plans/api/event-driven-reading.md`

Separately, a finding that stops being reported is currently never removed. The sighting record
exists and the sweep is written; it waits on a crawl to make its report believable.
`plans/db/finding-lifecycle.md`

## Scale

The interface is legible at the sizes an operator actually arrives at, and the endpoints behind it
answer in well under a second at the sizes measured so far. The known ceilings are the browser's
node count and the fact that reach is recomputed wholesale. Neither is urgent; both are measured
rather than guessed, and both have to be re-measured against a real organization rather than carried
forward as remembered numbers. `plans/db/data-efficiency.md`

## Deliberately not on the roadmap

- **Enterprise scope, or federation across organizations.** The boundary is what makes the
  efficiency target mean anything, and a tool spanning organizations inherits no permission model it
  can rely on.
- **Re-implementing what `gh-ghas-audit` measures.** Enablement has an owner; ABOM consumes the
  answer and contributes the one gap that tool cannot see.
- **Evaluating an `if:` condition.** Conditions are reported verbatim. Deciding whether one holds is
  not something static analysis settles.
- **Deriving vulnerability from an advisory.** Advisories enrich; a verdict comes only from a rule.
- **A `pin_status` column, or any stored judgement about how a reference is pinned.** The reference
  is recorded as written; a judgement about it belongs to whoever asks.
- **Anything that writes to another account's repositories without a person asking.**
