# A failed authoring session resumes from the step on screen

**Status:** The loop runs to review, on a person's token or an App's installation token, with
either model family's file tools. A session home is deleted when the run ends, the model is the
flag's where the runtime offers it and the runtime's choice where not, and ql-mcp's test, query,
metadata and language-server tools are offered; every other ql-mcp tool is refused by name.

## Why this exists

The icons in [AuthoringSteps.razor](../../ui/Shared/AuthoringSteps.razor) are `target`, `help`,
`expected`, `database`, `ast`, `compiles` and `tests`, derived from files on disk. They are not
checkpoints. [session.go](../../internal/harness/session.go) deletes the session directory when
the run ends, and [authoringsession.go](../../internal/store/authoringsession.go) stores no
runtime session id, so the SDK's resume has nothing to load. A live session takes the request's
prompt and whatever `Next` appends when the agent goes idle. It has no way to accept a person's
prompt while it is running, or after it has failed.

The model is `--authoring-model`, default `claude-opus-5` in
[copilot.go](../../internal/harness/copilot.go). That name is already absent from the picker a
Copilot subscription shows. A hardcoded catalog will be wrong again the next time a model is
retired. The pinned SDK (`github.com/github/copilot-sdk/go` v1.0.14) already has `ListModels`,
`ResumeSession`, `SetModel` (reasoning effort and context tier) and `Send` with `immediate` and
`enqueue`. The gap is that ABOM does not call them.

Ten of ql-mcp's tools are offered: test extract and run, query compile and run, resolve metadata, and
the language server's diagnostics, definition, references, completion and document symbols. The
rest are refused by name before any argument is checked, though the pinned server advertises more
than sixty, several of which only read: `codeql_resolve_files`, `codeql_resolve_tests`,
`codeql_bqrs_decode` over a run's own output, `read_database_source` over the test database.

## Pending

1. **Offer the server's other tools that read.** Offer what the pinned server advertises, not a
   list copied from a document that will drift, each with the arguments its schema documents and
   every path judged the way the gate already judges one: a file read is the pack, the locked
   libraries or a run's own output; a workspace is the pack. An argument that names a search path,
   an output, a log or a flag stays refused by name. Read one real session's refused event before
   adding a tool: the name may already be allowed, and the argument is what failed. Depends on
   nothing.
2. **Three tools stay refused, by name, with the reason.** `codeql_test_accept` makes the query's
   own output the expected result, so the test can never fail. `codeql_pack_install` rewrites the
   lock ABOM resolves. `create_codeql_query` writes a layout ABOM did not choose. An argument that
   leaves the session's bounds stays refused: `additionalArgs`, an output or log path outside the
   scratch ABOM owns, a write outside the target, a read outside the pack and the libraries, and
   `learn`. Offering the server's tools does not weaken that. Depends on 1.
3. **Each icon is a snapshot.** When a step transitions, snapshot the writable target files and
   record the test-database identity. The icon is that snapshot. A step with no snapshot says so
   and is not a restore point. `target` is a decision: restoring it re-binds the query and does
   not invent a file that was not written. `database` restores the recorded identity, not a
   database the agent wrote — only the CLI writes that. Depends on nothing. The resume that uses
   the snapshot is 4.
4. **Resume rewinds, then continues.** Persist the runtime session id. Keep the session home until
   the operator discards it or the request is closed. Resume uses `ResumeSession` against that
   home. Resuming from an icon rewinds the target files to that snapshot first. Later edits at
   that step and after it are undone, and the timeline says so before the agent runs. A failed,
   stopped or interrupted session on an open request can be resumed. A request that is done or
   declined cannot. A live session takes an in-UI prompt: `Send` with `immediate` to steer the
   current turn, `enqueue` to run after it. The prompt is recorded before it is sent. If the
   runtime transcript cannot be loaded, say so and start a new session on the restored files.
   Do not pretend the conversation survived. Depends on 3.
5. **Choose the model from the runtime's list, on screen.** A session already runs on that list:
   the configured model where the runtime offers it to the session's credential, the runtime's
   `auto` where it does not, and a list that cannot be read, or offers nothing, refuses the session.
   The model each call actually ran on is recorded from the usage events. What is left is the
   picker: `ListModels` on the credential the session will use, rendering id, name, policy state,
   supported reasoning efforts and context tier, with no retiring badge the runtime did not send;
   the selected effort and tier passed to `SetModel` on a live session and to the next create or
   resume. Measured on `has-ghas` (2026-11-19): an App installation is offered `auto` and
   `gpt-5.3-codex` only, so `claude-opus-5`, the flag default, ran nowhere through the App. Depends on
   nothing.
