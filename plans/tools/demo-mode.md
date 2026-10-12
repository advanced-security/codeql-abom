# Practice safely in a startup-only Demo instance

**Status:** planned; the local scripted foundation precedes an ephemeral Codespaces profile with real Copilot authoring.

## Why this exists

Read-only tutorials teach the interface but cannot let a newcomer practice the QLaaS loop or
see deliberately chosen direct/transitive dependencies and evidence. ABOM already has a
reproducible corpus; it needs qualified training scenarios and enforced isolation, not a second
disconnected mock interface. A later Codespaces prebuild must make the same experience available
without a workstation setup, using real Copilot SDK authoring and CodeQL over fictional repositories.

## Dependencies and boundaries

- Extends the [Tutorial experience](../../docs/tutorials.md)'s definitions, checklist, spotlight
  and action policy. Its read-only behavior must remain independent of Demo.
- Reuse the corpus generator, generated fixtures, store APIs, authoring sessions and local
  CodeQL execution. Do not add a parallel fake data/query engine.
- Demo is an instance startup mode, not a checkbox, URL parameter, per-request dataset selector
  or live switch. It owns a dedicated database, work directory, query pack and address.
- Leaving Demo requires stopping the instance and replacing its database/workspace with a
  normal setup. The UI explains that; it provides no Exit Demo toggle.
- The foundation supports local SQLite and one operator per instance. Stage 6 adds one isolated
  demo workspace per Codespace, not a shared multi-user ABOM server. PostgreSQL demo provisioning,
  distributed demo execution and pack publication remain outside this plan.
- The local scripted profile is offline. The Codespaces profile permits only qualified Copilot API
  traffic from ABOM and its agent/MCP children; all repository, advisory and code-scanning data comes
  from its loopback-only simulated GitHub provider. Provisioning may download pinned tools and
  dependencies; neither runtime silently installs a missing dependency or fabricates a passing test.

### Two startup profiles, one Demo boundary

| Profile | Authoring | GitHub analysis data | Mutable exercise state |
| --- | --- | --- | --- |
| Local scripted, stages 1-5 | Deterministic scenario adapter | Generated corpus and recorded comparison inputs | Dedicated local workspace; persists until explicit reset |
| Codespaces Copilot, stage 6 | Actual Copilot SDK/CLI and pinned local QL MCP server; real GitHub calls limited to Copilot APIs | Read-only simulated GitHub API on loopback, backed only by demo fixtures | Private to one Codespace lifecycle; fresh after stop/start or rebuild |

Neither profile becomes normal mode through the UI. The Codespaces profile does not replace
the deterministic profile used for offline tests. A missing Copilot entitlement or credential
disables agent authoring with a reason; it never silently substitutes the scripted adapter.

## Startup contract and durable identity

Proposed explicit CLI contract:

1. `codeql-abom demo init --dir <demo-root>` creates a dedicated workspace from bundled demo
   sources/recordings. It refuses nonempty/unmarked destinations and never overwrites a normal
   database. `demo reset --dir <demo-root>` acts only on a stopped, marked demo.
2. `codeql-abom serve --demo --demo-dir <demo-root> --addr <demo-address> --ui-dir <assets>`
   serves that instance. The root determines database, local query pack and work directories;
   conflicting flags and arbitrary production path overrides are refused.

The initial authoring profile is scripted. Stage 6 adds an explicit startup selection such as
`--demo-authoring=copilot`, coupled to a validated Codespaces demo manifest and simulated provider.
It is not a runtime tool-path switch or permission to attach a production GitHub reader.

Store a typed dataset marker through `internal/store`, the only SQL owner. A small workspace
manifest records dataset ID, corpus/scenario format version, toolchain contract, permitted
local paths and authoring/provider profile. Check marker/manifest agreement before normal
service composition.
Shared schema changes remain additive and compatible with both supported database backends;
Demo provisioning itself supports SQLite only.

Opening a marked demo database without demo startup is refused. Demo startup over an unmarked
normal database is refused. Never infer mode from repository names or corpus-like graph contents.
Unknown versions, absent required fixtures, path/symlink escapes, incompatible CodeQL and partial
initialization are errors before serving.

A read-only instance-capability endpoint exposes dataset ID, `normal`/`demo`, scenario version
and permitted practice capabilities, not filesystem paths or secrets. Immutable server startup
state is authoritative. Browser state cannot enable Demo. Normal instances and reader-only
instances retain their existing behavior.

## Isolation by construction

Branch in [`runServe`](../../cmd/codeql-abom/serve.go) before App/token discovery, normal supervisor
construction, toolchain auto-install, GitHub readers, webhooks/relay and reference polling.
Construct only the store/static UI, local resolver/tester/printer, confined authoring sessions,
local executor and demo-data readers. The scripted profile uses recorded inputs; stage 6 additionally
constructs the simulated provider and a separately credentialed Copilot adapter. Never construct
a production repository reader and later disable it.

