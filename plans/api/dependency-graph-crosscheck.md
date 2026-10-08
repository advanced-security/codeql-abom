# Measure native Actions coverage before filling a gap

**Status:** CodeQL's extraction is compared continuously with GitHub's own parsers, as check 1 of the
consistency checker; the reference action's parser, native lockfiles and product ingestion need
separate measurements.

## Why this exists

Native Actions lockfiles describe transitive pins. Dependabot's source consumes those locks for
updates. Neither fact, by itself, establishes how a deployed Dependency Graph, SBOM export, review
or advisory alert consumes them. ABOM must measure that boundary rather than justify a publisher
with the obsolete claim that nothing native names transitive dependencies.

The same comparison answers a second question that matters more day to day: **is our extraction
right?** ABOM parses workflows with CodeQL and nothing checks the result against an independent
reading. GitHub parses the same files for its own dependency graph. Where the two disagree, one of
the readings may be wrong or the declared coverage may differ; today that distinction is not
measured. A Dependency Graph enriched by a submission action is also not a native-parser baseline.

`internal/submit/client.go` already has `PackageURLs()`, which reads every purl GitHub holds for a
repository through the asynchronous SBOM flow and refuses to read an unready `202` as an empty
document. It is used to verify ABOM's submissions round-tripped and can support the product
comparison, provided the repository revision and the producer of its data can be established.

The parser in Jesse Houwing's `actions-dependency-submission` offers another independent reading,
including local actions and remote composition. Comparing those results belongs here, not in a
second production ingestion path. Unlike the native-SBOM comparison, it can use a fixed source tree
and recorded remote-file responses without submitting anything.

## Capabilities to distinguish

| Capability | Evidence required |
| --- | --- |
| Native lock graph and parsed coverage | Supported schema, exact source commit, workflow membership and native parser result |
| Identity/reachability verification | Native tool version, mode, diagnostics and verification time, including unknowns |
| Execution-time enforcement | Approved run at a known commit and lock blob on the named host/runner |
| Dependabot lock updates | Source support plus an observed update of workflow and lock on the target platform |
| Native DG/SBOM ingestion | Direct/transitive packages and versions observed without custom submissions |
| Dependency Review and advisory alerts | Separate product results at the corresponding revisions and configuration |
| Security or version update PR | An actual update and its trigger, not an inference from an alert |

## What this is not

**This is not a way to make discovery faster, and that was measured before it was refused.** Three
findings close it off:

- Evaluation is already nearly free. The three graph queries share one `database create` and run in
  a persistent query server, so dropping `abom/edges` in favor of an SBOM read saves one of three
  queries against a database that was already paid for.
- The database is needed regardless, because `abom/surface`, `abom/step-conditions` and the findings
  suites all read it.
- The SBOM flow is `generate-report` plus polling, against a sparse checkout of `.github`. It is not
  obviously the cheaper of the two. Both report endpoints, the plain SBOM and the dependency-review
  comparison read with repository Contents, which the App holds.

