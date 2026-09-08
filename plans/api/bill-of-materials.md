# A bill of materials you can read, export and verify

**Status:** implemented. Shipped with two known approximations, both stated in the documents
themselves: `abom_commit_content` is empty so no component carries an indexed digest, and a
quarter of findings cannot be traced to a run.

## What the corpus falsified

- **93% of the first document was noise.** The closure walked every reachable node, and 10,530 of
  11,290 components were `step` rows — positions in a file, not things anything depends on, and
  9,483 of them had neither a reference nor a finding. Steps and jobs are now folded into whatever
  holds them, the same collapse the Explorer draws, so the document and the picture agree about
  what a component is. **11,290 → 2,548.**
- **Two components shared one SPDX identifier.** The character set SPDX allows is narrower than a
  node key's, so `actions/upload-artifact/merge` and `actions/upload-artifact-merge` both reduced
  to `SPDXRef-actions-upload-artifact-merge`, and `$/` — an unexpanded `${{ }}` — reduced to the
  empty `SPDXRef-`. Silently merging two dependencies into one is worse than an invalid document,
  and only the real corpus had the pair that collided.
- **Every relationship said nothing.** `boundary` is filled by a derivation the serving path runs
  from `RebuildAggregates`, which the command never called, so the first run emitted 11,258 empty
  boundaries. The command classifies first now; the pass is idempotent and settles to zero.
- **The SPDX document name pasted all 44 roots into a 1,400-character string**, and the namespace
  with it. Named after the first owner and a count now; the roots are still listed in full in the
  comment, where there is room.
- **"Generated for ABOM's own repository, the document names the SBOM libraries this plan adds"
  is not achievable and was struck.** ABOM extracts GitHub Actions, so a document rooted at this
  repository lists the actions its workflows use. It has no Go module extractor and this plan does
  not add one. The dependency cost is measured below instead, which is what that item was for.

## What reconciling against the code corrected

- **"ABOM holds all of that" is false, and this is the finding that shapes the plan.** The
  provenance chain below is partial in the real corpus: `run_key` on **5,472 of 5,472** runs, but
  `pack_digest` on **3,917 (72%)**, `query_run_id` on **8,611 of 11,370 findings (76%)**,
  `result_hash` on **6,582 (58%)**, and `abom_commit_content` holds **0 rows** — the table is
  created and never written to on this corpus, so `indexed_digest` is available for nothing at
  all. A document claiming provenance it does not have is precisely the failure the coverage
  section exists to prevent, so **provenance is stated per component the same way coverage is**:
  present, or absent and named as absent.
- **"There is no third state" was wrong.** The verification asked that every component trace to a
  `run_key` or be marked unanalyzed. The corpus has a third state: **2,759 findings with no
  `query_run_id` at all**, every one of them `actions/unpinned-tag`. They were analyzed — the
  finding exists — but which run produced them was not recorded. Analyzed-but-untraceable is a
  real state and the document has to be able to say it.
- **A closure read now exists, and it is not the one this needs.** `graph.OwnedClosure()` landed
  with `db/discovery-boundary.md`. It is **repository-granular** and returns membership only — no
  components, no references, no coverage — because it exists to bound the crawler. The BOM needs
  a node-granular walk that carries what each component is. Same shape, different unit; reusing
  it would mean a translation nobody could check.
- **31 routes, not 27.** `/api/owned-scope` gained a GET and a PUT since.
- **`ref_raw` exists and the "178 edges" figure is unverifiable as stated.** The column is real,
  and the point behind it stands — `actions/checkout` is referenced by `v4` 2,370 times, `v7`
  1,686, `v6` 1,468, and by bare SHAs — so a document that normalizes references away discards
  the most useful thing it has. The specific count in the plan was not reproducible.
- **Findings belong to the component that holds the reference, not the one referenced.**
  `LinkFindingsToNodes` attributes by source location, so an unpinned `uses:` is a fact about the
  workflow that wrote it. Two of the first tests asserted this backwards.

## Why this exists

The Explorer already draws approximately the right thing: a scoped portion of the dependency graph
with findings and advisories attached. **There is no way to get it out.** No CSV, no JSON
download, no `@media print`, no export endpoint of any kind — `internal/serve/serve.go` publishes
thirty-one routes and every one of them answers a browser. *(Checked: nothing in `internal/`,
`cmd/` or `ui/` writes a `Content-Disposition`, a CSV, or any SBOM format.)*

A bill of materials for a set of owned Actions resources is the fully resolved list of their
direct and indirect dependencies, enriched with what is known about each and verifiable by
reference and hash. ABOM holds much of that, and the document has to be honest about the rest.

## What is already true

| | |
| --- | --- |
| export of any kind | **none** |
| store read for "everything reachable from these repositories" | `graph.OwnedClosure()`, repository-granular, membership only |
| `Subgraph` | focus plus hops plus a node budget — a neighborhood, not a closure |
| `FullGraph` | the whole corpus, or `scope=exposure`, or one boundary class |
| `BumpImpactOf` | walks *dependents* from a node, the opposite direction, capped at 8 hops |

The provenance a BOM needs is stored, traceable, and **incomplete** — the percentages are what the
document has to state rather than assume:

- `abom_query_run.run_key` is `sha256` over query id, version, pack, pack version, **pack digest**,
  CLI version, repository and commit. Present on **all 5,472 runs**; `pack_digest` on **72%**.
- `abom_finding.result_hash` is content-addressed and repository-independent, so one defect
  reaching many repositories is a group-by. Present on **58%**; `query_run_id` on **76%**.
