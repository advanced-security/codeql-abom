# A failed authoring session resumes from the step on screen

**Status:** The loop runs to review. A session home is deleted when the run ends, the model is a
flag, and ql-mcp calls outside six tools are refused by name.

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

The operator was stopped while the agent validated QL. `codeql_lsp_diagnostics` is already one of
the six tools in `loopTools`. `packURI` in [confine.go](../../internal/harness/confine.go)
requires a `file://` URI. The ql-mcp server documents a plain directory path for `workspace_uri`,
not a URI. A model that follows the server is refused. The other LSP tools —
`codeql_lsp_completion`, `codeql_lsp_definition`, `codeql_lsp_references` — are refused by name
before any argument is checked. Read one refused event from a real session before changing the
allowlist: the name may already have been allowed, and the argument is what failed.

## Pending

1. **Confirm the refusal, then offer the server's tools.** Read one real session's refused event.
   Offer every ql-mcp tool the pinned server advertises, not a list copied from a document that
   will drift. LSP tools take the arguments the server documents: a plain pack directory for
   `workspace_uri`, 0-based positions, and `file_path` as well as `ql_code`. A tool the server
   does not advertise is still refused by name. Depends on nothing.
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
5. **The model list is the runtime's.** `ListModels` on the credential the session will use —
   the user's, when no App is configured; the organization's, when
   [production-credentials.md](production-credentials.md) mints the Copilot token. Render id,
   name, policy state, supported reasoning efforts and context tier. Do not invent a retiring
   badge the runtime did not send. Pass the selected effort and tier to `SetModel` on a live
   session and to the next create or resume. A model the runtime does not list is unavailable,
   including the flag default. A list that could not be read is an error, not an empty catalog
   and not `claude-opus-5` presented as the catalog. The flag names the session's model only when
   that id is in the list. Depends on nothing for the local path. The production list depends on
   the Copilot installation token existing; the control is the same.

## Decided

**Rewind is the restore.** An icon that only resumed the conversation and left the files as they
are would be a bookmark. The operator chose the rewind. It is recorded, and it is not silent.

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

- A diagnostics call with the plain pack path the server documents is allowed. One with a
  workspace outside the pack is not. `codeql_lsp_definition` is offered. `codeql_test_accept` is
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
