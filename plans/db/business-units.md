# A business unit is read from the organization, never guessed

**Status:** not built. ABOM makes no request for repository custom properties and holds no column
for them.

## Why this exists

An organization of ten thousand repositories has nobody who can act on a finding in it. Every
mechanism that moves remediation forward is addressed to somebody narrower — a security campaign has
a named contact, a vulnerability report has an audience — and ABOM currently offers no scope between
"one repository" and "the whole account".

The dividing line already exists and is already administered: repository custom properties. An
organization that runs `business_unit` or `app_name` has said, in a place GitHub enforces, which
repositories belong together. ABOM should read that rather than ask anyone to maintain a second copy
of it.

## Pending

1. **Read and store property values.** `/orgs/{org}/properties/values` is paginated and returns every
   repository's values in one walk, which is cheaper than a call per repository. Needs the
   organization *Custom properties* read permission, which ABOM does not currently ask for.
   **A second route exists and may be better**: `repositoryCustomPropertyValues` is a field on
   `Repository` in GraphQL, so the values arrive already joined to the listing that
   [`../api/org-enumeration.md`](../api/org-enumeration.md) is going to make anyway, for the same
   point. Two REST walks correlated on repository name is the alternative, and a repository renamed
   between them is a defect nobody will reproduce.
2. **Configured keys.** The operator names which key or keys carry the dividing line; ABOM discovers
   the values. A key that no repository sets is its own state and is reported, never silently
   dropped. Depends on 1.
3. **A predicate term.** The view language in `internal/view` is what the Explorer, the API and the
   CLI already share, so business unit enters there once rather than per surface. Depends on 2.
4. **The surfaces.** Discovery coverage per unit; Explorer group-by and filter beside owner,
   repository and workflow; a bill of materials bounded to one unit. Depends on 3.
5. **Policy scope**, so a rule can be adopted for one unit before the organization. Depends on 3 and
   on [`../api/policy-lifecycle.md`](../api/policy-lifecycle.md).

Property semantics are settled upstream and should be copied rather than re-derived: names are
matched case-insensitively, a multi-select property matches if any value matches, and a value
containing a literal comma cannot be filtered unambiguously in a comma-separated list. That last one
is a real limitation and is stated rather than papered over.

## The constraints that must not be broken

**A business unit is read, never inferred.** Not from a repository-name prefix, not from a team
name, not from a path convention that happens to hold today. The property is what GitHub holds; a
pattern that agrees with it by coincidence will disagree the first time somebody creates a
repository, and nobody will notice.

**A repository with no value is its own state, never a default bucket.** Folding unset into "other"
makes a missing property indistinguishable from a deliberate one, which is the same error as
reporting an absent measurement as zero.

**Properties scope ABOM's own work, so ABOM reads them itself.** They are not taken from an external
report, because discovery and policy have to be scopeable before any audit has ever been run against
the organization.

## Verification

- The distinct values ABOM discovers for a configured key match what the GitHub UI lists for that
  organization, including the repositories with no value.
- Requests spent on the whole organization's properties, against a count of repositories. A per
  repository call is the wrong shape and the number will say so.
- Filtering by one unit and summing across all units, including the no-value state, reconciles with
  the unfiltered total. If it does not, something is being dropped rather than grouped.
- A repository whose property changes is re-grouped on the next cycle without re-analysis, because
  the property is metadata about the repository and not evidence about its code.
