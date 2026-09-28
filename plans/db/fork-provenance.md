# An action remembers its upstream without becoming it

**Status:** not built; same-host provenance can be designed and tested independently of host routing.

## Why this exists

An organization can maintain a fork or mirror of an action while its upstream publishes the
advisories and releases people need to inspect. Recording only the dependency's declared name
leaves that context disconnected. Replacing the name with the upstream is no better: a fork may
contain different code, and matching tag names do not establish matching commits.

ABOM's `repository.origin` means seed or upstream acquisition. It is not GitHub fork ancestry,
ownership, or evidence that two repositories contain equivalent code. The missing relationship
needs its own provenance rather than another meaning on that field.

## Pending

1. **Record identity and the source of each relationship.** Keep the declared repository,
   canonical repository returned by GitHub, observed fork parent/source, and configured mirror
   mapping distinct. Name the host, evidence source and observation time; attach commit/ref
   evidence where a statement concerns code equivalence. Reuse the existing observation and
   repository infrastructure where its identity rules fit. An unattempted or failed lookup is not
   a repository known to have no upstream. Depends on nothing.
2. **Read ancestry and accept reviewable mirror mappings.** Read GitHub's fork metadata; allow
   explicit mappings and validated naming-convention rules where no fork relationship exists.
   A rule's output is a configured claim, not an API observation. Validate full coordinates, retain
   conflicting claims, record who configured the mapping, and do not silently choose one. Bound
   ancestry traversal and cache reads by repository identity. Depends on 1.
3. **Carry upstream context without inventing exposure.** Present attributed advisory identities,
   published ranges and measured references beside the fork, with applicability unresolved where
   the evidence cannot settle it. No automatic finding, vulnerable flag, ownership declaration or
   dependency edge follows from ancestry. Depends on 2.
4. **Join version evidence only when it was measured.** Use the relation and lookup owned by
   [version-resolution.md](version-resolution.md). A fork's SHA may be found under a tag upstream;
   a same-named tag in each repository proves nothing by itself. Keep the repository in which the
  tag was observed and the original pin. Upstream package publication remains deferred with
  [dependency-submission.md](../api/dependency-submission.md) until native capability measurement
  demonstrates a residual need; provenance collection does not depend on publishing an alias.
  Depends on 2 and measured version evidence, not a pin comment.
5. **Expose the provenance and its gaps.** Carry it through existing repository/dependency APIs,
   details and exports, with readable distinctions between observed, configured, conflicting and
   unread. This is server behavior consumed by every client; it adds no resolver to `abom-check`.
   Depends on 3; version explanations consume 4 when available.

## Dependencies and boundaries

Same-host collection and its tests do not wait for a hosted service or the workflow action.
Cross-host lookup consumes [github-hosts.md](../api/github-hosts.md); the provenance records must
name hosts from the outset, even while only one host is supported. The identity collection here
does not depend on completing version resolution; version resolution can consume that identity
later without making the two plans circular.

This plan does not resolve a fork by analyzing the upstream in its place, create new owned roots,
or import an upstream repository's development workflows into the fork's runtime dependencies.

[Native lock evidence](../../docs/architecture.md#locks) adds owner/repository IDs and a context-selected commit.
Those identify what was locked, not its fork ancestry or who the operator owns. Preserve that
selected commit when looking up upstream context; a live parent tag cannot replace it. Reuse
GitHub's identity evidence rather than reimplementing its lock verifier.

## The constraints that must not be broken

**Ancestry is not equivalence or use.** A fork relation is not a `uses:` declaration. Do not add
it to dependency traversal merely to connect an advisory. Existing direct/transitive counts and
reach must not change because a provenance statement was added.

**A configured claim never overwrites measured evidence.** Naming conventions are useful inputs,
not proof. They grant no ownership, exclusion, exception or submission permission, and must not
become policy patterns that silently cover new repositories.

**The fork and its raw reference survive every projection.** An upstream version is additional
evidence, not a replacement identity. Security findings still come from rule IDs; advisories remain
attributed external statements.

## Verification

- Cover API-derived forks, mirrors without API ancestry, matching and conflicting configuration,
  redirects, case variants, subfolder actions, missing parents, denied reads and ancestry cycles.
- Two repositories with equal tag names but different SHAs remain different. A shared SHA with an
  upstream tag preserves both identities and the exact source of the version measurement.
- Repeat derivation adds nothing; cached reads have asserted request counts. Failed or exhausted
  lookups remain distinguishable from an observed absence of ancestry.
- On a SQLite backup of the real recorded graph, compare node/declaration-edge counts, reach,
  exposure, direct/transitive totals and existing findings before and after. They stay unchanged;
  only the separately reported provenance and coverage change.
- API and rendered details retain the evidence source and unknown/conflicting states. Any external
  submission experiment uses approved test repositories and a recorded repair procedure.

## Prior art

Jesse Houwing's
[actions-dependency-submission](https://github.com/jessehouwing/actions-dependency-submission),
raised in [issue #18](https://github.com/advanced-security/codeql-abom/issues/18), supplies the
concrete use case and useful patterns in
[ForkResolver](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/src/fork-resolver.ts)
and its [API, mapping and SHA tests](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/__tests__/fork-resolver.test.ts).
The lesson is to retain upstream context even without a GitHub fork relationship. ABOM must also
retain the distinction between configured ancestry, observed ancestry and measured code identity.