### Backend capability policy

Permit a small explicit set of local mutations:

- File/edit a request from pasted or bundled local examples.
- Start/stop an authoring session admitted by the immutable startup profile.
- Run the selected custom query's real tests and PrintAST.
- Start/cancel a bounded regular run over local fixture databases.
- Compare that run with a compatible, labelled recorded or simulated code-scanning baseline.
- Advance stages under existing readiness gates, including local-only `deploy`.
- Change a demo-local tier suite binding for the deployment lesson; discovery remains unavailable
  and the screen says the binding schedules nothing in this offline instance.

Permit saved scope changes only where an exercise requires them, with existing scope semantics.
All other control endpoints deny with a 4xx explanation and standard logging, even if the UI
hides them. Audit the entire route table, including GET handlers with external side effects.
Unknown/new control routes deny by default.

Explicitly deny production discovery/enumeration, real GitHub identity/membership APIs,
arbitrary linked-example fetch, live alerts/databases, dependency submissions, code-scanning
setup writes, webhook ingestion, relays, remote lock verification, tool installation/update,
arbitrary agent configuration, destructive prune/compact and share/export/publish routes,
including read-method download endpoints. Read endpoints use only demo-local backends; replace
nil helpers that default to gh with explicit demo refusals/readers. Stage 6 may admit read-only simulated enumeration
and linked examples within its manifest; it never enables a fallback to real GitHub.

No production repository credential is looked up or inherited by demo-data services or CodeQL.
Supply explicit child environments. The scripted profile never launches Copilot, ql-mcp, npx,
gh, git fetch/clone or arbitrary commands; its scripts are typed Go operations, not shell strings.
Stage 6 permits only the pinned Copilot CLI and local QL MCP executable through the existing
authoring gate. Only that Copilot adapter receives the user's explicitly supplied Copilot
credential. No runtime `npx -y`, arbitrary tool installation or generic credential forwarding.

Canonicalize and confine query imports, writes, fixture/database paths and outputs under the
marked workspace plus approved read-only CodeQL pack roots. Reuse runner locking, cancellation
and diagnostics. Missing/corrupt databases are rebuilt from local fixtures or refused, never
fetched through the normal crawler.

Loopback is the service boundary. Stage 6 exposes only ABOM's UI through a private Codespaces
forwarded port, with exact host/origin handling. The simulated provider is not forwarded.
Public or shared multi-user sandboxing is not promised.

## Dedicated demo dataset from the existing corpus

Extend existing corpus sources and generation, then derive a **demo profile** from that shared
fixture corpus. Keep one canonical source/fixture location under repository-root `testdata/`,
not copies per package. Preserve ordinary empty/populated CI profiles and structural assertions.

[`cmd/abom-corpus`](../../cmd/abom-corpus/main.go) is currently a development-only `package main`.
Extract only reusable builder/profile operations into a narrow internal corpus package, keeping
the command a thin caller and adding demo initialization as another. Installed Demo must not
spawn `go run`, require a source checkout or duplicate the builder. Pass a validated empty demo
destination; do not expose the builder's current unconditional SQLite reset to arbitrary paths.

The scenario manifest maps stable scenario IDs to generated keys, query IDs, fixture commits and
required evidence. It identifies real stored rows, not mock UI responses. Validation refuses
absent/incompatible data; tests assert all six Tutorial outcomes are coverable.

### Required scenarios

| Scenario | Reproducible content and outcome |
| --- | --- |
| Orientation | Explicit demo ownership, owned and third-party dependencies, populated/incomplete coverage, CodeQL tabs and navigable Inventory rows |
| QLaaS lifecycle | All authoring icons/states, all five stages, refused advances, current/stale toolchain evidence and failed/stopped/interrupted sessions |
| New rule | Starting request, positive/negative examples, stub failing on marks, scripted correction, passing tests, AST, regular run and compatible recorded comparison |
| FP correction | Existing custom version reporting a compliant case; regression fails first; correction removes the FP and preserves true detections |
| FN correction | Existing version missing a marked case; regression fails first; correction finds it without reporting compliant cases |
| Advisory reach | Attributed range/observed refs, reached/unreached cases, direct/multi-hop routes, owned/upstream split and unknown coverage |
| Finding reach | Genuine rule/location/version attribution and scope-compatible paths, without invented dependent vulnerability verdicts |
| Action dependents | Owned reusable/composite and third-party Actions, multiple refs/commits, governing lock, direct/transitive users, leaf and genuinely unknown measurement |

Use fictional organizational identities and label their ownership **demo declaration**. Preserve
attribution of real upstream Actions or published advisories while labelling the repository usage
scenario synthetic. Do not ship private source, credentials, live database backups or real
authoring transcripts.

