# An edge carries what a reader wants to filter on

**Status:** an edge stores its boundary and its reference; what it means — pinned or floating, on a
route to an advisory, direct or transitive — is re-derived per request or not derivable at all.

## Why this exists

A `uses:` line *is* an edge. "Which references float on a tag", "what crosses into another account",
"what sits on a route to a live advisory" are all questions about relationships, and a reader has no
way to ask any of them as a relationship. Version pinning counts edge rows through a purpose-built
query; the graph re-derives the boundary class in the browser on every load; nothing returns an edge
as a row at all.

Making relationships a unit an operator can filter, list and select needs the facts to live on the
edge, stored once and read everywhere.

```mermaid
flowchart LR
  E[("abom_edge")]
  E --> KEY["edge_key<br/>stable, and in no response"]
  E --> B["boundary<br/>stored"]
  E --> R["ref_value<br/>stored"]
  B -. "derived again in the browser" .-> CLS["one answer<br/>read everywhere"]
  R -. .-> STYLE["commit · tag · branch"]
  E -. "nothing filters edge_kind" .-> KIND["kind as a term"]
  E -. "needs the walk" .-> ROUTE["on a route to an<br/>advisory · a finding"]
  E -. .-> DEPTH["depth from<br/>an owned repository"]
  CONT["repo-contains · org-contains"] -. "synthesized per request" .-> E
  style CLS fill:#3d2d1d
  style STYLE fill:#3d2d1d
  style KIND fill:#3d2d1d
  style ROUTE fill:#3d2d1d
  style DEPTH fill:#3d2d1d
  style CONT fill:#3d2d1d
```

## Pending

1. **Edge identity reaches the client.** `edge_key` is a stable natural key — the source location
   that declares the reference — and no response carries it and no handler accepts it. A delta
   already names removed edges by it, so nothing can act on that removal until the payload does.
   Depends on nothing.
2. **One definition of a boundary, stored and read.** The browser derives the class rather than
   reading the stored column, which is two answers waiting to disagree. The stored value is the
   answer; the client reads it. Depends on 1.
3. **Every term is enforced or refused.** `boundary` accepts a value outside its three and filters
   nothing, and no term filters `edge_kind` at all. Depends on nothing.
4. **Facts derived from the graph, filled by the aggregate pass.** Each one tri-state, with NULL
   meaning *not computed* rather than *false*:
   - reference style — commit, tag, branch — which is what makes pinning a filter rather than a
     purpose-built page;
   - on a route to a live advisory, which the stored exposure path can already answer;
   - on a route to a finding, which needs the walk;
   - depth from an owned repository, so direct and transitive are read rather than re-walked.
   Depends on nothing; recompute incrementally for edges whose endpoints moved.
5. **A merged edge stays honest.** The graph merges relationships by endpoint pair for drawing.
   A merge that keeps one row's facts and discards the rest is a claim about references it did not
   look at, so a merged edge carries its constituent keys and, per fact, either a shared value or
   an explicit *mixed*. Depends on 1 and 4.
6. **An edges endpoint** returning rows with identity and facts, paged, taking the full predicate.
   Depends on 3 and 4.
7. **Relationships as a unit of Inventory.** Dependencies, repositories and advisories are units of
   one scope already; relationships are the shape with none, and this plan is the whole reason.
   A unit only exists once its columns are its own, and an edge's are identity, boundary, reference
   style and what it sits on a route to — 1, 3 and 4 above. Node actions are disabled there and say
   why, because a selection of edges is not a selection of nodes. Depends on 6.
8. **Containment is stored rather than synthesized.** The graph draws two relationships it does not
   store, so any list over the edge table and the canvas disagree about how many exist. They belong
   in a table of their own: an edge's identity is the source location that declares it, containment
   has none, and the edge table is joined by reach, exposure, blast radius, the bill of materials,
   pinning and pruning. Depends on nothing.

## The constraints that must not be broken

**A relationship ABOM does not understand is not a relationship to an account.** A reference whose
syntax is unrecognized is refused, never split on the first slash and treated as `owner/repo`. That
fall-through is how a self reference becomes a third-party dependency on an account that does not
exist.

**NULL is unknown, and unknown is not a class.** An edge whose ends nobody could name has no
boundary, which is different from crossing an account. The same holds for every derived fact.

**Storing containment changes no aggregate.** Reach, exposure, blast radius, the bill of materials
and pinning each count relationships; none of their numbers may move because containment became
storable. A single shared filter says what a dependency is, and every traversal uses it.

**Ordering is not use.** `job-needs-job` expresses sequence between two jobs in one file. A list of
relationships must not present it as a dependency.

## Verification

- A second run of the derivation classifies nothing, so it settles.
- The derived classification agrees with the same classification computed independently in SQL, and
  the rows that disagree are named rather than reconciled — they are the interesting ones.
- Case is compared insensitively: accounts spell themselves inconsistently, and a case-sensitive
  compare reports a repository's own files as third-party.
- Before and after storing containment: node count, edge count, reach, exposure, bill of materials
  components and pinning totals are unchanged.
- Filtering by a term through the API and through the browser returns the same edges.
- Measured at grid scale with paging before any index is added.
