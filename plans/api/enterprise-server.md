# An address the enterprise can reach, an identity it granted, and readers GitHub authorizes

**Status:** ABOM runs as a read-only GitHub App it creates through the manifest flow — private, so
installable only on the account that owns it — minting an installation token per account and
recording installations as evidence beside the declaration, re-read whenever an installation event
arrives; deliveries reach a workstation through a relay, and a production address, readers signing
in through the App, and workflow callers are not built. What the events the address receives do is
in [`docs/architecture.md`](../../docs/architecture.md#the-pipeline).

## Why this exists

ABOM authenticates as a person. `internal/ghauth` reads an authenticated `gh` session or
`GITHUB_TOKEN`, which is right for a tool somebody runs and wrong for a service an enterprise
depends on, in three ways that all bite at once: the crawl's rate limit is one human's, what ABOM
can see follows whoever last logged in, and when that person's access changes the install degrades
without anything in the data saying so.

**ABOM's scope is now an enterprise** — many organizations in one instance, by the operator's
decision, reversing the one-organization boundary [the vision](../../docs/vision.md) used to draw.
An App owned by the enterprise and installed on its organizations is the identity that matches:
granted deliberately by someone who could grant it, surviving the departure of whoever set it up,
and naming by its installations exactly which organizations ABOM may read. Installation tokens are
still per organization, so the instance holds one per installation; the installation list is the
enterprise as ABOM sees it.

Readers stop being local here, and the reason the old boundary excluded enterprises — a tool spanning
organizations inherits no permission model — is answered by inheriting GitHub's: **a reader sees
what GitHub says they can read, and nothing ABOM shows them is derived from what they cannot.**

A workflow client also needs its own authenticated path to the server: `abom-check` cannot borrow a
maintainer's session or receive the server's GitHub write credential just to request one authorized
operation.

## Pending

1. **The address.** TLS and a name that does not change when the process restarts, for the
   interface and for the App's webhook deliveries. **The fork is how deliveries arrive:** an
   endpoint GitHub can reach, verifying every signature before parsing; or a relay the instance
   pulls from, which keeps the instance unreachable and adds a component that can lose events.
   Decide with [`../tools/deployment.md`](../tools/deployment.md). The workstation path is a relay:
   `ABOM_EVENTS_RELAY` reads a smee.io channel and hands each delivery, headers and body as
   received, to the same receiver. It loses whatever arrives while ABOM is down, and smee re-encodes
   JSON — a body with `\u` escapes, `\/` or extra whitespace no longer matches its signature and is
   refused — so it is for testing, with the sweep behind it.
2. **Readers authorized by GitHub.** Readers sign in through the App. Every read is filtered to the
   organizations and repositories GitHub says that reader can read, with the visibility held for a
   bounded time and read again, never assumed from the last answer forever. Depends on 1.
3. **Authenticate workflow callers and authorize individual operations.** Define short-lived,
   repository-scoped verdict-read grants for [abom-check.md](abom-check.md). Verify repository,
   workflow/event, base/head commits and expiry, and return evidence for those exact lock contexts.
   Separate evidence reads from any justified check-run permission. Refuse expired/revoked grants
   and cross-repository access, including misuse of fork-PR identity. The workflow client receives
   no submission or discovery authority. Depends on 1 and 2, not on new publication grants; those
   are deferred in [dependency-submission.md](dependency-submission.md).
4. **A declared account the App cannot be installed on.** `app create` makes a private App, which
   GitHub lets only its owner install, and a declared account with no installation is listed by
   nobody: `advanced-security`, declared beside `has-ghas`, fails its listing every hour as not
   installed and reads as unknown in Settings — the constraint below working, not a fault. Choose
   before a second account is installed: make the App public in GitHub's settings for it (ABOM
   still reads only what is declared, but anyone may then install it, so the installation lending
   public reads must be named with `--app-public-reads` rather than be the earliest); hold one App
   per account, several App directories chosen by owner; or an App the enterprise owns, where the
   host offers one. The first is a setting; the second is the shape an enterprise whose
   organizations have different owners needs.
5. **A narrower installation than the declaration.** An account declared in full with an
   installation on some repositories names the rest "missing" at enumeration, and the prune offer
   would list them. Nothing guards it yet; install on every repository of an account declared whole.

## Authentication decision

Choose between a verified GitHub OIDC exchange and a short-lived repository-scoped App-backed
caller grant before authenticating workflow callers. For either, document the trusted issuer and
audience, repository/installation binding, workflow/event/ref restrictions, expiry, revocation and
replay handling. Do not replace missing issuer support on a host with an unscoped shared secret. The
workflow action receives no App private key or GitHub contents-write credential.

`X-Abom-Control` currently guards cross-site requests and local control; it is not an authenticated
caller identity. Enabling remote control alone cannot authorize a workflow client.
[github-hosts.md](github-hosts.md) owns destination/credential routing and its host support matrix;
this plan owns service identity, reader authorization, caller grants and deployment.

## The constraints that must not be broken

**The App asks for what ABOM's features read, built or planned, and only to read.** Audited against
GitHub's permission tables: repository Metadata, Contents, Code scanning alerts, Administration and
Actions, and organization Custom properties and Administration, all read, and no account permission.
Every installation must approve each new permission, so the operator grants the whole set once
rather than one feature at a time. Actions read is for check 5's run logs and `workflow_run`.
Organization Administration read is only for `repository_ruleset` events about organization
rulesets; ABOM never calls `GET /orgs/{org}/rulesets`, which GitHub documents at write, and re-reads
each repository's protected branches instead. Refused: organization Members, because ownership is
declared and never read from membership; repository Custom properties, whose one endpoint writes;
and every write. The manifest requests exactly this set, and its trace test fails on a grant that
neither a call nor an event handler needs, on an event no processor method handles, and on a path
the reader builds that no grant covers.

**An installation is evidence, never a declaration.** Being installed somewhere does not make its
supply chain anyone's remediation work, for exactly the reason membership does not — see
[`../db/owned-scope.md`](../db/owned-scope.md). Ownership stays declared, organization by
organization. An installation can contradict a declaration, and can support one, and can do neither.

**A reader sees what GitHub lets them read, and an aggregate never names what they cannot.** A count
spanning organizations discloses that something exists: how many repositories reach an action, how
many findings a rule has. It is computed over what the reader can see, or reported as that plus an
unnamed remainder — never with the remainder's names, paths or findings.

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
- The install keeps working when the operator who created it loses access to the enterprise.
- Every permission the App requests is traceable to an endpoint ABOM calls, and removing any one of
  them breaks a named thing.
- A revoked installation puts that organization's declared accounts into *unknown* and roots
  nothing, rather than silently reporting an organization as empty.
- A reader without access to an organization sees none of its repositories, findings or edges, and
  every aggregate they see either excludes it or reports it only as an unnamed remainder.
- Caller-contract tests reject wrong issuer/audience, repository, workflow/event, commit/ref and
  expired or revoked grants. Valid verdict reads cannot submit, control discovery, renew other
  repositories or change ownership; returned lock contexts match the requested commits.
- Test the permitted workflow operation and denied operations against an approved dedicated
  repository. Tokens and private keys appear in neither logs, summaries nor error responses.
- Change ingestion is measured on a real enterprise organization once one is available, and each
  figure is stated for the organization it came from: an organization ruleset's targeted re-read
  against the whole listing it replaced (115 REST requests and 19.5 s for 31 repositories on the
  crawled organization, where the narrowest ruleset read 2), the owned-closure walk behind every
  claim and objective read (14.7 ms for 246 repositories there), and each pool's spend.
