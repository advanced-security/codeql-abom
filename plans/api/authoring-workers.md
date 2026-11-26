# Authoring sessions run as their own worker pool

**Status:** Not built. Authoring sessions run inside the process that serves the interface, so in a
cluster the coordinator would need the Copilot CLI, Node and ql-mcp, and today it starts with
none: requests can be filed and worked by hand, and no session can start.

## Why this exists

Query authoring is how a request becomes a query, and a hosted instance without it hands half of
QL-as-a-Service back to people. Running sessions in the coordinator is also the wrong shape there:
a session runs an agent with tools for minutes, beside the process that holds the store and signs
every decision, and one runaway session would compete with both. Analysis already moved out of the
coordinator into pools of Jobs; authoring is the last work still in it.

## Decided

- **A fourth pool, `authoring`**, with its own ServiceAccount, SPIFFE identity, object-store prefix,
  resources and NetworkPolicy, beside `discovery`, `qlaas-test` and `qlaas-run`. Its image adds the
  Copilot CLI, Node and ql-mcp to the analysis image; the others do not carry them.
- **The coordinator decides, the worker runs.** Starting a session is a decision as it is today: the
  requester's role, the request, and the Copilot entitlement and budget it reads. The grant names the
  session, the request, the query workspace snapshot by digest and the not-after time.
- **Copilot is billed to the installing organization.** The coordinator mints the App's
  `organization_copilot_requests` token for the session and hands it over in the Job's Secret,
  owned by the Job, as analysis hands a repository token over today. No person's token is used.

## Pending

1. **The session contract.** What a session needs as inputs (the request, the workspace snapshot,
   the test database) and what it returns (the workspace's changed files, the audit events, the
   tree prints), as content-addressed objects under the pool's prefix, with a result attestation
   signed by the attempt. Depends on nothing.
2. **The worker command.** `codeql-abom worker` runs a session from its grant: it verifies the grant,
   materializes the workspace, starts ql-mcp behind the one-call-at-a-time relay, runs the Copilot CLI
   with the confinement the local harness applies, and uploads what changed. Depends on 1.
3. **Events while it runs.** The audit log shows a session's events as they happen, so the worker
   streams them to the object store in batches the coordinator ingests, and a session killed midway
   keeps everything it reported. Depends on 2.
4. **The pool in the chart.** Values, ServiceAccount, RBAC subject, egress to GitHub and Copilot's
   endpoints and nothing else, and the image. Drilled on kind with a real Copilot entitlement. Depends
   on 2.

## The constraints that must not be broken

**A session never reaches the store.** It writes a workspace and events; the coordinator decides
what of that is kept.

**The Copilot credential reaches only the session it was minted for.** Never another pool, never the
coordinator's log, never the object store.

**A session's workspace is a snapshot.** A change made in the interface while a session runs is not
overwritten by its result; the coordinator refuses a result built on a workspace that has moved, as a
component that changed since it was read is refused for export.

## Verification

- A request worked on kind produces the same steps, events and query as the same request worked by
  the single binary.
- A session killed midway leaves its reported events in the audit log and the workspace unchanged.
- The authoring pool's worker cannot read another pool's objects, and no other pool holds a Copilot
  token.
