# An address the organization can reach, and an identity it granted

**Status:** not built, blocked on ingress this workstation does not have. Identity and residency
only; the webhook receiver is [`event-driven-reading.md`](event-driven-reading.md).

## Why this exists

ABOM authenticates as a person. `internal/ghauth` reads an authenticated `gh` session or
`GITHUB_TOKEN`, which is right for a tool somebody runs and wrong for a service an organization
depends on, in three ways that all bite at once: the crawl's rate limit is one human's, what ABOM
can see follows whoever last logged in, and when that person's access changes the install degrades
without anything in the data saying so.

An App installed in the organization is the identity that matches what ABOM already claims to be —
scoped to one account, granted deliberately by someone who could grant it, and surviving the
departure of whoever set it up. A workflow client also needs its own authenticated path to the
server: `abom-check` cannot borrow a maintainer's session or receive the server's GitHub write
credential just to request one authorized operation.

The one-organization boundary in [the vision](../../docs/vision.md) and an App installation agree
with each other, which is a useful check on both: `gh-ghas-audit` documents that installation tokens
work per organization and cannot enumerate an enterprise. The boundary is not a limitation being
worked around; it is the shape of the credential.

## Pending

1. **App identity.** App id, private key, and installation tokens minted and refreshed — they expire
   in an hour, so this is a lifecycle rather than a value in a config file.
2. **The installation as evidence.** `abom_account` records what `gh` says about membership; an
   installation is a stronger statement from the same source, and belongs beside it rather than
   replacing the declaration. See [`../db/owned-scope.md`](../db/owned-scope.md). Depends on 1.
3. **Least permission that answers the questions.** Each endpoint ABOM calls names the permission it
   needs, and anything the list does not justify is not requested. Depends on 1.
4. **The address.** Ingress, TLS, and a name that does not change when the process restarts.
5. **Who may read it.** The interface is currently open because it is local. It stops being local
   here, and reads stop being free. Depends on 4.
6. **The private key.** The most dangerous thing this project would ever hold, and the plan for it
   is written before it exists rather than after. Depends on 1.
7. **Authenticate workflow callers and authorize individual operations.** Define short-lived,
   repository-scoped verdict-read grants for [abom-check.md](abom-check.md). Verify repository,
   workflow/event, base/head commits and expiry, and return evidence for those exact lock contexts.
   Separate evidence reads from any justified check-run permission. Refuse expired/revoked grants
   and cross-repository access, including misuse of fork-PR identity. The workflow client receives
   no submission or discovery authority. Depends on 1, 3, 4 and 5, not on new publication grants;
   those are deferred in [dependency-submission.md](dependency-submission.md).

## Authentication decision

Choose between a verified GitHub OIDC exchange and a short-lived repository-scoped App-backed
caller grant before implementing item 7. For either, document the trusted issuer and audience,
repository/installation binding, workflow/event/ref restrictions, expiry, revocation and replay
handling. Do not replace missing issuer support on a host with an unscoped shared secret. The
workflow action receives no App private key or GitHub contents-write credential.

`X-Abom-Control` currently guards cross-site requests and local control; it is not an authenticated
caller identity. Enabling remote control alone cannot authorize a workflow client.
[github-hosts.md](github-hosts.md) owns destination/credential routing and its host support matrix;
this plan owns service identity, caller grants and deployment.

## The constraints that must not be broken

**An installation is evidence, never a declaration.** Being installed somewhere does not make its
supply chain anyone's remediation work, for exactly the reason membership does not — see
[`../db/owned-scope.md`](../db/owned-scope.md). Ownership stays declared. An installation can
contradict a declaration, and can support one, and can do neither.

**A credential that reads is not the credential that writes.** ABOM submits dependency snapshots and
would create check runs, which are irreversible against a live repository. Those live behind a
separate grant from the one that crawls. Client permission to request a submission is narrower than
the server's GitHub permission to perform it and is not planned for `abom-check`. Its verdict-read
grant cannot invoke existing submission or arbitrary control routes.

**Losing the App is unknown, never contradicted.** An expired token, a revoked install and a
network failure look alike from the inside, and only one of them is evidence about ownership. This
is the same distinction [`../db/declaration-provenance.md`](../db/declaration-provenance.md) draws
for `gh`, and it is drawn here for the same reason.

## Verification

- The rate limit consumed by a crawl is the installation's, measured against the same crawl run as a
  person — because the point of this plan is that they are different.
- The install keeps working when the operator who created it loses access to the organization.
- Every permission the App requests is traceable to an endpoint ABOM calls, and removing any one of
  them breaks a named thing.
- A revoked installation puts declared accounts into *unknown* and roots nothing, rather than
  silently reporting an organization as empty.
- Caller-contract tests reject wrong issuer/audience, repository, workflow/event, commit/ref and
   expired or revoked grants. Valid verdict reads cannot submit, control discovery, renew other
   repositories or change ownership; returned lock contexts match the requested commits.
- Test the permitted workflow operation and denied operations against an approved dedicated
   repository. Tokens and private keys appear in neither logs, summaries nor error responses.