- `abom_frontier.resolved_sha` is the commit a reference actually resolved to. **2,818 of 3,662**.
- `abom_commit_content.indexed_digest` is a hash over exactly what the extractor read — and the
  table is **empty**. `RecordCommitContent` exists and nothing on this corpus has called it.

That chain is the substrate `db/analysis-ledger.md` built, and this plan is what it was for. It is
also the plan that discovers how much of it was never filled in.

## The design

### One read, several roots

A closure from a root set: the whole owned scope, one repository, one workflow, or one action.
Same walk, different starting points, because "the BOM for our organization" and "the BOM for this
one workflow" are the same question asked at different scales.

Direction is *outward* — what does this depend on — which is the opposite of `BumpImpactOf` and
the reason neither can be reused for the other.

### What each component carries

- What it is, and where it lives.
- **The reference as written**, verbatim. `ref_raw` for 178 edges is literally
  `actions/checkout@v7`; that is evidence about what the organization runs, not corruption to be
  normalized away.
- The commit it resolved to, when one is known.
- The findings against it, each naming its rule.
- The advisories naming its repository, with their ranges — reported beside the observed
  references, never applied to them.
- The provenance of every claim: `run_key`, `pack_digest`, `result_hash`.

### What each relationship carries

The boundary it crosses, from `db/edge-relationships.md`, and whether it is direct or indirect
**relative to this document's root**. Two BOMs over the same graph disagree about direct and
indirect, correctly, because they start in different places.

### Coverage is inside the document

This is the part that makes it a bill of materials rather than a list.

A component ABOM has not analyzed is *in* the BOM, marked as unanalyzed, with the count of
references in use against the count analyzed. Leaving it out would make the document look
complete; including it silently would make an unexamined component read as a clean one. Both are
the same failure — an absent measurement drawn as a measurement — and the interface already has
three scars from it.

The document therefore states what it contains, what was examined to produce it, and what was not.

### Three serializations

| Format | Why |
| --- | --- |
| **Native ABOM JSON** | The only one with anywhere to put `run_key`, `pack_digest` and `result_hash`. The others cannot express provenance at this granularity, so a native document is the one that is actually verifiable. |
| **CycloneDX 1.6** | `github.com/CycloneDX/cyclonedx-go` v0.12.0+ requires Go 1.25, which this repo is on, and supports spec 1.0–1.7. Components, dependencies and vulnerabilities in one document, consumable by existing tooling. |
| **SPDX 2.3** | `github.com/spdx/tools-golang` supports **2.1, 2.2 and 2.3 only** — not 3.0. See the decision below. |

### The correction to record

**SPDX 3.0 was asked for and is not available.** `tools-golang`'s README states it works with
files conformant to versions 2.1, 2.2 and 2.3, and does not support anything prior to 2.1 or later
than 2.3. SPDX 3.0 is JSON-LD and would be hand-rolled with no library, on a format whose
correctness we could not check against a reference implementation.

So this plan emits **SPDX 2.3**. Hand-rolling 3.0 is a decision to take on its own evidence, not a
line item inside this one.

### The dependency surface is part of the decision

ABOM has **one direct Go dependency** today — `mattn/go-sqlite3`, zero transitive — and a
`dependency-review.yml` workflow with no `paths:` filter precisely so it always runs. Adding two
SBOM libraries is a material change to the supply chain of a supply-chain tool.

That is not an argument against it; it is an argument for measuring it. **Measured: the module
graph goes from 2 to 19 (`go list -m all`), but the shipped binary needs 4 third-party modules —
`mattn/go-sqlite3`, `CycloneDX/cyclonedx-go`, `spdx/tools-golang` and `anchore/go-struct-converter`.
The other 13 are test-only dependencies of those libraries and never reach the build**, which
matters because one of them, `terminalstatic/go-xsd-validate`, wraps libxml2 through cgo and
would otherwise be a build requirement on every machine.

## Phases

1. The closure read, with coverage, tested against the fixture before any serialization exists.
2. `GET /api/bom` with the root as a parameter, serving native JSON.
3. The BOM page: listable, filterable, with the coverage statement visible rather than in a
   tooltip.
4. CycloneDX 1.6 and SPDX 2.3 writers, each behind the same read.
5. Download from the page, and the same document from the command line.

## Verification

- The native document's component set is the collapsed graph reachable from the roots,
  cross-checked in SQL. **2,548 of 9,426 collapsed nodes**, the difference being reachability.
- CycloneDX output parses as 1.6 with unique `bom-ref`s; SPDX output parses as 2.3 with unique
  `SPDXID`s. **2,548 distinct of 2,548 in both.**
- Every component in the document traces to a `run_key` that exists, is marked unanalyzed, **or is
  marked analyzed-but-untraceable**. The third state is real and is counted in the document.
- A component with references in use and none analyzed says so, and is not omitted.
- The same root produces an identical native document twice, ignoring the generation time, which
  is the one field that is supposed to differ.
- Module count and transitive dependency count before and after, reported.

## Open questions

- **Whether the BOM should be servable for an unowned root.** The read takes a root set, so it
  can. Whether the interface offers it is a different question: a BOM for something you do not own
  is a legitimate research artifact and an illegitimate compliance one.
- **Print.** "Otherwise printable" was in the original framing and a print stylesheet was not
  chosen. The page will print as any page does; a real `@media print` layout is not planned.
- **CSV** was not chosen either. The native JSON is the escape hatch for anyone who wants to make
  one.
