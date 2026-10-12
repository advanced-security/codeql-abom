# Practice safely in a startup-only Demo instance

**Status:** fully planned; depends on delivered Tutorial mode.

## Why this exists

Read-only tutorials teach the interface but cannot let a newcomer practice the QLaaS loop or
see deliberately chosen direct/transitive dependencies and evidence. ABOM already has a
reproducible corpus; it needs qualified training scenarios and enforced isolation, not a second
disconnected mock interface.

## Dependencies and boundaries

- Depends on [Tutorial mode](../ui/tutorial-mode.md)'s definitions, checklist, spotlight,
  action policy and verified behavior. Rank immediately after it, ahead of existing next work.
- Reuse the corpus generator, generated fixtures, store APIs, authoring sessions and local
  CodeQL execution. Do not add a parallel fake data/query engine.
- Demo is an instance startup mode, not a checkbox, URL parameter, per-request dataset selector
  or live switch. It owns a dedicated database, work directory, query pack and address.
- Leaving Demo requires stopping the instance and replacing its database/workspace with a
  normal setup. The UI explains that; it provides no Exit Demo toggle.
- First implementation supports local SQLite and one operator per instance. No PostgreSQL
  demo provisioning, per-user workspaces, cloud authoring, crawling or pack publication.
- Runtime is offline. Provisioning may install pinned CodeQL packs/toolchain as an explicit
  operator action. Missing runtime dependencies cause an actionable refusal, never a simulated
  passing test or implicit download.

## Startup contract and durable identity

Proposed explicit CLI contract:

1. `codeql-abom demo init --dir <demo-root>` creates a dedicated workspace from bundled demo
   sources/recordings. It refuses nonempty/unmarked destinations and never overwrites a normal
   database. `demo reset --dir <demo-root>` acts only on a stopped, marked demo.
2. `codeql-abom serve --demo --demo-dir <demo-root> --addr <demo-address> --ui-dir <assets>`
   serves that instance. The root determines database, local query pack and work directories;
   conflicting flags and arbitrary production path overrides are refused.

Store a typed dataset marker through `internal/store`, the only SQL owner. A small workspace
manifest records dataset ID, corpus/scenario format version, toolchain contract and permitted
local paths. Check marker/manifest agreement before normal service composition.
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
local executor and recorded-alert reader. Never construct a live reader and later disable it.

### Backend capability policy

Permit a small explicit set of local mutations:

- File/edit a request from pasted or bundled local examples.
- Start/stop a scripted scenario authoring session.
- Run the selected custom query's real tests and PrintAST.
- Start/cancel a bounded regular run over local fixture databases.
- Compare that run with a compatible, labelled recorded code-scanning baseline.
- Advance stages under existing readiness gates, including local-only `deploy`.
- Change a demo-local tier suite binding for the deployment lesson; discovery remains unavailable
  and the screen says the binding schedules nothing in this offline instance.

Permit saved scope changes only where an exercise requires them, with existing scope semantics.
All other control endpoints deny with a 4xx explanation and standard logging, even if the UI
hides them. Audit the entire route table, including GET handlers with external side effects.
Unknown/new control routes deny by default.

Explicitly deny discovery/enumeration, gh identity refresh, arbitrary linked-example fetch,
live alerts/databases, dependency submissions, code-scanning setup writes, webhook ingestion,
relays, remote lock verification, tool installation/update, arbitrary agent configuration and
destructive prune/compact. Read endpoints use only demo-local backends; replace nil helpers that
default to gh with explicit offline refusals/readers.

No production credential is looked up or inherited by local children. Supply an explicit
CodeQL/local-data environment. Demo authoring never launches Copilot, ql-mcp, npx, gh, git
fetch/clone or arbitrary commands. Scripts are typed Go operations, not shell strings.

Canonicalize and confine query imports, writes, fixture/database paths and outputs under the
marked workspace plus approved read-only CodeQL pack roots. Reuse runner locking, cancellation
and diagnostics. Missing/corrupt databases are rebuilt from local fixtures or refused, never
fetched through the normal crawler.

Loopback is the first supported network boundary. Public multi-user sandboxing needs separate
authentication and resource isolation; this mode does not promise it.

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
| Orientation | Owned declaration with provenance, seed/upstream data, populated/incomplete coverage and drawable named scopes |
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

Unsupported metadata/example combinations refuse before starting, explaining that scripted
practice is bounded; this is not a general coding agent. Both FP and FN exercises demonstrate
fail-first regression and preserve other examples. Stopped/failed work remains stopped/failed;
Repeat does not silently make it ready. Preserve runner limits and serialized CodeQL execution.
No synthetic progress timers or manufactured successful outcomes.

### Runs and comparison provenance