Separate recorded examples from practice starting states. New-rule exercises start without the
finished target; improvement exercises start with the flawed version. Completed/stale examples
can have their own catalog entries, never masquerade as the exercise's beginning.

### Generation and qualification

- Change source trees and regenerate BQRS/SARIF with the CodeQL CLI; never hand-edit output.
- Correct [`queries.go`](../../cmd/abom-corpus/queries.go)'s obsolete `layAccept` teaching sequence
  and completion wording. Generate expected results from fixture marks before query changes.
  Intentionally bad expectations are labelled refusal examples, not recommended workflow.
- Regenerate [`authoring.json`](../../testdata/corpus/authoring.json) through `--record-queries`
  and [`regenerate-corpus-fixtures.sh`](../../scripts/regenerate-corpus-fixtures.sh).
  Validate source/query/library/fixture/toolchain digests against recordings.
- Build regular local Actions databases during provisioning or restore compatible synthetic
  bundles. Read CodeQL identity from actual database metadata; another CLI's recorded pass
  cannot satisfy current readiness.
- Replay structural recordings through existing ingest/store APIs and rebuild projections
  normally. New practice results also use existing ingestion/attribution.
- Verify manifest counts, routes, reference identity and FP/FN outcomes against actual generated
  results, rather than putting invented constants in lesson copy.
- Qualify source attribution/licensing before release bundling. Exclude unqualified corpus
  material from the demo profile; do not blindly ship the entire development corpus.

## Interactive QLaaS

### Scripted authoring with real tests

Implement a deterministic adapter for [`harness.Agent`](../../internal/harness/copilot.go).
Use [`harness.Sessions`](../../internal/harness/session.go) for session state, audit events,
confinement, fail-first guard, cancellation and lock ownership. Each scenario specifies typed
operations: scaffold from request, run failing tests, inspect/print AST, apply a reviewed revision,
rerun tests and finish.

Use gate-approved target/write/tools, not store edits behind the session. Tool events and results
are genuine local evidence. Label the session **scripted demo authoring**, never a model-generated
answer; do not invent token/credit spend. Edits produce real content-hash versions.

In the scripted profile, unsupported metadata/example combinations refuse before starting,
explaining that scripted practice is bounded; that adapter is not a general coding agent.
The Codespaces profile instead accepts user-authored requests within the demo workspace and
uses actual Copilot reasoning, while retaining the same fixture, tool and filesystem boundaries.
Both FP and FN exercises demonstrate
fail-first regression and preserve other examples. Stopped/failed work remains stopped/failed;
Repeat does not silently make it ready. Preserve runner limits and serialized CodeQL execution.
No synthetic progress timers or manufactured successful outcomes.

### Runs and comparison provenance

Use ordinary run planning and [`expand.Executor`](../../internal/expand/executor.go) with
local-only sources/databases. Preserve scopes, versions, CodeQL identity, failures, held results,
cancellation and finding attribution. Admit only scenario queries and bounded fixture scopes,
not arbitrary remote repositories or filesystem paths.

Comparison reads a **recorded or simulated demo baseline**, never production GitHub. Record provenance, fixture
repository/commit identity and CodeQL/rule-version compatibility, displaying the label beside
results and stage explanations.

Extend comparison evidence minimally to distinguish `live`, `demo-recorded` and
`demo-simulated` provenance.
Existing comparisons retain their established live meaning. Production `review` still needs
live/current compatible evidence. Demo evidence demonstrates the gate only within a
durably marked Demo store; enforce that in the store, not merely the UI. Incompatible
scope/commit/toolchain refuses the baseline rather than comparing unrelated data.

Local `deploy` rewrites only the demo suite with existing validated-ID/YAML safeguards.
It never publishes, enables code scanning, creates alerts or implies offline discovery has run.

## Tutorial integration and permanent Demo UI

- Show a persistent accessible **Demo — synthetic training data** banner on every page.
  Distinguish scripted from actual Copilot authoring, real CodeQL evaluation from fictional
  repositories, and recorded/simulated comparison inputs from live GitHub alerts.
- Demo status is fixed, not an enabled checkbox or exit action. Explain stopping/replacing the
  database as the way to cease being Demo.
- Keep read-only Walkthrough available. Add **Practice in Demo** as an explicitly selected
  interaction variant, not a dataset switch.
- Practice permits only semantic actions admitted by server capabilities. Spotlight one step
  at a time and wait for actual operation/evidence before advancing. Next cannot fake a
  required practice outcome.
- Exit tutorial or Escape removes coaching; Demo and its backend restrictions remain active.
  Home and Settings offer the tutorial list; there is no header Tutorial checkbox.
  Normal local controls remain usable for unguided practice.
