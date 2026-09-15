# GitHub already parsed the first hop, and we never compare

**Status:** not started. The client that would read the comparison already exists and is used for
something else.

## Why this exists

ABOM's central claim is that GitHub's Actions dependency graph stops at the first hop and ABOM
carries the rest. That claim is sourced from GitHub's documentation, not from evidence. The house
rule in [`../next.md`](../next.md) is that a number names the data it was measured against, and this
one has no number behind it at all.

The same comparison answers a second question that matters more day to day: **is our extraction
right?** ABOM parses workflows with CodeQL and nothing checks the result against an independent
reading. GitHub parses the same files for its own dependency graph. Where the two disagree, one of
them is wrong, and today nobody finds out.

`internal/submit/client.go` already has `PackageURLs()`, which reads every purl GitHub holds for a
repository through the asynchronous SBOM flow and refuses to read an unready `202` as an empty
document. It is used only to verify ABOM's own submissions round-tripped; pointing it at a
repository ABOM has analyzed is the whole mechanism.

## What this is not

**This is not a way to make discovery faster, and that was measured before it was refused.** Three
findings close it off:

- Evaluation is already nearly free. The three graph queries share one `database create` and run in
  a persistent query server, so dropping `abom/edges` in favor of an SBOM read saves one of three
  queries against a database that was already paid for.
- The database is needed regardless, because `abom/surface`, `abom/step-conditions` and the findings
  suites all read it.
- The SBOM flow is `generate-report` plus polling, against a sparse checkout of `.github`. It is not
  obviously the cheaper of the two.

**And the default branch is the wrong commit.** GitHub's SBOM describes a repository's default
branch. ABOM analyzes the commit a consumer pinned. For every transitive dependency — 93% of the
analysis — those are different trees, so an SBOM read cannot seed the frontier without quietly
substituting HEAD for the pin. Seeding is refused for that reason rather than left to revisit.

## What measuring already showed

Measured on the seed tier of a crawled organization, against the files GitHub says it reads —
workflows "must be located in the `.github/workflows/` directory of a repository to be recognized as
manifests":

| Source of the edge | Edges | Distinct targets |
| --- | --- | --- |
| `.github/workflows/**`, which GitHub parses | 764 | 70 |
| Anywhere else, which it does not | **25** | **12** |

So GitHub's reading covers about 97% of direct edges and misses **12 distinct dependencies
outright** — the ones declared inside the repository's own composite actions, where a first hop for
ABOM is not a manifest for GitHub. That asymmetry is why the comparison is interesting both ways.

## Pending

1. **A post-check on a repository ABOM has analyzed.** For each seed repository, read
   `PackageURLs()` and compare against ABOM's direct set for the same repository. Record three
   figures per repository: present in both, GitHub only, ABOM only. The comparison is on purls, so
   `internal/normalize/purl.go` is the shared vocabulary and is already derived from GitHub's own
   exports.
2. **A reading of each disagreement class.** *GitHub only* is the interesting one and the reason to
   build this: it is either an extraction defect or a reference ABOM cannot represent. *ABOM only*
   is expected, and quantifying it is what turns the value proposition into a measurement.
3. **Where it surfaces.** A per-repository figure on the Repositories page is the obvious home, but a
   number nobody looks at is not worth an API call, so this may be a report rather than a screen.
   Decide after the first measurement says how often the two disagree.

## The constraints that must not be broken

**A disagreement is a question, never an automatic correction.** Neither reading overwrites the
other. GitHub's graph is not ground truth — it does not read composite actions, and it describes a
branch rather than a commit — and ABOM's extraction is the thing under test. Writing GitHub's answer
into `abom_edge` would destroy the evidence the comparison exists to produce.

**A repository whose dependency graph is off is unknown, never empty.** The feature can be disabled,
and a token can lack the scope to read it. An unreadable SBOM is a coverage gap that says so, not a
repository with no dependencies, and the existing `202` handling in `PackageURLs()` is there because
the same mistake was available one layer down.

**Compare like with like or not at all.** ABOM's set for a repository must be taken at the commit
GitHub's SBOM describes, which for a seed repository is the default branch and for a pinned
dependency is usually not. If the commit cannot be matched, the comparison is skipped and counted as
skipped, because a mismatch caused by comparing two different trees looks exactly like an extraction
defect.

**The reference is the evidence.** A purl normalizes a reference, so a comparison that only ever
holds purls cannot tell a moved tag from a different action. Keep the raw `uses:` value beside the
purl in whatever this records.

## Verification

- On a repository both readings cover, the intersection is most of the set, and every element of
  *GitHub only* is explained rather than tolerated.
- The known asymmetry appears where it should: a repository with a composite action under
  `.github/actions/**` produces *ABOM only* entries, and one without produces few or none.
- A repository with the dependency graph disabled is reported as unread, and is distinguishable at a
  glance from one that was read and agreed.
- Any figure about ABOM's value names the corpus it came from and when it was measured.