**It orders work; it never replaces it.** For an owned repository's push, the dependency-review
comparison between the analyzed commit and the live one
[claims that push ahead of the others](../../docs/architecture.md#the-pipeline) when it names a
`uses:` that moved. The analysis still runs, and nothing GitHub's graph says is written as an edge.
It reads workflows only: adding `uses: actions/github-script@v7` to `has-ghas/test-repo-1`'s root
`action.yml` left the comparison empty, so a comparison naming nothing is no evidence that nothing
moved, and it never moves a push back.

**A default-branch report is not a pinned revision.** A repository-level SBOM cannot seed a
consumer's effective graph unless its source revision and semantics match that consumer. Native
lock ingestion at the analyzed commit is a different operation, and
[built](../../docs/architecture.md#locks). Nothing here substitutes current default-branch
data for a dependency selected by an older workflow lock.

## Pending

1. **Crosscheck source extraction in isolation.** Compare CodeQL, the native lock graph/CLI JSON,
  and the reference action's parser over identical source trees and pinned or recorded remote
  content. Disable writes and invoke parsing components, never a submission entrypoint or default
  native fix mode. Generate QL results with the CLI. This needs no live test target and can start
  independently of the product comparisons below. Run continuously over sampled commits, it is
  check 1 of [the consistency checker](../../docs/architecture.md#how-it-is-tested), and the rules
  below govern it there. Check 1 reads with a YAML walk of its own and the native package's
  `ParseActionRef`, `ParseReusableWorkflowRef` and `ParseActionMeta`; the reference action's parser
  and the native CLI's JSON are still to compare, over the same trees.
2. **Establish an uncontaminated native baseline.** With explicit consent, choose a clean test
  repository containing a benign nested dependency. Confirm no ABOM or third-party snapshot owns
  its manifests; historical test1/test2 data cannot be assumed clean. If repair is needed, use the
  recorded procedure in [dependency-submission.md](dependency-submission.md) before measuring.
3. **Compare unlocked and natively locked inputs.** Hold workflow dependency choices and selected
  commits constant except for native onboarding and the lock artifact; account for any narrowing
  or ref rewrite. Observe runs at each known commit and the default-branch DG/SBOM after processing.
  Record tool/schema/host versions and configuration. A run-details lockfile and a default-branch
  report answer different questions. Depends on 2 and coordination with
  [actions-lock.md](../tools/actions-lock.md).
4. **Read each product independently.** Record direct/transitive package and version identities,
  Dependency Review, advisory outcomes and update PRs separately. Use `PackageURLs()` for the
  SBOM comparison and the existing normalization for matching package identities, keeping raw
  evidence beside them. Never execute vulnerable code just to see an advisory. Distinguish not
  processed, denied and unavailable from an empty result. Depends on 3.
5. **Explain disagreement and decide the remaining need.** Compare raw refs, selected commits,
  declaring paths, subpaths and direct/transitive meaning before package projections. Include local
  actions, self references, composites, reusable workflows, multiple entrypoints/refs, cycles and
  inaccessible inputs. Native package links do not promise CodeQL's edge locations. Classify
  coverage, parser, provenance and normalization differences, adding container cases only as that
  reader exists. If native products cover a publication use case, stop planning another publisher;
  otherwise identify the measured residual gap before revisiting expansion. Depends on the relevant
  measurements, not on an assumed product roadmap.
6. **Choose the reporting surface from the results.** Report matched, native-only, ABOM-only and
  unread/unverified sets with source and revision. A per-repository API/UI field is useful only if
  the measurement warrants it; a report may suffice. Depends on 5.
7. **Check 2, at a commit both name.** Run continuously as the consistency checker's check 2: an
  owned repository's direct dependencies against GitHub's dependency graph at the same commit,
  sampled as checks 1, 3 and 4 are — the open disagreements again, then what changed most recently,
  then a random baseline per stratum with its bound — on the checker's REST and GraphQL pools, under
  the producer rules below. GitHub's SBOM cannot be that reading: it names no commit, only when it
  was generated. The dependency-review comparison names both of its commits, so a comparison from a
  commit that declares nothing to the analyzed one reads what the graph holds at that commit; measure
  where such a base exists before building. Until a commit is named, the repository is skipped and
  counted, never compared with the analyzed one.
8. **Measure what run logs record, then check 5.** Before any run log is compared, find out which
  downloads job setup logs — nested composite steps, reusable workflows, Docker actions, actions a
  self-hosted runner already holds, steps whose `if:` was false — on consented repositories.
  Whatever it does not log is excluded from the check and named, not treated as absent. Then run it
  as the consistency checker's check 5: a workflow's closure at a commit against the actions that
  commit's run downloaded, with their SHAs, sampled on the checker's pools. Run logs read with
  repository Actions read, which the App holds for this item. Each completed `workflow_run` is
  already kept in `abom_workflow_run` with the reusable workflows it called at their commits.

## The constraints that must not be broken

**A disagreement is a question, never an automatic correction.** Neither reading overwrites the
other. Native locking supplies execution-selection evidence, not fabricated source declarations,
and DG output may mix producers or describe a different revision. Preserve the evidence rather
than writing a comparison result into `abom_edge` to make the answers agree.

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

**An independent reader is not ground truth.** A reference-action result never overwrites ABOM
rows, and a mismatch does not justify broadening either tool's coverage claims. Separate fetching
a repository/commit once from visiting every referenced entrypoint within it; a cache key that
omits the action path cannot prove that all nested dependencies were read.

## Verification

- On a repository both readings cover, the intersection is most of the set, and every element of
  either difference set is explained rather than tolerated. Do not prescribe which reader wins.
- Nested dependencies distinguish native lock coverage from plain workflow parsing. A package
  from an earlier custom submission cannot be counted as proof of native ingestion.
- A repository with the dependency graph disabled is reported as unread, and is distinguishable at a
  glance from one that was read and agreed.
- Any comparison names its source data, commit, reader version, configuration and coverage.
- The reference-action harness proves no dependency-submission request can leave it. A mutation
  dropping one subfolder entrypoint or one source-file attribution produces a named disagreement,
  while expected normalization differences do not appear as missing declarations.
- Record and explain mismatches on consented, pinned real examples as well as fixtures. A read
  denied or truncated on either side is reported as incomplete rather than agreement on zero.
- Each capability in the table has its own evidence or explicit unverified state. No delivery
  date, GHES availability or automatic transitive-alert claim follows from a public feature request.

## Prior art

[Issue #18](https://github.com/advanced-security/codeql-abom/issues/18) points to Jesse Houwing's
[actions-dependency-submission](https://github.com/jessehouwing/actions-dependency-submission).
Its [WorkflowParser](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/src/workflow-parser.ts)
and [parser tests](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/__tests__/workflow-parser.test.ts)
are the reference for the isolated comparison. Credit that implementation in comparison reports;
do not copy its test material without preserving the required notices. Compatibility experiments
that actually submit are owned by [dependency-submission.md](dependency-submission.md).

GitHub's [native lock graph](https://github.com/github/actions-lockfile/blob/go/v0.0.4/README.md)
and [Dependabot CLI engine](https://github.com/dependabot/dependabot-core/blob/main/github_actions/lib/dependabot/github_actions/lockfile/cli_engine.rb)
establish format and updater support in source. Jesse Houwing's
[request #122](https://github.com/github/gh-actions-lock/issues/122) asks for native DG reporting;
track it and official release evidence without assuming implementation or a delivery commitment.