Use ordinary run planning and [`expand.Executor`](../../internal/expand/executor.go) with
local-only sources/databases. Preserve scopes, versions, CodeQL identity, failures, held results,
cancellation and finding attribution. Admit only scenario queries and bounded fixture scopes,
not arbitrary remote repositories or filesystem paths.

Comparison reads a **recorded demo baseline**, never GitHub. Record provenance, fixture
repository/commit identity and CodeQL/rule-version compatibility, displaying the label beside
results and stage explanations.

Extend comparison evidence minimally to distinguish `live` from `demo-recorded` provenance.
Existing comparisons retain their established live meaning. Production `review` still needs
live/current compatible evidence. Recorded evidence demonstrates the gate only within a
durably marked Demo store; enforce that in the store, not merely the UI. Incompatible
scope/commit/toolchain refuses the baseline rather than comparing unrelated data.

Local `deploy` rewrites only the demo suite with existing validated-ID/YAML safeguards.
It never publishes, enables code scanning, creates alerts or implies offline discovery has run.

## Tutorial integration and permanent Demo UI

- Show a persistent accessible **Demo — synthetic training data** banner on every page.
  Explain local-only changes, scripted authoring and recorded comparisons where they occur.
- Demo status is fixed, not an enabled checkbox or exit action. Explain stopping/replacing the
  database as the way to cease being Demo.
- Keep read-only Walkthrough available. Add **Practice in Demo** as an explicitly selected
  interaction variant, not a dataset switch.
- Practice permits only semantic actions admitted by server capabilities. Spotlight one step
  at a time and wait for actual operation/evidence before advancing. Next cannot fake a
  required practice outcome.
- Unchecking Tutorial removes coaching; Demo and its backend restrictions remain active.
  Normal local controls remain usable for unguided practice.
- Separate Walkthrough completion from Practice completion, normal progress from demo progress,
  and different demo dataset IDs/scenario versions.
- Repeat explicitly creates a fresh manifest-bounded exercise request/query/workspace or requires
  operator reset. Never erase old evidence by ad hoc deletes; explain when reset is required.
- Restart preserves local work and applies existing interruption semantics. Reset requires
  no running server/worker, rebuilds only the canonical marked workspace as a coordinated unit,
  and issues a new dataset ID so old browser progress does not survive. Reset remains Demo.

## Pending

1. **Corpus qualification, test-first.** After Tutorial verification, extract the reusable builder,
   extend shared sources/authoring recorder, add the profile/manifest, regenerate on a quiet
   machine and qualify exact scenarios and release-bundle provenance. Coordinate the narrow
   obsolete-flow correction with [query requests](../ui/codeql-requests.md), leaving unrelated
   refinements alone. Depends on [Tutorial mode](../ui/tutorial-mode.md).
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

## The constraints that must not be broken

Demo is a marked isolated instance, not relaxed production behavior. No ambient credential,
external read/write path or production workspace is reachable. Local CodeQL evidence remains
real; scripted authoring/recorded comparisons are labelled. Neither Demo state nor Tutorial
completion satisfies production gates. Advisories remain attribution, not verdicts;
direct/transitive and owned/upstream distinctions stay unchanged.

## Verification

- Test-first CLI/store/server coverage: marker/manifest mismatch, ordinary startup over demo data,
  unmarked destinations, conflicting flags, symlink/path escapes, malformed/partial initialization,
  stopped-only reset and restart interruption.
- Enumerate all routes, asserting local allowlist and denials for direct API attempts, background
  loops and external readers accidentally attached. Test new/unknown controls deny by default.
- With ambient GitHub/App/Copilot credentials present, assert no provider is invoked and no
  credential reaches a child. Network-denying fakes and runtime instrumentation prove zero
  outbound GitHub/git/agent calls. Displayed fixture links are never automatically fetched.
- Manifest validation covers exact required paths/evidence/counts. Ordinary empty/populated
  corpus suites still pass without weakened structural assertions.
- New-rule, FP and FN exercises run with real CodeQL: expected failing tests precede scripted
  fixes, then pass. Inspect genuine AST, version hashes, results and session audit events.
- Review refuses absent/incompatible evidence; production rejects demo-recorded comparisons.
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
- Launch release-like assets without credentials and with runtime network access denied.
  Promised exercises work; absent tools/packs produce explicit prerequisite guidance.

## Coordination

[Deployment](deployment.md) owns portable assets/locations. Integrate the demo bundle without
requiring images/Helm or reopening deferred pack publication.
[Query requests](../ui/codeql-requests.md) owns the existing corpus acceptance correction.
Complete that narrow change once, not through duplicate patches. Catalog-width/tag work is
not a prerequisite. Retire pending plans only after genuine verification, not a green replay.
