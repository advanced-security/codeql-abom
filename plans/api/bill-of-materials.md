# A bill of materials you can read, export and verify

**Status:** planned. This is the product. Everything before it exists so that what this emits is
about repositories somebody is actually responsible for.

## Why this exists

The Explorer already draws approximately the right thing: a scoped portion of the dependency graph
with findings and advisories attached. **There is no way to get it out.** No CSV, no JSON
download, no `@media print`, no export endpoint of any kind — `internal/serve/serve.go` publishes
twenty-seven routes and every one of them answers a browser.

A bill of materials for a set of owned Actions resources is the fully resolved list of their
direct and indirect dependencies, enriched with what is known about each and verifiable by
reference and hash. ABOM holds all of that. It cannot state it.

## What is already true

| | |
| --- | --- |
| export of any kind | **none** |
| store read for "everything reachable from these repositories" | **none** |
| `Subgraph` | focus plus hops plus a node budget — a neighborhood, not a closure |
| `FullGraph` | the whole corpus, or `scope=exposure` |
| `BumpImpactOf` | walks *dependents* from a node, the opposite direction |

The provenance a BOM needs is already stored and already traceable:

- `abom_query_run.run_key` is `sha256` over query id, version, pack, pack version, **pack digest**,
  CLI version, repository and commit. Every finding carries `query_run_id`.
- `abom_finding.result_hash` is content-addressed and repository-independent, so one defect
  reaching many repositories is a group-by.
- `abom_frontier.resolved_sha` is the commit a reference actually resolved to.
- `abom_commit_content.indexed_digest` is a hash over exactly what the extractor read.

That chain is the substrate `db/analysis-ledger.md` built, and this plan is what it was for.

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

That is not an argument against it; it is an argument for measuring it. The added module count and
transitive closure are recorded in the plan's result, and ABOM's own BOM is the obvious first
document to generate.

## Phases

1. The closure read, with coverage, tested against the fixture before any serialization exists.
2. `GET /api/bom` with the root as a parameter, serving native JSON.
3. The BOM page: listable, filterable, with the coverage statement visible rather than in a
   tooltip.
4. CycloneDX 1.6 and SPDX 2.3 writers, each behind the same read.
5. Download from the page, and the same document from the command line.

## Verification

- The native document's node and edge sets are **exactly** what the Explorer draws for the same
  scope, cross-checked in SQL. If they differ, one of the two is lying.
- CycloneDX output validates against the 1.6 schema; SPDX output validates against 2.3.
- Every component in the document traces to a `run_key` that exists, or is marked unanalyzed.
  There is no third state.
- A component with references in use and none analyzed says so, and is not omitted.
- The same root produces a byte-identical native document twice — the same test of "verifiable"
  the ledger already passes for findings.
- Generated for ABOM's own repository, the document names the SBOM libraries this plan adds. The
  tool describes its own supply chain or it does not work.
- Module count and transitive dependency count before and after, reported.

## Open questions

- **Whether the BOM should be servable for an unowned root.** The read takes a root set, so it
  can. Whether the interface offers it is a different question: a BOM for something you do not own
  is a legitimate research artifact and an illegitimate compliance one.
- **Print.** "Otherwise printable" was in the original framing and a print stylesheet was not
  chosen. The page will print as any page does; a real `@media print` layout is not planned.
- **CSV** was not chosen either. The native JSON is the escape hatch for anyone who wants to make
  one.
