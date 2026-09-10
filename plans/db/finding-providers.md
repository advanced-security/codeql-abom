# Findings are a pluggable class, not a CodeQL detail

**Status:** The seam is built. `abom_finding.provider` records the tool the SARIF document names,
and CodeQL is expressed through it. A second provider is deferred by decision.

## Why this exists

ABOM adds two things to GitHub's dependency graph, and only the first is structural. The edges are
a one-time fix: once submitted, GitHub resolves Actions transitively and that gap closes. The
findings are a **surface** — every new provider makes the same graph worth more without changing
its shape. This matters most exactly where Dependabot goes quiet. Actions alerts are generated for
semantic versions only, not SHA references, and this corpus is heavily SHA-pinned. On the
population GitHub cannot alert about, ABOM's own findings are the only evidence there is.

## Pending

1. **Deferred by decision: the second provider.** CodeQL is the provider, and the work that pays is
   making it better — findings that stop being reported, findings that cannot name their run,
   coverage that says what was not looked at. A second provider before that is breadth bought with
   depth. The candidate, when the time comes, is **`policy-reachable-action`**: an action the
   effective Actions policy would refuse, reachable through one it permits. An allow-list is
   written against `uses:` lines a person can read and is never evaluated against what those
   permitted actions themselves pull in, because nothing else holds the closure.
   `store.ActionsReachedThroughComposites` already walks it — **5,724 call sites** — with no caller.
2. **Per-provider provenance, where *unknown* is a state rather than a default.** The state exists
   and the document reports it. What a non-CodeQL provider records is undefined until one is
   wanted; defining it now would be guessing at a shape nothing has to fit.
3. **Projection to sinks** — the pointer-not-payload rule for snapshot metadata. Depends on
   `api/dependency-submission.md` settling PURL identity, which is also the join key that lets a
   finding about `actions/checkout@v4` reach the node GitHub already holds.

## The constraints that must not be broken

- **A finding is evidence attached to a node, never a verdict about it.** Adjudication happens in
  `api/verdict-gate.md`, against a policy the operator declared, and nowhere else.
- **Every finding names its producer and how to reproduce it.** A CodeQL run is reproducible from
  its pack digest and CLI version. An agent is not. Pretending every provider has CodeQL's
  provenance is how the ledger becomes decorative.
- **A provider may not invent an edge.** Findings attach to the graph; only the crawl changes its
  shape. A provider that could add nodes would let a scanner's guess become a dependency.
- **Unknown is not permissive.** Where a provider cannot establish a fact, the state is *unknown*,
  never "no violations".
- **Nothing is back-filled.** A migration that guessed a producer from which columns happen to be
  filled would defeat the column.
- **Verbatim on the way in is not verbatim on the way out.** A SARIF ABOM uploads reports ABOM's
  analysis at locations ABOM re-anchored; republishing it as `CodeQL` would tell a repository their
  own scanner said this.

## Verification

- A finding whose provider cannot be reproduced says so, distinguishably from one nobody has tried
  to reproduce: `reproducible` is absent rather than `false` when no producer was recorded.
- When a second provider is eventually wanted, it is readable through the same shape without
  either side carrying a field the other has to leave empty.

## Open question

- **What is a finding's subject?** A PURL alone joins to GitHub's graph; a PURL plus a location
  inside the dependency is what CodeQL produces. Carrying both means the join key and the evidence
  key differ, and every sink has to choose. Deciding it now would decide it from CodeQL's shape.
