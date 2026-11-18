# A production instance does not need a person's token

**Status:** Reads of an installed account use the App. Copilot, the declaration check, dependency
snapshots and the default-setup language add still use a person's token. Lands in one
implementation with [ownership-claims.md](ownership-claims.md): a pod that can read and still
cannot say who may declare has not stopped needing a person.

## Why this exists

A production pod has no `gh auth` session. The server still will not author, will not say who may
declare, and will not perform the two writes it already performs, without one.

`githubToken` in [discover.go](../../cmd/codeql-abom/discover.go) is `GITHUB_TOKEN`, then
`GH_TOKEN`, then `gh auth token`. It is what [serve.go](../../cmd/codeql-abom/serve.go) hands to
dependency submission and to the `HeadSHA` that submission resolves, and what
[codescanningstatus.go](../../cmd/codeql-abom/codescanningstatus.go) hands to the default-setup
write. [authoring.go](../../cmd/codeql-abom/authoring.go) asks `ghauth.Token` for every Copilot
session and for the readiness probe. [accounts.go](../../cmd/codeql-abom/accounts.go) and the
serve default for `Verify` shell out to `gh api`. With no App, [newReader](../../cmd/codeql-abom/credentials.go)
and the advisory client use the same person's token; with an App, those two already do not.

`tokenFor` in [app.go](../../internal/ghapp/app.go) posts to
`/app/installations/{id}/access_tokens` with no `permissions` body, and caches one token per
installation. GitHub then returns every permission the installation holds. After this plan adds
write grants, that cache would hand a crawl the write token, or a write the Copilot token,
whichever was minted first.

GitHub's server-to-server path for Copilot is an installation token (`ghs_`) with organization
permission `organization_copilot_requests: read`, passed explicitly, with logged-in-user fallback
off. It is billed to the installing organization. It needs that organization's policy allowing
Copilot requests from GitHub App installations. It expires in one hour. It is not a user
subscription, and a classic `ghp_` token is not a supported Copilot credential. The pinned SDK
(`github.com/github/copilot-sdk/go` v1.0.14) already takes `GitHubToken` and
`GitHubTokenProvider`, and defaults `UseLoggedInUser` to false when a token is set.

## Pending

1. **Mint a token for the call, not for the installation.** The cache key is the installation plus
   the permission set. `POST /app/installations/{id}/access_tokens` sends a `permissions` body that
   is a subset of what the installation was granted. A crawl token carries the existing read set
   and nothing else. A snapshot token carries `contents: write` and the reads that snapshot needs,
   not Copilot. A default-setup write carries `administration: write`, not Copilot and not
   `contents: write`. A Copilot token carries `organization_copilot_requests: read` and nothing
   else. A permission the installation has not granted yet refuses that call, with that reason. It
   does not borrow another token, and it does not fall back to `GITHUB_TOKEN`, `GH_TOKEN`, or
   `gh`. Depends on nothing.
2. **Copilot uses the App when one is configured.** The session's `GitHubTokenProvider` mints on
   `initial` and on `refresh`, through the call in 1, for the organization the session is billed
   to. `UseLoggedInUser` is false. The child process does not inherit `GH_TOKEN` or
   `GITHUB_TOKEN`. When no App is configured, the session keeps `ghauth.Token`, which is the local
   and development path. A missing Copilot grant, or GitHub's refusal that App installation tokens
   are not enabled for that organization, refuses the session and is what readiness says. It is
   not "no agent" and not a 403 discovered mid-turn. Depends on 1.
3. **The two existing writes use the App when one is configured.** Dependency submission, including
   the `HeadSHA` it resolves, uses the snapshot token from 1. The default-setup language add uses
   the administration-write token from 1. Both stay explicitly requested, ledgered, and bounded to
   approved targets. This is not a new publisher, not a timer, and not a workflow grant — those
   stay deferred in [dependency-submission.md](dependency-submission.md). When no App is
   configured, both keep the person's token. Depends on 1.
4. **Add the three grants to the manifest, traced to the call that needs each.**
   `organization_copilot_requests: read` to the Copilot session.
   `contents: write` to the existing snapshot submission — the current contents grant is read, and
   the dependency submission API requires write.
   `administration: write` to `PATCH /repos/{owner}/{repo}/code-scanning/default-setup` — the
   current administration grant is read, which covers the GET. An already-installed App that has
   not been re-approved reports the missing grant as in 1, rather than calling and reading the 403
   as a product failure. The manifest trace test still fails a grant no call needs. Depends on 1.
5. **Declaration checks do not shell out to `gh` for the server to run.** When an App is
   configured, identity and the declaration picker use the App, as
   [ownership-claims.md](ownership-claims.md) specifies. When no App is configured, `ghauth.Verify`
   stays. A production process with an App and no `gh` does not record "you belong to nothing".
   Depends on 1. The role rules are the other plan's; this one only stops the missing CLI from
   being the answer.
6. **Readiness names the credential.** `GET /api/authoring/readiness` says whether authoring will
   use an installation token or `gh`, and the org-policy refusal when that is the reason it cannot.
   It does not print a token. Analysis workers still receive neither the Copilot token nor a write
   token; [deployment.md](../tools/deployment.md) keeps that separation, and the token they would
   receive is an installation token, not a PAT. Depends on 2.
7. **Say what the system does, once the grants exist.** The architecture sentence that writes keep
   the person's credential, and [enterprise-server.md](enterprise-server.md)'s status line that the
   App is read-only, move to what this plan made true. Not before the grants land.

## Decided

**One App, three tokens.** The owner re-approves the registration once. A second App is not
required. The separation enterprise-server requires — a credential that reads is not the
credential that writes — is the minted token's permission set, not a second registration.
[github-hosts.md](github-hosts.md) still chooses the host. This plan does not send a token minted
for one host to another, and does not invent a second router.

**No person's token is a production requirement.** Local and development runs with no App keep
`gh auth token` for reads, Copilot, declaration checks and the two writes. Production with an App
never consults it. An empty environment is not an empty identity.

**Copilot is billed to the installing organization.** There is no user subscription on that path,
and the model list that path returns is the organization's. The user's list is the no-App path,
which [qlaas-loop.md](qlaas-loop.md) renders. Same control, different credential.

## The constraint that must not be broken

**A missing user token is not a denial and not an empty membership.** Production with an App and
no `gh` starts and reads. It authors, or it says which grant or org policy is missing. It does not
fall back to an ambient token, and it does not record that the operator belongs to nothing because
the CLI was absent.

**A token is never stored, logged, or written into an event.** The App's key stays a 0600 file
beside the database, as it is now. The Copilot child receives the installation token as the SDK's
explicit credential and not as an inherited environment variable.

**A write stays irreversible and explicit.** Moving the existing snapshot and default-setup writes
onto an installation token does not add a timer, a first submission, or a client that submits.
[dependency-submission.md](dependency-submission.md) still defers those. A write whose grant is
absent is refused before GitHub is asked.

## Verification

- A process with no `gh`, no `GITHUB_TOKEN` and no `GH_TOKEN`, and an App configured, serves and
  reads an installed account. A Copilot session is offered a `ghs_` token minted for that call. A
  refresh mints another. The crawl's token does not carry `contents: write` or
  `organization_copilot_requests`. The snapshot token does not carry Copilot.
- A process with no App still uses `gh auth token` for those same calls. The two paths are tested
  by which credential function runs, not by elapsed time.
- An installation that has not re-approved the new grants refuses the session and the write with
  the missing permission named. It does not attempt the person's token.
- No token appears in a log line, an authoring event, or the database. The readiness body names the
  credential kind and the refusal, not the secret.
