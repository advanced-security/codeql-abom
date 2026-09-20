# Record the container references Actions actually declares

**Status:** broader declaration inventory is not built; the literal-source coverage spike can start locally.

## Why this exists

`uses: docker://...` is already an ABOM node, edge and bill-of-materials component. It is not the
whole container dependency surface: jobs and services name images, and Docker action metadata can
name an image or a Dockerfile whose `FROM` declarations name more. The existing
[container-images.md](container-images.md) fixes account grouping only; it must remain a small
correction rather than acquiring all of this work.

The checkout's sparse patterns include workflow content and action manifests, not every referenced
Dockerfile. A parser cannot distinguish no base-image declarations from a file never fetched unless
the acquisition and coverage contract makes that distinction explicit.

## Pending

1. **Measure and declare source coverage.** Build a small source tree covering the forms below and
   inspect what the current Actions extractor and ABOM queries already provide. Reuse that evidence
   before adding extraction. Record repository, commit, source file/location, owning job/action and
   declaration context for each reference. Missing, dynamic, unsupported and not-fetched inputs are
   coverage states, not empty lists. Depends on nothing.

   | Source | Required forms |
   | --- | --- |
   | Job container | String and object-with-image declarations |
   | Service container | Each service's image |
   | Docker step | `uses: docker://...`, without duplicating existing edges |
   | Docker action | Local and remote `runs.using: docker`, with image or Dockerfile path |
   | Referenced Dockerfile | Each external `FROM`, with stage and source location |

2. **Acquire the missing source at the analyzed commit.** Follow action-relative Dockerfile paths
   in local and remote actions with bounded file/byte reads, not a recursive whole-repository
   fetch. Reject path and symlink escapes. Distinguish a missing file from a fetch failure and
   never substitute default-branch content for a pin. Coordinate sparse inputs with
   [checkout-cache.md](checkout-cache.md) and the adapter contract in
   [semantic-model.md](../ql/semantic-model.md). Depends on 1.
3. **Parse declarations, not display strings.** Use CodeQL for the Actions syntax it exposes and a
   proven Dockerfile parser for `FROM`; do not introduce another whole-workflow parser or a
   regex-only Dockerfile grammar. Keep the original reference alongside registry, name, tag and
   digest facts. Handle registry ports, Docker Hub aliases, tag-plus-digest references, platform
   options, stage aliases and `scratch`. Unresolved `ARG` and Actions expressions retain structured
   uncertainty rather than becoming literal image names. Depends on 1 and 2.
4. **Store and project the declaration kinds honestly.** Distinguish job/service use, action images
   and build-stage base references; a stage alias is not an external image and a build-stage base
   is not proof of runtime inclusion. Carry provenance and coverage through the API and existing
   bill-of-materials exports. Any new graph relation must state which traversals include it, and
   must not silently inflate Actions reach or submission closure. Depends on 3.
5. **Validate container identity in exports.** Decide the interoperable container PURL representation
   using the chosen export format and real consumer checks, retaining raw references and stable
   component identity. This is local inventory/export work, not permission to send container PURLs
   to GitHub. Keep broader all-package BOM changes outside this plan. Depends on 4.

## Dependencies and boundaries

The shared uncertainty states and adapter contract belong to
[semantic-model.md](../ql/semantic-model.md). Literal declaration coverage can be measured without
waiting for its dynamic/dataflow resolution; any new adapter must declare its source inputs and
record whether those inputs were fetched. Enabling it requires the relevant re-fetch/re-analysis,
not treating older analyses as if they covered containers.

The existing account-grouping correction remains [container-images.md](container-images.md).
This work does not execute builds or images, pull layers, inspect installed packages, evaluate
shell commands or Actions conditions, scan registries, or infer vulnerability from image identity.
It does not promise Dependency Graph alerts or submit container packages to GitHub.

[Native Actions locks](actions-lockfile.md) select the repository commit from which action metadata
and Dockerfiles must be read. That selection does not lock a registry image tag, enumerate image
contents or cover every runtime download. Keep native action-pin coverage and container-declaration
coverage separate; consume the selected action commit rather than resolving its live tag again.

## The constraints that must not be broken

**A declaration is evidence, not an inventory of image contents.** Each base reference names its
stage and source. Neither a `FROM` nor a registry name says which packages execute or are vulnerable.

**The reference and its uncertainty survive normalization.** A supplied tag and digest both remain
visible. A registry namespace is not a GitHub account; no new container is assigned fabricated
ownership. Defaults used for an export must not overwrite what the source declared.

**Unfetched is not empty.** Record which inputs and adapter version produced the answer. Changing
sparse inputs invalidates the relevant coverage claim rather than silently reusing an old result.

## Verification

- Generate Actions/BQRS fixtures with the real CodeQL CLI. Dockerfile parser tests use actual
  source files. Cover local/remote action paths, two subfolder actions at one repo/ref, different
  refs, job/service images, registry ports, tag-plus-digest and bare images.
- Cover multi-stage aliases, `scratch`, platform options, literal and unresolved arguments,
  malformed files, missing inputs and path escapes. Assert complete result identities and source
  locations, not only a package count.
- A warm run reuses already fetched commit inputs; a new adapter input set triggers the necessary
  fetch. Assert bounded requests and explicit truncation/failure coverage.
- On a SQLite backup of the recorded graph, explain every added declaration/component. Existing
  Actions findings, reach, exposure, pinning and submission results remain unchanged; repeated
  ingestion adds nothing. Extend to the shared deterministic test data after source-fixture review.
- API, rendered values and BOM serialization preserve context, tag/digest evidence and coverage;
  exported documents pass schema and consumer checks. GitHub acceptance or alerting is not inferred
  from a locally valid PURL or a mocked snapshot test.

## Prior art

Jesse Houwing's
[actions-dependency-submission](https://github.com/jessehouwing/actions-dependency-submission)
provides useful prior art in its
[WorkflowParser and Dockerfile parsing](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/src/workflow-parser.ts)
and [Docker integration tests](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/__tests__/integration-docker.test.ts),
following [issue #18](https://github.com/advanced-security/codeql-abom/issues/18). Adopt the breadth
of declaration sources and parser-based extraction, while verifying each source against ABOM's
pinned-commit and provenance requirements. Snapshot-shape tests are not evidence that an external
service scans container contents or produces vulnerability alerts.
