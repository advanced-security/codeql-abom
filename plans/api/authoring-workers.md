# Authoring sessions run as their own worker pool

**Status:** Built, not yet run on a cluster. A worker runs a session or a draft in the single
binary's own code and reports what it does as it does it; the coordinator keeps each report as it
verifies, stops a session when asked, lands what it changed, and keeps what was reported across its
own restart. The chart runs the pool and the egress proxy. No image carries the agent yet, so a
cluster cannot run a session.

## Why this exists

Query authoring is how a request becomes a query, and a hosted instance without it hands half of
QL-as-a-Service back to people. Running sessions in the coordinator is also the wrong shape there:
a session runs an agent with tools for minutes, beside the process that holds the store and signs
every decision, and one runaway session would compete with both. Analysis already moved out of the
coordinator into pools of Jobs; authoring is the last work still in it.

## Decided

- **Two places a session runs.** In the single binary, inside `serve`, as today. In a cluster, as a
  Job in a fourth pool, `authoring`, with its own ServiceAccount, SPIFFE identity, object-store
  prefix, resources and NetworkPolicy.
- **One engine.** A worker runs the single binary's own session code. The harness reads and writes
  through `Records`: the store in the single binary, and in a worker a snapshot of what the session
  reads with a journal of what it writes, which the coordinator replays into the store.
- **Printouts and drafts are named by the digest of their content**, in both places. No store row id
  reaches an event, so nothing is renumbered.
- **Three containers per session pod.** The agent container runs the Copilot CLI as a server
  (`--headless --port`, the token through `--auth-token-env`) with ql-mcp behind `serialize-mcp`,
  and holds the Copilot token and nothing else. The worker container runs the harness and reaches
  the CLI over the SDK's `URIConnection`. The attestor holds the SPIFFE key, the attempt's key and
  the object-store credential, and reads nothing the agent wrote.
- **The CLI serves the worker as it serves a child.** Started as the SDK starts a TCP server, it
  listens on 127.0.0.1 only and refuses a connection without the token it was started with. Over
  `URIConnection` the hooks, the permission requests, ABOM's tools, which run in the worker
  container, and the MCP servers, which the CLI starts in the agent container, work as over stdio
  (CLI 1.0.88, SDK 1.0.14).
- **What the worker configures runs in the agent container.** The MCP command line, its environment
  and every path in them are the agent container's, so both containers mount the pack and the
  scratch directory at the same paths.
- **ql-mcp holds no GitHub credential**, in both places. The CLI hands its own environment, the
  session's token among it, to every MCP server it starts, and a name given in the server's
  configuration wins, so ABOM gives each credential name blank.
- **Egress through an allowlist proxy.** Authoring pods reach DNS and an in-cluster proxy that admits
  Copilot, the GitHub API and the object store by host name, and nothing else. The CLI honours
  `HTTPS_PROXY`. A session on a person's Copilot Enterprise token reached only
  `api.enterprise.githubcopilot.com` and `telemetry.enterprise.githubcopilot.com`; the hosts an App's
  token reaches are measured in the drill.
- **Drafts too.** A draft ships `codeql-database.yml` and `src.zip` of each picked repository's
  database, which are all the draft tools read, bound by digest in the grant and unpacked where only
  the worker container reads: a draft is sealed, so its agent is handed source through ABOM's tools
  alone. A draft waits its turn with the sessions on requests, its picks kept with it; one whose run
  no longer keeps a picked database when its turn comes ends stopped. A draft lands no file.
- **One session at a time in a cluster**, unless `--authoring-concurrency` says otherwise; past it a
  session waits in a `queued` state, and its token is minted when it starts. A queued session is
  checked again when it starts: its start decision verified, its request read.
- **500 AI credits per session in a cluster** unless `--authoring-credits` says otherwise; 0 is no
  limit. The single binary keeps no limit by default. The CLI refuses a session limit under 30
  credits, so ABOM refuses one when it starts rather than failing every session.
- **A coordinator restart ends running sessions** and keeps what they reported; queued ones stay
  queued. A stopped session lands what it changed; a killed one lands nothing.
- **The coordinator decides, the worker runs.** The grant names the session, the request, the pack
  and inputs by digest and the not-after time. The Copilot token is minted from the App's
  `organization_copilot_requests` and handed over in the Job's Secret.
- **A session reports as a chain.** Each report is signed by the pool's identity and names the
  attempt, the grant's digest, its place and the digest of the report before it. The coordinator
  polls for the next one, keeps each as it verifies, and stops the worker at the first that does not.
  The result's manifest names how many reports the session made and the last one's digest, and
  nothing lands unless both match what was kept. A report write that failed after it succeeded
  breaks the chain, and the session then lands nothing: that is accepted over a retry that could
  be mistaken for a forgery.
- **Stopping suspends the Job.** The pod's SIGTERM ends the session as stopped, and the worker has
  the grace period to make its last report, upload what changed and sign its result.