6. **A query can read a list from a data extension.** The scaffolded `qlpack.yml` declares no
   `dataExtensions`, and a session writes only the target's source, help and fixtures, so a query
   whose subject is a list — actions that fetch a tool at run time, actions that restore a cache and
   the input that turns it on, publishers, well-known actions to compare a near-miss against — holds
   it as rows of a QL predicate. That reads the same and grows by editing the query. Two live
   sessions, asked for a list that can grow, tried to write `qlpack.yml` and then
   `src/<QueryName>.model.yml`, and were refused both. Upstream, the
   Actions pack keeps such lists as models (`ext/*.model.yml` against an `extensible` predicate).
   Scaffold `dataExtensions: - ext/**/*.model.yml` in the pack, and let a session write
   `ext/<QueryName>.model.yml` beside the target and nothing else under `ext/`; a model another
   query's file declares stays out of reach. Existing packs gain the line the way an old
   scaffold's `"*"` dependency is pinned. Depends on nothing.
7. **A fixture is checked as Actions, not only as YAML.** A query test extracts any YAML, so a
   fixture with a key Actions never takes still builds and still passes. Two sessions wrote a
   step-level `container:` and a step that calls a reusable workflow with `secrets: inherit`, then
   query branches for them that match nothing a repository can hold. The rules now say which keys
   belong to a job; that is guidance, and a model can ignore it. With the rule in place, the same
   request then got a top-level `x-` key, Docker Compose's convention for an anchor, and an `env:`
   on a job that calls a reusable workflow, which takes none: guidance names the cases someone
   thought of, and a schema names the rest. Validate each fixture against the
   workflow and action schemas SchemaStore publishes (`github-workflow.json`,
   `github-action.json`), vendored at a pinned commit so ABOM fetches nothing at run time, and
   report a key the schema does not allow as a warning on the expected step, naming the file and
   the key. A warning, not a refusal: the schemas lag GitHub, and a fixture can be meant to be
   malformed. Depends on nothing.

## Decided

**Rewind is the restore.** An icon that only resumed the conversation and left the files as they
are would be a bookmark. The operator chose the rewind. It is recorded, and it is not silent.

**A run names its database; ABOM never supplies one.** A `codeql_query_run` that names none is
refused, not filled in with the test database, even though that is the only one a bound session
may read: which database a query reads is never assumed, and running the target query against its
test database is `abom_test` and `codeql_test_run`'s job. The rules offer `codeql_query_run` for
PrintAST alone, with the database named every time.

**Confinement survives resume.** Restoring files does not restore a permission the gate refused.
Two agents still cannot write one request. A steer is a prompt event, not a side channel.

**This does not wait on the credential split to be useful locally.** A workstation with `gh auth`
can resume, steer and list models before production mints an installation token. The production
list is that token's `ListModels`, not a second catalog.

## The constraint that must not be broken

**The agent writes inside bounds ABOM sets, including after a resume.** A restored tree is the
target's files at that step. It is not a permission to read the session home, the database, or a
path the gate already denied. The three named refusals stay refusals because each one makes a
later test unable to mean what it says.

**A model the runtime did not list is not a model the session runs.** Substituting the flag
default when the list omits it is the failure this plan exists to stop.

**Say what was not restored.** A missing snapshot, a missing transcript, or a list that could not
be read is that fact on screen. It is not a successful resume and not an empty picker.

## Verification

- A read-only tool the server advertises is offered with its documented arguments, and one naming
  a path outside the pack, the libraries or the run's output is refused. `codeql_test_accept` is
  still refused, with the existing reason. An `additionalArgs` write outside the pack is still
  refused.
- A failed session resumed from Compiles has the query bytes from that snapshot, and the timeline
  records the rewind. A prompt sent while the agent is working arrives as `immediate` without a
  second session. A request that is done is refused.
- The picker contains whatever `ListModels` returned for that credential and does not contain a
  model the runtime omitted. A failed list is an error, and the session does not start on
  `claude-opus-5` because the flag still says so.
- A resumed session is still confined: the home stays denied, and a second start on the same
  request is still refused while one is running.
