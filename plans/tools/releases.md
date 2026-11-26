# Installable releases

**Status:** Source onboarding and the release workflow exist; clean-host qualification, installed-asset resolution and the first regular release are pending.

## Why this exists

Deployment means installing and configuring ABOM itself. A new operator should not have to
reconstruct a maintainer's checkout, toolchain, credentials and working directories before seeing
the interface. The [README](../../README.md#getting-started) and
[deployment guides](../../docs/deployment/index.md) describe the source route; portable release
installations still need the runtime and packaging work below.

Documentation alone cannot supply the missing deployment options. The
[package target](../../Makefile) copies the executable and published interface, but not ABOM's
query packs. [PackRoot](../../internal/runner/suites.go) is `ql`, relative to the working directory;
[packSearchPath](../../internal/runner/packsearch.go) skips absent directories. An executable that
starts outside a checkout has not thereby demonstrated that it can analyze anything.

Verified GitHub Release installations are the releasable milestone; source installation remains
supported beside them. Running on Kubernetes is [its own plan](kubernetes.md). This is one workstream
with sequencing, not a separate ranked entry for each phase. Its
[parallel priority](../next.md#parallel-priority) is to proceed alongside shared-runtime work,
without demoting that work or waiting for the ranked list to finish.

## Decisions and scope

| Area | Contract |
| --- | --- |
| Source installation | Retain the supported build-and-install path and document its prerequisites. |
| GitHub Releases | Publish semver releases from `advanced-security/codeql-abom`, with binaries for Linux and macOS, each on amd64 and arm64. This is the minimum releasable new option. |
| Versioning | Application semver is independent of the CodeQL CLI and query-pack versions. |
| Recommendation | Binary installation is the lower-dependency option beside Kubernetes. |

Windows, multi-cluster federation, a Kubernetes operator,
generic arbitrary-command execution, bundled cluster/storage provisioning, package-manager
distributions and automatic updates are outside this workstream. So are new GitHub publication
modes and new UI pages.

### Parallel delivery

Organize most documentation and deployment packaging under `docs/`, `plans/` and the new `infra/`
directory so it can proceed independently of other implementation efforts. The README entry point,
Make targets and release workflows are necessary integration points, not a reason to block the
isolated work. Prefer separate, narrowly scoped changes for those shared files.

| Work | Parallel delivery boundary |
| --- | --- |
| Onboarding and documentation | Source instructions, configuration reference and release recipes can be authored alongside implementation. Publish support claims only when the corresponding route works. |
| Release packaging | Fix the asset/version contract, then develop archive assembly and native build matrices independently of unrelated product features. Runtime asset lookup is a coordinated CLI/runner change. |

### Separate pre-release prerequisites

**Assume permission to redistribute CodeQL binaries in project-built images.** This is a planning
assumption supplied by the operator, not a legal conclusion from this document. License alignment
and confirmation of the applicable distribution terms are separate pre-release work. Do not change
the [LICENSE](../../LICENSE) as part of deployment implementation.

**Base-repository GHAS Code Scanning eligibility is a separate pre-release policy task.**
[SeedOwnedRepositories](../../internal/expand/seedowned.go) does not enforce that prerequisite.
That task must establish the authoritative eligibility evidence, unknown and revoked states, and
the permitted treatment of transitive dependencies. `has_database` is provenance, not an
eligibility proxy, and a root's setting is not proof of authorization for every dependency.

Deployment consumes the approved policy instead of inventing one. Neither prerequisite prevents
local development or candidate builds; unresolved prerequisites block the relevant supported
publication and support claims. Installing a chart must not silently change graph scope or
represent skipped analysis as a clean result.

## Evidence that controls the design

| Existing boundary | Consequence for implementation |
| --- | --- |
| [Go dependencies](../../go.mod) use CGO SQLite. | Native builds or qualified C cross-toolchains are required; `CGO_ENABLED=0` is not a working release strategy. |
| [UI project](../../ui/Abom.WebAssembly.csproj) publishes static WebAssembly. | A separate UI image needs a static server, not a .NET application server. |
| [serve configuration](../../cmd/codeql-abom/serve.go) requires `--db` and defaults to `localhost:8080`; [Makefile](../../Makefile) supplies different paths and `127.0.0.1:8099`. | Document CLI defaults and source lifecycle defaults separately. Do not copy checkout-specific paths into installed-service instructions. |
| [githubToken](../../cmd/codeql-abom/discover.go) selects `GITHUB_TOKEN`, then `GH_TOKEN`, then `gh auth token`. [Identity verification](../../internal/ghauth/ghauth.go) separately invokes `gh api`. | Test one coherent identity across API reads, ownership verification and private Git fetches. Token precedence does not establish that Git is authenticated. |

## Runtime contracts

### Source and release installations

Keep the local executable serving both UI and API and executing analysis locally. Release archives
contain the native executable, published UI, ABOM runtime query/library packs, manifests and
lockfiles, workspace-resolution metadata, build/compatibility information and required notices.
Do not make publication of ABOM query packs a hidden installation prerequisite.

Use one asset resolver: an explicit asset-root override, then assets relative to the real
executable, including when it is reached through a symlink. Development targets supply the
checkout root explicitly. The invoking directory must not shadow installed packs or make a release
depend on the build machine's absolute paths.

Install the complete bundle into a versioned directory; adding only the executable to `PATH` must
not discard its assets. Keep installation assets immutable and database, work directories, caches,
custom queries and tool caches writable elsewhere. Prove operation with a read-only installation.

Document prerequisites by purpose, not as one developer-tool shopping list:

- Source builds need the repository's pinned Go/C toolchain and .NET SDK; testing adds its own
  tools. Published bundles do not require those build tools or a source checkout.
- Local analysis needs a compatible CodeQL distribution, Git and the documented GitHub identity
  setup. Lock-verification tooling belongs to the features that invoke it.
- Serving needs Graphviz: `serve` refuses to start without `dot` on its `PATH`, because every
  details page draws its figure with it. Release notes list it as a system prerequisite rather than
  carrying it, and every image that runs `serve` must install it, as every CI job that starts one
  does.
- AI authoring additionally needs its supported Copilot CLI, ql-mcp, Node and credentials. These
  are not requirements for core installation.
- Supported Linux distributions and macOS minimums follow tested toolchain/runtime constraints,
  not compilation success. Validate the pinned CodeQL bundle and Apple Silicon prerequisites.

Retain established flags and environment behavior rather than introducing a general configuration
framework. Secrets are supplied through supported credentials or references,
not committed configuration. Reuse existing preflight logic in a deployment diagnostic command
that reports missing assets, tools, permissions and writable paths without starting discovery or
writing to GitHub.

### Release archive contract

`make release-archive` and the [release workflow](#release-workflow) produce this layout; there is
no installation guide yet.

A release is `vX.Y.Z`, where `X.Y.Z` is the CodeQL CLI pinned in `.codeql-version`, or
`vX.Y.Z-<pre-release>` against that same CLI: `-rcN` for a candidate before `vX.Y.Z`, and `-next.N`
for a further release after it, by the convention the CodeQL Development MCP Server uses.
[release-version.sh](../../scripts/release-version.sh)
enforces the form and sets every pack's version to the release's; releasing never moves the CLI
pin, which `update-codeql.yml` owns. Only ABOM's own pack versions take the suffix: the CLI pin,
upstream pack pins and the ql-mcp release ABOM names are other projects' regular releases, and
`make check-release-version` plants each such leak and requires `check-versions` to refuse it.
A release candidate is dispatched rather than tagged by hand, because the tag must point at the
commit that carries its pack versions. SemVer orders `v2.27.1-next.1` *before* `v2.27.1`, although
it is made after it, so every pre-release is published as a GitHub pre-release and never marked
latest, and nothing may choose between releases by SemVer precedence. Filenames and the embedded
application version use the semver without the leading `v`. The release commit, UI, executable
and query assets must agree; dirty builds are never release candidates.

The GitHub Release contains these independently verifiable platform archives:

| Platform | Archive |
| --- | --- |
| Linux amd64 | `codeql-abom_<semver>_linux_amd64.tar.gz` |
| Linux arm64 | `codeql-abom_<semver>_linux_arm64.tar.gz` |
| macOS amd64 | `codeql-abom_<semver>_darwin_amd64.tar.gz` |
| macOS arm64 | `codeql-abom_<semver>_darwin_arm64.tar.gz` |

Each archive has one matching version/platform root directory and this installation-relative
layout. Paths in metadata use forward slashes and never name the build machine:

```text
codeql-abom_<semver>_<os>_<arch>/
  codeql-abom
  ui/
  ui.cdx.json
  ql/
  codeql-workspace.yml
  build-info.json
  LICENSE
  THIRD_PARTY_NOTICES
```

`ui/` contains published `wwwroot` contents, including the browser framework. `ui.cdx.json` lists,
as CycloneDX, the NuGet packages that interface was built from; the release SBOM reads it because
no scanner finds packages in published WebAssembly, and it sits outside `ui/` so it is not served.
`ql/` contains the runtime query/library packs and their full local import closure, manifests and
lockfiles, with the workspace descriptor needed to resolve local dependencies. No developer
databases, test execution output, credentials or operator state belong in the archive. The release
does not bundle a local CodeQL distribution: operators install the separately documented compatible
toolchain. Worker images have their own explicitly approved CodeQL distribution contract.

`build-info.json` has this schema. Its version, revision, dirty state, target and CodeQL identity
must match the executable's embedded machine-readable identity. Packaging adds the asset inventory;
the executable does not recursively embed its own file hash.

| Field | Meaning |
| --- | --- |
| `schemaVersion` | Integer `1`; unknown schema versions are refused, not guessed. |
| `version` | Application semver matching the release tag and filename. |
| `revision` | Full source commit ID used for all application assets. |
| `dirty` | Boolean; release candidates require `false`. |
| `target.os`, `target.arch` | One of the platform pairs in the archive matrix. |
| `codeql.version` | Exact supported CLI version from the release commit's CodeQL pin. This is independent of application semver. |
| `assets.ui`, `assets.packs`, `assets.workspace` | Relative paths `ui`, `ql` and `codeql-workspace.yml`. |
| `packs` | Runtime pack names, versions and content digests, including the dependency identities needed to reproduce resolution. |
| `buildTools` | Actual Go/C and .NET build tool versions; not a claim those SDKs are required at runtime. |
| `platformRequirements` | What the executable demands, measured from the binary: on Linux the newest glibc symbol version it links, on macOS the minimum OS its load commands name, and the image it was built on. A measured floor is not a qualified one until the archive has run there. |
| `files` | Relative path, byte length and SHA-256 for every other shipped file. Exclude this manifest from its own hash inventory. |

Ship `SHA256SUMS`, a matching `codeql-abom_<semver>_<os>_<arch>.sbom.spdx.json` per archive, and
downloadable attestation bundles consumable by the pinned GitHub verification tooling. Checksums
cover each archive and SBOM. Attestations bind their digests to the expected repository, workflow
and revision; a checksum alone is not producer authentication. Promote the tested bytes, never
rebuild after signing or testing. Runtime verification failures name the mismatched artifact or
unsupported requirement rather than continuing with a different pack or CLI.

Do not fill in operating-system floors from the developer's machine. Linux qualification must
establish a glibc/native-library baseline compatible with both the CGO binary and pinned CodeQL
distribution, and run on that baseline. macOS qualification must establish deployment targets for
both architectures and verify the pinned CodeQL prerequisites, including Rosetta/Xcode tools
where required. Compare the requirements of every enabled runtime tool; the strictest applies.
Record the proven floors in the candidate manifest and release documentation before publication.
An unqualified matrix cell blocks release of that supported matrix, not just its smoke test.

### Release workflow

[release.yml](../../.github/workflows/release.yml) is the single entry point, on a `v*` tag push or a
dispatch naming the version. Each stage has only the permissions it uses, and the stages that write
run in deployment environments:

| Stage | Workflow and environment | What it writes |
| --- | --- | --- |
| Version | `release.yml` | Nothing; refuses a version that is not a release of the pinned CLI |
| Tag | `release-tag.yml`, `release-tag` | The tag, only for a dispatched version that has none, after the Go tests pass on the tree it tags. They run in [test-go.yml](../../.github/workflows/test-go.yml), the job pull requests run. An existing tag is checked and never moved. |
| Build | `release-build.yml` | Nothing. Tests the tagged commit, builds each archive on its own platform's runner and smoke-tests the uploaded bytes there; darwin/amd64 is smoke-tested under Rosetta on arm64, because Homebrew has no Intel bottle for Graphviz. |
| Draft | `release-publish.yml`, `release-draft` | Provenance attestations, then a draft release with the archives, SBOMs, `SHA256SUMS` and the attestation bundle |
| Publish | `release-publish.yml`, `release` | Publishes the draft, after checking it holds exactly the staged assets |

The [smoke test](../../scripts/release/smoke.sh) verifies the manifest against the extracted bytes
and the executable's identity, starts the server from the archive root and reopens its database,
then installs the shipped packs' dependencies and requires results from ABOM's own findings and
graph queries against the pinned CLI. Every job that needs Graphviz installs it with
[setup-graphviz.sh](../../scripts/setup-graphviz.sh), which adds the layout engines that Ubuntu
26.04 splits into a separate package and checks that each one draws. Attestations name
`release-publish.yml` as the signing workflow. Artifact attestations on a private repository need
GitHub Enterprise Cloud.

The repository needs, once: the three environments, with `release-tag` limited to `main`,
`release-draft` and `release` limited to `main` and `v*` tags, and required reviewers on `release`
(and on `release-tag` when tagging should also need a person); a `v*` tag ruleset, if any, that
lets the workflow's token create tags; immutable releases; and the four runner labels in the build
matrix. A missing label fails its cell rather than dropping the platform.

## Pending implementation

### Phase 1: Source-install qualification

1. **Qualify the documented recipe on a clean host.** Follow the
  [source guide](../../docs/deployment/source.md) with fresh tool and package state, an authorized
  GitHub identity and an isolated database. Confirm ownership, private Git access where needed,
  UI startup and an explicitly permitted first analysis. A fresh checkout that reuses an
  installed SDK and package cache is a useful smoke test, not this clean-machine acceptance gate.
  Record the tested environment and any unmet prerequisites before calling this phase qualified.

The [release archive contract](#release-archive-contract) is fixed for the following implementation
phase. User-facing release and Kubernetes recipes are added with their usable deployment routes,
not ahead of them. Source qualification must not modify an existing installation or start an
organization-wide crawl merely to validate the instructions.

### Phase 2: Installed runtime and verified binary releases

1. **Resolve installed assets from the executable, test first.** `PackRoot` is `ql` relative to
   the working directory and `--ui-dir` has no default, so an extracted archive works only when
   started from its own root with `--ui-dir ui` — which is all the smoke test claims. Add the one
   resolver the contract describes and the deployment diagnostic, preserving source invocation.
   Test unrelated working directories, symlinks, spaces and read-only installations, and resolve
   workspace pack dependencies without rewriting installed files. Then make the smoke test start
   the server from elsewhere.
2. **Publish the first regular release, `v2.27.1`.** Every stage has run for the release
   candidates except the one a regular release takes: publishing with `--prerelease=false
   --latest` in [release-publish.yml](../../.github/workflows/release-publish.yml). Correct this
   plan with anything that run refutes. Independent of item 1: the release notes already say to
   start the server from the archive root.
3. **Qualify the platform floors.** Linux archives are built on Ubuntu 22.04 images and record
   their newest glibc symbol; macOS archives target 12.0, Go's own floor. Run each archive on its
   recorded floor with the pinned CodeQL, including the Xcode requirements on Apple silicon, before
   any floor appears in release documentation. Serving and analyzing need no Rosetta there, since
   ABOM traces no build and the Actions extractor is native; the floor run is what confirms it. The
   candidates' archives are enough to start on. GitHub retires the `ubuntu-22.04` and
   `ubuntu-22.04-arm` runner images in April 2027, after brownouts in which every job on them
   fails, so the Linux archive and smoke rows need a new home first: an `ubuntu:22.04` container on
   a supported runner keeps the glibc floor, and a newer image raises it.
4. **Exercise the actual download path.** Document download, provenance/checksum verification,
   extraction into a versioned user-writable prefix, PATH setup, configuration and startup.
   Use appropriate Linux/macOS verification commands and an expected repository/workflow with
   `gh attestation verify`. Test corrupt and wrong-producer artifacts. Exercise macOS quarantine
   and Gatekeeper on downloaded archives, whose executable is unsigned; resolve
   signing/notarization needs without instructing users to disable protections. Add
   `docs/deployment/releases.md` and its onboarding links only for the qualified route. No
   curl-to-shell installer. Depends on items 1 to 3.
5. **Define supported upgrade behavior.** Preserve state outside the install prefix; test forward
   migration and restoration on copies. Once a prior release exists, qualify upgrades from the
   declared supported versions, ordering releases by publication rather than SemVer precedence.
   Do not invent compatibility shims for unreleased development states or promise that
   downgrading a binary reverses a schema migration. Depends on the backup/restore contract and
   an actual release.

This phase is the minimum releasable addition and must be able to ship independently of
Kubernetes work.

## Related work and ownership

- [Enterprise server](../api/enterprise-server.md) owns the App identity, the production address
  and workflow-caller authorization; [access](../api/access.md) owns sign-in and roles. This plan
  owns packaging releases.
- [Backup and restore](../db/backup-and-restore.md) owns the authoritative SQLite backup/restore
  commands and epoch behavior. This plan consumes them and adds the task/artifact recovery
  contract; it does not implement a second restore engine.
- [GitHub hosts](../api/github-hosts.md) owns destination and credential routing. Containerization
  does not certify GHES or another host, and does not change which organizations ABOM reads.
- [Checkout cache](../db/checkout-cache.md) and [database cache](../db/database-cache.md) retain
  ownership of identity and retention semantics. Coordinate host-aware and distributed reuse
  instead of changing their policies incidentally.
- [Database bundling](../db/codeql-db-bundling.md) owns the local retained-storage decision.
  Bundling for cross-pod transport is needed here; it does not silently adopt archives as the
  local cache's storage format or erase that plan's latency questions.
- [Pack promotion](../ql/pack-promotion.md) remains deferred. Bundling ABOM's runtime packs is
  separate from publishing query packs for use outside ABOM.
- [Closure databases](../../docs/architecture.md#analysis-modes) are a command whose results are
  extended evidence no derived number reads, and that settles staged-source semantics. Carry their
  mode and source identities through tasks; do not implement or reinterpret staging here.

## Implementation surfaces

Keep edits at the owning boundaries and reuse neighboring tests. New packages are justified by
the real local/remote execution and artifact contracts, not by a generic plugin framework.

| Surface | Intended work |
| --- | --- |
| [README](../../README.md), [docs index](../../docs/index.md), [architecture](../../docs/architecture.md) | Short onboarding, supplemental links and implemented runtime facts; no future support claims. |
| [Makefile](../../Makefile), [CLI entry point](../../cmd/codeql-abom/main.go) | Complete packaging, version/diagnostic/worker entry points and explicit source asset roots. |
| [Serve lifecycle](../../cmd/codeql-abom/serve.go), [discovery wiring](../../cmd/codeql-abom/discovery.go), [authoring wiring](../../cmd/codeql-abom/authoring.go) | Backend configuration, restart behavior, capabilities and worker orchestration. |
| [Runner suite discovery](../../internal/runner/suites.go), [pack search](../../internal/runner/packsearch.go) | One installed/source/custom-pack resolution contract. |
| [Preparation and ingestion](../../internal/expand/expand.go), [requested runs](../../internal/expand/executor.go), [work pool](../../internal/work/pool.go) | Typed local/remote execution while preserving evidence and serialized persistence. |
| [Frontier](../../internal/frontier/queue.go), [store schema](../../internal/store/schema.go) | Attempt/receipt persistence, reconciliation and fenced acceptance; new SQL through store APIs. |
| [HTTP server](../../internal/serve/serve.go), [control guard](../../internal/serve/discovery.go), [AST](../../internal/serve/queryast.go) | Separate internal worker protocol, health/status and existing browser/API integration. |
| [Authoring sessions](../../internal/harness/session.go), [Copilot adapter](../../internal/harness/copilot.go), [confinement](../../internal/harness/confine.go) | Remote optional execution without losing workspace or credential boundaries. |
| [UI project](../../ui/Abom.WebAssembly.csproj), [browser tests](../../ui/tests/e2e/) | Static image publication and existing-view state/interaction coverage. |

Release/deployment workflows belong under `.github/workflows/`; infrastructure files remain under
`infra/`. New Actions dependencies must follow the repository's native workflow-lock tooling and
regenerate its lock; never hand-author generated lock entries. Source/binary runtime must not need
a Kubernetes configuration or object store merely because a remote backend exists.

## Constraints that must not be broken

**Packaging is not proof of analysis.** A binary or pod that answers HTTP but cannot resolve and
execute the promised queries is not a supported deployment.

**Only coordinators own semantic state, and one schedules at a time.** Workers return evidence,
never database writes or policy decisions. Connection-pool settings, volume labels and successful
retries are not proofs of exclusive scheduling or ingestion idempotency.

**A retry is not new evidence until accepted.** Attempts retain immutable source/query identity;
late, canceled or incompatible results cannot advance a ledger or declare coverage complete.

**The transport cannot change the answer.** Preserve lock-selected revisions, seed/upstream
provenance, analysis modes, query-kind routing, finding attribution and unknown-versus-zero
measurements. Worker failure is visible coverage loss, never a clean result.

**Installation does not grant identity or publication authority.** Separate read credentials,
worker capabilities, authoring credentials and any existing guarded write path. Do not activate
GitHub writes through release tests, onboarding or Helm hooks.

**Qualification is explicit.** Missing native runners, CodeQL, cluster capabilities or artifact
services fail their configured gate rather than silently skipping it. Do not claim a platform,
storage provider or operation works because a neighboring one passed.

## Verification

Implementation is test-first. Use focused checks for each change and the repository's full release
gates before declaring a milestone qualified. Run CodeQL tests/integration and `make verify` only
on a quiet test environment, never alongside the operator's discovery crawl. Use `make itest` for
runner/protocol changes and `make selfcheck` for real CLI validation. Do not replace the repository
gate with an asserted hand-assembled equivalent.

Take real-corpus baselines with SQLite's online backup on a normal connection, never `cp` or a
live source opened through `immutable=1`. Tests and comparisons use copies, not the working store.
Generate BQRS/SARIF fixtures through the real CLI; do not hand-write output shapes.

| Gate | Required evidence |
| --- | --- |
| Documentation | Clean-operator source and release recipes, valid local links and Markdown diagnostics. Docs-only edits do not run the full code suite. |
| Native release matrix | All four targets unpack without a checkout or build SDKs; SQLite open/write/reopen, UI loading, cold-pack resolution, real permitted extraction/findings, graph layouts and private Git/identity checks succeed. |
| Provenance | Expected producer/workflow verification passes; tampered, wrong-platform and wrong-producer artifacts are refused. Downloaded macOS installation works without disabling platform protections. |

Measure peak CPU/RAM/scratch, cold/warm execution costs, artifact volume and transfer time on a
named corpus to justify defaults. Do not substitute guessed organization-size capacity claims.
External integration tests require approved credentials and isolated targets; no test submits to
an operator's production repositories.

Release workflows require explicit publication approval and narrowly scoped permissions. Creating
the implementation does not authorize staging, committing, tagging, pushing, publishing a release
or image, changing repository protection, or provisioning real infrastructure.

## Upstream qualification references

- [CodeQL CLI setup](https://docs.github.com/en/code-security/codeql-cli/getting-started-with-the-codeql-cli/setting-up-the-codeql-cli)
  describes platform bundles, glibc compatibility and Apple Silicon prerequisites. Recheck against
  the pinned distribution during release qualification.
- [CodeQL CLI v2.27.1](https://github.com/github/codeql-cli-binaries/releases/tag/v2.27.1) provides
  evidence that a Linux ARM64 distribution exists; it is not proof of this application's support
  matrix or every feature's compatibility.
- [CodeQL terms](https://github.com/github/codeql-cli-binaries/blob/main/LICENSE.md) are input to
  the separate pre-release review. Redistribution authority remains the stated planning assumption,
  not something this deployment plan certifies.