- **What a session left is read once it ends, from the pack the agent still shares**: confined to
  the pack, from regular files only, never a name that is itself a link, and only as exactly the
  digest taken when the session ended.
- **A restarted coordinator reads what was reported since it last kept**, verified as a report
  followed while the worker ran is, before marking the session interrupted.
- **What a worker reports is stamped by the worker**: every time an entry carries is the time the
  session kept it, since replay can come long after. Nothing in a report is believed beyond what
  verification binds: entries are forced to the session, spend never falls, and a draft keeps no
  target, tree or test.
- **What a coordinator reads of a worker is bounded before it is read**: every object by what it
  is, a result by its outputs' count and total, a session's reports by a budget, and the results
  all pools hold at once by one bound inside the coordinator's memory.
- **A session's token is revoked once its worker is done**, however the session ended.
- **A session's tests run on a copy of the pack only the worker writes**, made of the pack's regular
  files less what a run leaves. A link or a pipe in the pack refuses the run. What the run leaves goes
  back beside each test through no link, dated as the run dated it, and what it says, and every tree
  printed from a database it built, names the pack.
- **The pack is prepared before it is shared.** The worker unpacks it and installs what it depends
  on where the agent cannot write, then unpacks what that made where the agent works.
- **An attempt signs as itself, and its secrets live apart from its worker**, as every pool's do:
  see [Kubernetes](../tools/kubernetes.md#the-constraints-that-must-not-be-broken). The agent mounts
  nothing the attestor does, and the worker container holds the connection token and nothing else.
- **Waiting sessions are not bounded.** One waits per request, and a request opened by the hundred
  would queue a hundred; that is not how requests are opened yet.
- **A draft's pod is sealed.** Its agent is offered ABOM's tools and nothing else, so it mounts the
  pack read-only and no scratch: it can leave nothing where its worker reads.

## Pending

1. **The authoring image's lock.** `infra/docker/authoring/package-lock.json`, made in that directory
   with `npm install --package-lock-only --ignore-scripts --omit-lockfile-registry-resolved` once npm
   serves ql-mcp at the CLI's version: the workstation's `min-release-age` holds a release for a week.
   Without `resolved` the lock names no registry, so it installs from whichever the build reaches.
   `make check-versions` checks what it resolved; until it exists `make kind-up` builds no
   authoring image, and the default build is unaffected.
2. **A drill on kind**, light on the laptop: the image run with `docker run --read-only` and a
   tmpfs `/tmp`, to see that the CLI and ql-mcp write nothing else; then one real session on
   `has-ghas` and one draft, a second session waiting its turn, a stop, a deleted pod and a
   coordinator restart midway, and from the agent container the proxy refusing an unlisted host,
   NetworkPolicy refusing a direct connection, and no SVID or object-store variable. `audit verify`
   passes after. It measures which hosts an App's token reaches, and whether a sealed draft's CLI
   runs in a working directory it cannot write. Depends on 1.
3. **What an authoring pod reaches besides the proxy.** A NetworkPolicy admits the pod, not one of
   its containers, so the agent reaches DNS and whatever `workerEgress` adds for the worker, such as
   an object store in the cluster. The cluster's resolver answers any name, a channel out the proxy
   never sees. Alternatives: the proxy by its address and no DNS, or a resolver that answers only
   the proxy's name; and the worker's object store reached through the proxy too, so the pod needs
   no rule of its own.

## The constraints that must not be broken

**A session never reaches the store.** It writes a workspace and reports; the coordinator decides
what of that is kept. No authoring pod holds a database credential.

**The Copilot credential reaches only the agent container of the session it was minted for.** Never
the worker container, a report, the object store or a log.

**A decision is reported before the call it allows runs.** A worker reports a gating decision and
waits for the write before answering, as the store records it before answering in the single
binary; a decision that cannot be reported refuses the call.

**A session's workspace is a snapshot.** Nothing lands until its result verifies and every report it
made is kept, and nothing lands over a file that changed since the session read it.

**A report is kept only if it verifies and follows the one before.**

**Nothing the worker does with what the agent can write follows a link or waits on a pipe.** The
agent shares the pack and the scratch with the container that holds the worker's identity. The CLI
run over the shared pack copied a file a fixture linked to into the database the agent reads, and
quoted a linked `qlpack.yml` whole in its error. So the CLI never runs over the shared pack, a file
there is read only as a regular file and, where its content is kept or shown, never through a link,
and no pack is ever on `--search-path`, where a directory can supply an extractor the CLI runs.

## Verification

- A request worked by a worker settles exactly as the same request worked by the single binary.
- A session killed midway leaves its reported events in the audit log and the pack unchanged.
- The agent container holds no SPIFFE key and no object-store credential, and reaches nothing but
  the proxy; no other pool holds a Copilot token.