- Separate Walkthrough completion from Practice completion, normal progress from demo progress,
  and different demo dataset IDs/scenario versions.
- Repeat explicitly creates a fresh manifest-bounded exercise request/query/workspace or requires
  operator reset. Never erase old evidence by ad hoc deletes; explain when reset is required.
- Restart of the local profile preserves work and applies existing interruption semantics.
  The Codespaces lifecycle is deliberately different: stop/start or rebuild begins a fresh demo,
  while a process restart within that still-running Codespace may recover its current exercise.
  Reset requires no running server/worker, rebuilds only the canonical marked workspace as a coordinated unit,
  and issues a new dataset ID so old browser progress does not survive. Reset remains Demo.

## Pending

1. **Corpus qualification, test-first.** Extract the reusable builder,
   extend shared sources/authoring recorder, add the profile/manifest, regenerate on a quiet
   machine and qualify exact scenarios and release-bundle provenance. Coordinate the narrow
   obsolete-flow correction with [query requests](../ui/codeql-requests.md), leaving unrelated
   refinements alone. Uses the [Tutorial foundation](../../docs/tutorials.md); no pending dependency.
2. **Startup and policy, test-first.** Add marker/manifest, init/reset commands, startup validation,
   immutable capabilities and local-only composition before external wiring. Test the route
   allowlist, deny-by-default behavior, credential boundary and path confinement. Depends on 1.
3. **Execution and provenance, test-first.** Add scripted Agent, confined local database provider
   and recorded comparison reader. Preserve earned stages and fail-first; carry provenance
   through store -> API -> C# model -> UI. Depends on 2.
4. **Practice UI, test-first.** Add permanent banner, scenario selection and Practice adapters
   for all six lessons, separate outcomes and explicit Repeat/restart/reset behavior.
   Depends on 3 and Tutorial.
5. **Package and qualify.** Bundle qualified sources/recordings; document prerequisites,
   initialization/start, persistence/reset and replacing the demo database with a normal setup.
   Drive full exercises and isolation using release-like assets. Depends on 4.
6. **Prebuilt, ephemeral Demo Codespaces with real Copilot.** Add the dedicated dev container,
   GitHub-managed prebuild configuration, loopback simulated GitHub provider, actual SDK-backed
   QLaaS profile, private forwarding and disposable per-user lifecycle described below.
   Qualify creation from the prebuild with two independent users, genuine new requests and
   CodeQL findings, and zero reuse/publication of either user's modified data. Depends on 1-5;
   repository/organization prebuild, billing and policy administration are explicit prerequisites.

## The constraints that must not be broken

Demo is a marked isolated instance, not relaxed production behavior. No ambient repository
credential, production GitHub data path or production workspace is reachable. The Codespaces
exception permits only qualified Copilot API calls, not general authentication, identity,
repository or Codespaces-control APIs from ABOM.
Local CodeQL evidence remains real; authoring mode and comparison sources are labelled.
Neither Demo state nor Tutorial completion satisfies production gates. Advisories remain attribution, not verdicts;
direct/transitive and owned/upstream distinctions stay unchanged.

## Verification

- Test-first CLI/store/server coverage: marker/manifest mismatch, ordinary startup over demo data,
  unmarked destinations, conflicting flags, symlink/path escapes, malformed/partial initialization,
  stopped-only reset and restart interruption.
- Enumerate all routes, asserting local allowlist and denials for direct API attempts, background
  loops and external readers accidentally attached. Test new/unknown controls deny by default.
- With ambient GitHub/App/Copilot credentials present, assert no production data provider is
  invoked. In the scripted profile no credential reaches a child and there are zero external
  agent calls. In the Codespaces profile only the explicitly selected Copilot credential reaches
  its dedicated adapter; data requests and redirects remain on the simulated provider.
- Manifest validation covers exact required paths/evidence/counts. Ordinary empty/populated
  corpus suites still pass without weakened structural assertions.
- New-rule, FP and FN exercises run with real CodeQL: expected failing tests precede scripted
  fixes, then pass. Inspect genuine AST, version hashes, results and session audit events.
- Review refuses absent/incompatible evidence; production rejects recorded and simulated demo comparisons.
  Only a marked demo with a matching baseline can demonstrate that advance.
- Normal-mode affected endpoints/metrics on a real-corpus `.backup` remain unchanged.
  Demo before/after practice shows only expected mutations. Normal database, query pack and
  working directories stay untouched.
- Playwright drives all six lessons, both blast-radius branches, both FP/FN paths, premature
  Next refusal, stop/cancel/Repeat, Tutorial off with Demo still active, forbidden controls,
  restart and reset into a new progress namespace.
- Screenshot pass covers both themes and normal/narrow widths, with visible provenance and
  no overflow in home or catalog controls.
