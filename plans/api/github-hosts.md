# A repository lookup names its host and its credential

**Status:** not built end to end; host-routing contracts and two-host tests are startable locally.

## Why this exists

An organization on an enterprise GitHub host can use both mirrored actions and public upstream
actions. The repository name alone cannot choose the server or credential, and the same
`owner/repo` can exist on both hosts with different code.

ABOM has separate paths today: `ghauth.Token` asks `gh auth token` without a hostname,
`Checkout.cloneURL` defaults to `github.com`, and `submit.NewClient` defaults to `api.github.com`.
Overridable URLs in individual clients are useful for tests, but are not a shared identity and
credential contract across enumeration, ref resolution, cloning and submission.

## Pending

1. **Define host configuration and repository identity together.** Name the primary web, API and
   clone endpoints plus any explicitly permitted public upstream host. Inventory the REST,
   GraphQL, `gh` and Git callers that must consume this configuration. Qualify repository,
   provenance, frontier and cache identities by host before admitting colliding names. Preserve
   requested and canonical names across redirects. Depends on nothing.
2. **Route each operation with a destination-scoped credential.** Reads, clone authentication and
   writes must agree on the repository's host. Never send a primary-host credential to a public
   fallback or an unapproved redirect destination. Validate endpoints and redirects, including
   separation of web and API URLs; repository content cannot choose an arbitrary credential
   destination. Reuse current injectable clients rather than adding a second transport stack.
   Depends on 1.
3. **Make fallback a configured choice, not an interpretation of an error.** A denied read, rate
   limit or network failure does not establish that a repository lives elsewhere. A `404` can
   hide denied access too. Permit public lookup only under an explicit routing policy, keep each
   host's outcome, and refuse ambiguous same-name matches rather than silently switching code.
   Depends on 2.
4. **Cache evidence, not guesses.** Cache successful routing and metadata by host, repository and
   credential/installation context, with freshness and invalidation rules for renames, transfers
   and changed access. Keep bounded negative/error state separate from proven absence. Include
   host identity in the commit-keyed cache owned by [checkout-cache.md](../db/checkout-cache.md)
   and the tags lookup in [version-resolution.md](../db/version-resolution.md); do not duplicate
   either implementation. Depends on 1 through 3.
5. **State the support and permission matrix.** Document GitHub.com, enterprise-host and optional
   public-upstream behavior, endpoint permissions, private repository access and disabled fallback.
   EMU is an identity model, not automatically a separate host. Verify supported combinations on
   authorized environments before advertising them. Depends on 2 through 4.

## Dependencies and boundaries

This routes each lookup to its host; it does not choose which organizations ABOM reads, which the
enterprise App's installations do. Public lookup does not declare ownership or extend the crawl's
roots.

[enterprise-server.md](enterprise-server.md) owns App identity, credential renewal, ingress and workflow-caller
authentication. Those deployment requirements do not block deterministic local host-routing tests.
Live GHES verification requires an authorized environment; a passing mock test is not a GHES
support claim. [fork-provenance.md](../db/fork-provenance.md) consumes this routing for cross-host
ancestry without making ancestry a prerequisite of routing.

[Native lock ingestion](../../docs/architecture.md#locks) must agree with this host identity. The inspected
released parser/CLI combination uses schema `v0.0.2`; the shared parser's development branch
describes `v0.0.3` with canonical GitHub.com/GHE tenant hostnames. Neither that field nor the CLI's
hostname option proves general GHES runtime enforcement. Pin compatible releases, retain the
governing lock's host/IDs and refuse unsupported combinations without routing to a convenient
same-named repository. Basic supported-host lock ingestion does not wait for this plan's full
enterprise routing implementation.

## The constraints that must not be broken

**A host is part of identity, not just a base URL.** No join, cache hit or version measurement may
combine same-named repositories from different hosts. Existing single-host results must retain
their meaning when the primary host becomes explicit.

**Credentials stay at the destination and authority they were granted for.** Public read fallback
never redirects a submission or upgrades a read credential to a write grant. Host selection in a
workflow request is verified against the server's configuration, not trusted from the caller.

**Failed, unread and absent are different answers.** Cached errors cannot become successful empty
enumeration or evidence that a public repository is the private repository's replacement.

## Verification

- Use two HTTP test hosts containing the same repository name and different refs. Assert the
  selected host, request path and authorization on every operation, not just the final result.
- Exercise disabled and authorized public lookup, `401`, `403`, `404`, `429`, network failures,
  redirects, credential rotation and revocation. No credential reaches the wrong host, and no
  failure erases previously measured data or silently changes source identity.
- Assert cache/request counts, host-qualified checkout reuse, and invalidation after identity or
  access changes. Measure API cost as calls rather than relying on elapsed time.
- On a SQLite backup, explicit primary-host configuration produces unchanged dependency,
  ownership, reach and finding results for the existing single-host data set.
- Perform read-only checks against approved GitHub.com and enterprise targets for each advertised
  combination. Submission-host tests remain mocked until an approved dedicated target is available;
  all existing target and manifest-path guards must still refuse unauthorized writes.

## Prior art

Jesse Houwing's
[actions-dependency-submission](https://github.com/jessehouwing/actions-dependency-submission)
separates primary and public clients in
[OctokitProvider](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/src/octokit-provider.ts)
and documents [enterprise-host configuration](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/docs/github-ghes.md).
That client separation and reuse of routing decisions inform this plan, following
[issue #18](https://github.com/advanced-security/codeql-abom/issues/18). A long-lived ABOM service
also needs host-qualified stored identity, credential isolation and explicit uncertainty when
lookup fails; fallback alone does not establish those properties.