- Run `make verify`, plus `make itest`/`make selfcheck` for affected runner/CLI protocols and
  targeted genuine Demo lifecycle tests. Check `make status` and quiet discovery before
  regeneration or any CodeQL-backed validation.
- Launch the scripted release profile without credentials and with runtime network access denied.
  Promised exercises work; absent tools/packs produce explicit prerequisite guidance. Separately
  qualify the Codespaces profile's real Copilot path and its no-entitlement/refused-network states.

## Coordination

[Deployment](deployment.md) owns portable assets/locations. Integrate the demo bundle without
requiring images/Helm or reopening deferred pack publication.
It also owns shared toolchain/image packaging consumed by the Codespaces stage; this plan owns
the demo-only lifecycle, simulated data boundary and Codespaces qualification.
[Query requests](../ui/codeql-requests.md) owns the existing corpus acceptance correction.
Complete that narrow change once, not through duplicate patches. Catalog-width/tag work is
not a prerequisite. Retire pending plans only after genuine verification, not a green replay.

## Stage 6: prebuilt, ephemeral Demo Codespaces

### Outcome and sharing guarantee

A user of `advanced-security/codeql-abom` can create a Codespace from a qualified demo prebuild,
open ABOM, submit a new QLaaS request and have actual Copilot SDK authoring create and test a
custom CodeQL query. A real run produces real query results over fictional repositories supplied
by the local simulated provider. Tutorials guide the real UI controls; their read-only mode
continues to work independently of Demo.

**Demo never helps a user share modified data.** Every creation starts from
the immutable approved demo baseline. One person's requests, query edits, findings, transcripts
and credentials never enter another person's Codespace, a prebuild, shared cache, repository,
release or CI artifact. Demo offers no commit/push/PR assistance, share links, publish actions,
pack or workspace export, bulk result downloads, Codespace snapshot/export assistance or
exercise-content telemetry. Enforce this at the API as well as in the UI, including export
endpoints that use GET. Reading code in the editor and reading findings in ABOM remain available.
Copilot receives demo prompts/code only as part of the explicitly started authoring operation;
that approved inference path is not permission to export them elsewhere.

This is not a guarantee against a Codespace owner deliberately copying files, changing the
container, publishing a port or sharing their screen. The operator explicitly accepts that
boundary. Removing remotes, hiding controls and ignoring files are useful defaults, not a
security boundary against an owner with a terminal and sudo.

### Real analysis over fictional source

The pinned Actions extractor forwards to the JavaScript extractor
(`actions/codeql-extractor.yml` declares `forwarded_extractor_name: javascript`), but this does
not turn arbitrary text into an Actions database. Use `--language=actions`, its libraries and
the existing extraction options. The demo must contain valid `.github/workflows/*.yml`,
`action.yml`/`action.yaml` and any called workflow/composite sources needed by each scenario.

No real GitHub repository or workflow execution is required. Fictional repositories have
internally consistent local commits, trees, refs and files. The provider serves those files;
the normal materialization/extraction path produces a database; CodeQL evaluates the actual
query. Preserve source locations, content/version identities, toolchain identity and real
fail-first test results. Do not manufacture BQRS, SARIF, passing tests or findings.

Separate three claims on screen: **real Copilot authoring**, **real CodeQL results**, and
**simulated GitHub repositories/alerts**. File links and finding locations open the corresponding
demo source through ABOM, not a guessed `github.com` URL naming a repository that may not exist.
The provider's loopback address must not leak into a browser link that only works inside the
Codespace; use a read-only source route behind the same private ABOM UI origin.

### Dedicated configuration and prebuild contents

Use a dedicated `.devcontainer/demo/devcontainer.json`, with narrowly scoped lifecycle scripts
under `.devcontainer/demo/`. Do not replace the normal contributor environment or silently
turn ordinary repository Codespaces into Demo instances.

The prebuild contains:

- Pinned Linux toolchain, ABOM executable/published UI, Graphviz, CodeQL and resolved query packs.
- Pinned Copilot CLI and installed QL MCP executable/dependencies; runtime does not download
  a different MCP server through the current `npx -y` default.
- Qualified immutable demo source trees, profile/manifest and compatible baseline databases or
  generated recordings, with hashes that the runtime verifies before use.
- An immutable build identity tying the app, toolchain and demo profile to the qualified source
  commit. No mutable exercise workspace, user request, agent session or user token.

Use Dockerfile/build steps and `onCreateCommand`/`updateContentCommand` for deterministic,
user-credential-free preparation. Any platform build token is provisioning-only and must not
survive in an image, cache or log. GitHub runs these during prebuild creation; it does **not** run
`postCreateCommand` then. Never start interactive Copilot authoring, authenticate a user or
initialize a shared mutable demo during a prebuild.

Prebuilds are GitHub-managed Actions workflows configured for repository, branch, dev container
and region. A checked-in dev container alone does not enable them. The repository administrator
must enable Actions, configure the approved branch/profile and selected regions, choose update
and retained-version policies, and qualify the actual "Prebuild ready" creation path.
Use only trusted baseline commits for that configuration, not users' exercise branches or workspaces.
Coordinate licensing and redistribution with [deployment](deployment.md)'s pre-release policy;
do not silently change the repository license.

### Per-Codespace lifecycle

| Phase | Required behavior |
| --- | --- |
| Prebuild | Produce immutable tools and clean demo inputs only; exercise content and user authentication are absent |
| Creation | Validate this is the demo profile, resolve the installed assets and explain the disposable workspace/authentication boundary |
| Start | `postStartCommand` creates a fresh private root under `/tmp`, assigns a new demo dataset ID, materializes the clean baseline and starts the simulator plus ABOM in Demo mode |
| Attach/re-attach | Open the existing private UI; do not reset an active exercise merely because another editor/tab attaches |
| ABOM process restart | Reuse this still-running Codespace's current private root and mark interrupted work through existing recovery behavior |
| Container rebuild | Reinitialize the private demo root from the immutable baseline; `/tmp` is not assumed clean on rebuild |
| Codespace stop/timeout | Mutable demo state is discarded; configured zero retention deletes the Codespace, rather than keeping an inactive user's workspace |
| New Codespace | Create a new dataset ID and pristine exercise state, never an image/snapshot of a previous user's work |

GitHub documents that `/workspaces` persists across stop/start **and** rebuild, and that most
other directories also survive stop/start. Do not call a home directory ephemeral. `/tmp` is
cleared on stop but survives a rebuild, so combine its lifecycle with an explicit fresh-start/
rebuild initializer rather than relying only on a shutdown hook.

All mutable ABOM data belongs in that private root: SQLite plus WAL/SHM, query pack and tests,
materialized repositories, databases, generated source/SARIF, screenshots, logs, session homes,
MCP scratch, npm/CodeQL caches containing user content, and temporary authentication material.
Use the existing authoring-home confinement and explicit child environments. Installed public
tools/packs may be reused read-only; mutable custom-query caches may not.

Keep the cloned repository and reusable baseline free of exercise output. A workspace shortcut
may point the editor at the private query pack, but must not copy it into `/workspaces`, dotfiles,
user settings sync or a persistent volume. Startup must not consume `.abom` data found in the
repository checkout. A lock makes initialization single-owner; a second attach must not reset
or contend with the active SQLite writer.

Non-secret Demo flags must reach the background services through container-wide environment or
explicit launcher configuration. `remoteEnv` alone only configures the editor's remote process
and its children; it is not a container-wide startup contract. Never place secrets in a committed
Dockerfile, `devcontainer.json` or shell initialization file. The exercise pack has no writable
Git remote, publishing task or export extension supplied by the Demo profile.

### Loopback-only simulated GitHub provider

Run one read-only simulator inside each Codespace, bound explicitly to `127.0.0.1` (and `::1`
only if deliberately supported), never `0.0.0.0`. It reads only the immutable demo manifest and
fixture content. User queries/tests are written to the separate exercise pack, not to the
provider's repository baseline.

Define and contract-test only the GitHub operations ABOM actually consumes: repository metadata,
ownership/account listings, refs/commits/trees/blobs/contents, local source archives, advisory
records and simulated code-scanning analyses/alerts/databases where needed. Missing routes,
objects and unsupported operations fail explicitly. Do not create an HTTP mock that returns
200/empty for anything it does not understand.

GET/HEAD are the default. If an existing reader requires GraphQL POST, admit read-only queries
through a parsed allowlist and reject mutations; HTTP POST alone does not imply a write, and
an unparsed GraphQL body is not safely read-only. Prefer the existing local checkout seam or
provider-served archives over implementing a fake writable Git server. Any read-only Git
transport required by the real materializer must be separately qualified; receive-pack/push
is never provided.

Use a distinct simulated-data client/transport with an exact loopback base URL, bounded paths
and a manifest allowlist of fictional repositories. Reject external redirects and remote
download URLs. Nil adapters, unknown hosts or malformed configuration must fail closed rather
than falling back to `gh`, an App installation, public GitHub or a user's ambient token.
Repository materialization must consume provider-served content; it may not fetch a real repo
whose name happens to match a fictional one.

The simulator port is not listed in forwarded ports, is excluded from automatic forwarding,
and is never proxied as an unrestricted GitHub API through the UI. Binding loopback alone does
not prevent an owner deliberately forwarding a port; the supported default and platform
visibility controls are part of the boundary.

### Actual Copilot SDK QLaaS and the only real API exception

Reuse [`harness.Copilot`](../../internal/harness/copilot.go),
[`harness.Sessions`](../../internal/harness/session.go), the gate, real CodeQL runner, AST tools
and stage checks. The user can write a new request or improve a custom query, not merely choose
from canned scripted responses. Scripts may prepare scenarios; they do not impersonate Copilot.

- Obtain the user's Copilot credential **after** Codespace creation through a qualified setup
  flow, outside ABOM's demo-data runtime. The repository's automatic Codespaces token is not
  assumed to authorize Copilot. Qualify the credential type, CLI/SDK versions and model availability.
- Pass that credential only to the Copilot adapter. Do not pass ambient `GITHUB_TOKEN`,
  `GH_TOKEN`, App keys or repository-write credentials to ABOM's data client, simulator, CodeQL
  or MCP children. Do not request additional production repositories in dev container permissions.
- **ABOM and its agent/MCP children may contact real GitHub only through the qualified Copilot
  APIs.** Identify the precise inference and Copilot-specific token endpoints required by the
  pinned SDK/CLI; allow paths and methods, not an entire `api.github.com` host. General `/user`,
  organization/membership, repository, GraphQL, Actions, code-scanning and Codespaces APIs are
  denied even when a valid user credential exists.
- Enforce the rule in the actual transport/egress path used by ABOM and its SDK/CLI/MCP children.
  Configuring the data client's base URL, setting proxy environment variables, or removing
  `gh` from PATH is not proof that another child cannot fall back to a real API. Qualification
  must observe and deny the disallowed calls. If the chosen Copilot flow requires non-Copilot
  GitHub calls inside ABOM's runtime, that flow is unsupported until a compliant setup/adapter
  is available; do not silently broaden the allowlist.
- Repository checkout, public tool installation, Codespace creation and policy/retention
  read-back are provisioning/control-plane work performed by the operator's external setup
  tooling. They are not runtime exceptions available to ABOM. The running demo does not call
  the real Codespaces API to manage or validate itself.
- Keep the existing narrow tool set and filesystem gate. No arbitrary shell/network tool,
  provider reconfiguration, secret read, repository publish or live GitHub fallback is enabled
  because the agent is "only a demo." A user request is not permission to leave the demo roots.
- Show the applicable spend/session limits and require an explicit Start session. A prebuild,
  attach or restart never consumes Copilot credits by starting a session automatically.
- Missing credentials, denied entitlement, exhausted budget, unavailable model, disallowed API
  access or canceled work remain explicit states. Scripted answers are not a successful fallback
  for this profile.
- Real local runs and comparisons use current query and CodeQL identities. Simulated alert inputs
  carry `demo-simulated` provenance and can demonstrate review only in a marked demo store.

Recommended Codespaces secrets may be convenient, but GitHub saves them in the user's personal
Codespaces settings and reuses them later. Describe that persistence; deleting an exercise or
Codespace is not credential revocation. No user secret is permitted in prebuild steps, logs,
image layers or baseline artifacts.

Do not assume a repository visitor receives development environment secrets: GitHub documents
that they are not injected without write access to the repository. Qualify a post-creation
Copilot credential setup for that user without asking them to commit, create a fork or gain
repository-write access. Codespaces' automatic repository token may itself gain write access
through platform forking behavior; its existence is never authority for ABOM to use real APIs.

### Private access, retention and administrative prerequisites

Only the ABOM UI port is forwarded, privately to the Codespace owner. Keep the simulator internal.
Add exact Codespaces forwarded-host/origin support to the existing HTTP guard; do not disable
host checks or accept wildcard origins to make the proxy work.

For organization-paid Codespaces, configure repository-scoped policies for private-only ports
and maximum retention **0 days**, then verify the effective policy on newly created Codespaces.
GitHub documents that zero retention deletes a Codespace on stop/inactivity timeout and that
an organization retention policy removes the "Keep codespace" exemption.

Those organization policies do **not** apply to personally paid Codespaces, even when created
from this organization's repository. The qualified personal launch path must explicitly request
zero retention and read back the created Codespace's effective settings through the external
launcher, not through ABOM. The current CLI accepts a duration; the candidate creation recipe
must be qualified by reading back zero retention on the actual created environment:

```text
gh codespace create --repo advanced-security/codeql-abom --branch <qualified-branch> \
  --devcontainer-path .devcontainer/demo/devcontainer.json --retention-period 0h
```

Pin and test the CLI contract; do not combine this recipe with `--web`, which does not accept
the retention flag. A browser launch button must select the demo configuration and explain the
retention prerequisite; it cannot claim to enforce zero retention through `devcontainer.json`.
An ordinary browser-created Codespace with unverified defaults is not a qualified
auto-deleting launch. The private `/tmp` exercise lifecycle remains mandatory regardless.

Document who pays for Codespaces/prebuild storage, the required organization plan and administrator
actions, Copilot entitlement and licensing assumptions. Do not promise an arbitrary repository
visitor a free Codespace, prebuild availability in every region/machine, or organization policy
coverage when they pay personally. Select idle-stop and resource limits during qualification and
publish their actual configured values, not an untested performance promise.

### Qualification gates and deliverables

1. **Provider contract.** Against the actual ABOM readers, prove fixture trees/refs/files and
   simulated alerts agree. Exercise unknown hosts, redirects, absent objects, GraphQL mutation
   attempts and attempted real GitHub fetches. Record zero real GitHub API requests from ABOM
   or its children outside the qualified Copilot path, not merely zero writes. Attempt direct
   share/export/download routes and verify they are refused.
2. **Real authoring.** From a prebuild-created Codespace, submit a new QLaaS request not already
   satisfied by a canned query. Run actual Copilot, observe a genuine failing test before the
   correction, inspect AST and query version, run over demo repositories and view real findings
   linked to the correct demo source. Repeat for an FP/FN improvement. Do not assert a fixed LLM
   transcript; assert the evidence, limits and final behavior.
3. **Two-user isolation.** Create A and B independently from the same qualified prebuild. Write a
   unique marker into A's request, query, findings and agent session. B must contain none of it.
   Create C after A has worked; C must still match the immutable baseline. Baseline hashes,
   repository status, reusable caches, Actions artifacts and logs show no exercise-content upload.
   Demo supplies no share/export action to make that marker portable.
4. **Lifecycle.** Confirm attach preserves an active exercise, process restart marks interruption,
   rebuild and stop/start provide a fresh dataset, and effective zero retention deletes on stop.
   The next creation has no previous progress, SQLite state, source changes, tokens or transcripts.
   Inspect `/workspaces`, home directories and tool caches, not only the ABOM database.
5. **Access boundary.** A second account cannot access A's forwarded UI under the supported policy.
   The simulator listens only on loopback and is not forwarded. Unauthenticated/incorrect-origin
   controls are refused. Record the limits of personal Codespaces policy enforcement explicitly.
6. **Prebuild qualification.** Verify the exact repository/branch/configuration/region path shows
   Prebuild ready and uses the approved artifact. Inspect the prebuild itself for credentials and
   user artifacts. Missing/incompatible prebuilds are reported honestly, not called prebuilt.
   Test both genuine Copilot success and unavailable-entitlement behavior after creation.

Deliver the dedicated configuration/lifecycle scripts, simulator and provider contracts, real
Copilot demo composition, private source-link routing, focused integration/browser tests and a
repository entry point explaining creation and data loss. Record external administrator setup
and effective settings separately from checked-in configuration. Do not enable prebuilds,
change organization policies, create billable Codespaces or run paid authoring merely by editing
this plan; those implementation actions require the operator's authorization.

### Sources and verified platform constraints

- [Prebuilding your codespaces](https://docs.github.com/en/codespaces/prebuilding-your-codespaces)
  and [about prebuilds](https://docs.github.com/en/codespaces/prebuilding-your-codespaces/about-github-codespaces-prebuilds):
  snapshot scope, lifecycle hooks and fresh-VM creation.
- [Configuring prebuilds](https://docs.github.com/en/codespaces/prebuilding-your-codespaces/configuring-prebuilds):
  administrator setup, Actions/billing prerequisites, branches and regions.
- [Creating a codespace for a repository](https://docs.github.com/en/codespaces/developing-in-a-codespace/creating-a-codespace-for-a-repository):
  configuration selection and recommended-secret persistence.
- [Persisting environment variables and temporary files](https://docs.github.com/en/codespaces/developing-in-a-codespace/persisting-environment-variables-and-temporary-files):
  `/workspaces`, home, `/tmp`, environment and rebuild behavior.
- [Automatic deletion](https://docs.github.com/en/codespaces/setting-your-user-preferences/configuring-automatic-deletion-of-your-codespaces)
  and [organization retention policy](https://docs.github.com/en/codespaces/managing-codespaces-for-your-organization/restricting-the-retention-period-for-codespaces):
  zero retention, exemptions and who the policies cover.
- [Forwarded ports](https://docs.github.com/en/codespaces/developing-in-a-codespace/forwarding-ports-in-your-codespace)
  and [visibility restrictions](https://docs.github.com/en/codespaces/managing-codespaces-for-your-organization/restricting-the-visibility-of-forwarded-ports):
  private UI access and organization-paid policy scope.
- [Codespaces security](https://docs.github.com/en/codespaces/reference/security-in-github-codespaces):
  repository-token and port-sharing boundaries; a user-controlled Codespace is not a no-copy sandbox.
