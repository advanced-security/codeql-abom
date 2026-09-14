# A business unit is read from the organization, never guessed

**Status:** read, stored and reported. Property values arrive on the enumeration walk over both
routes, the operator names which key carries the dividing line, and `/api/units` answers how it
divides the corpus. No screen reads it yet, and no predicate filters by it.

## Why this exists

An organization of ten thousand repositories has nobody who can act on a finding in it. Every
mechanism that moves remediation forward is addressed to somebody narrower — a security campaign has
a named contact, a vulnerability report has an audience — and ABOM offered no scope between "one
repository" and "the whole account".

The dividing line already exists and is already administered: repository custom properties. An
organization that runs `business_id` or `app_name` has said, in a place GitHub enforces, which
repositories belong together. ABOM reads that rather than asking anyone to maintain a second copy.

## What measuring changed about the design

**The unset state can be unreachable, and the largest unit can be the one nobody chose.** On the
reference organization `business_id` is `required: true` with `default_value: "0"` and
`require_explicit_values: false`, so every repository carries a value and `withoutValue` is **0** —
a figure that is true and reads as "everything is placed". **8 of 25 repositories sit in unit `0`,
which is the default.** The plan's own constraint says a repository with no value must never be
folded into a default bucket; what measuring showed is that *GitHub* supplies the bucket, upstream,
and ABOM reported it as a unit like any other. `abom_property_schema` now records the declared
default and the breakdown marks it, so a reader can see which bucket is a choice.

`phi-scan` makes the point sharply: all 25 repositories carry `scan-pending`, every one of them the
default. It looks like a unanimous decision and is the absence of one.

**The values are free on the preferred route and a second walk on the fallback.** Adding
`repositoryCustomPropertyValues` to a hundred-repository page leaves the price at **1 point** —
measured at nested page sizes of 5, 10, 25, 50 and 100, all 1. REST has no such field, so it pays
`/orgs/{org}/properties/values`, which returns a hundred repositories at a time rather than costing
a request each, and is correlated on repository name afterwards.

**It is a connection, not a list.** `repositoryCustomPropertyValues` returns
`RepositoryCustomPropertyValueConnection`, so the values are under `nodes { propertyName value }`
and the field takes its own `first`. A `value` is a string for a single-select property and a list
for a multi-select one, and reading only one shape drops every multi-select value silently.

## Pending

1. **A predicate term.** The view language in `internal/view` is what the Explorer, the API and the
   CLI already share, so business unit enters there once rather than per surface. It has to land
   **with an executor**: a term the spec parses and no query applies returns unfiltered results to
   somebody who believes they filtered, which is worse than having no term.
2. **The surfaces.** Discovery coverage per unit; Explorer group-by and filter beside owner,
   repository and workflow; a bill of materials bounded to one unit. Depends on 1.
3. **Policy scope from a unit the interface can pick.** A policy already binds to a unit —
   `store.PolicyScopeUnit` evaluates against the configured key — and nothing yet offers the
   operator the list of values to choose from. Depends on 1.

Property semantics are settled upstream and are copied rather than re-derived: names are matched
case-insensitively, and a multi-select property matches if any value matches. A value containing a
literal comma cannot be filtered unambiguously in a comma-separated list; that is a real limitation
and is stated rather than papered over, which is why each value is stored as its own row.

## The constraints that must not be broken

**A business unit is read, never inferred.** Not from a repository-name prefix, not from a team
name, not from a path convention that happens to hold today. The property is what GitHub holds; a
pattern that agrees with it by coincidence will disagree the first time somebody creates a
repository, and nobody will notice. With no key configured, nothing is grouped.

**A repository with no value is its own state, never a default bucket** — and where the organization
declares a default, the bucket it creates is named as one. Folding unset into "other" makes a
missing property indistinguishable from a deliberate one; leaving the declared default unmarked
makes an inherited value indistinguishable from a chosen one. Both are the same error.

**Values that could not be read are unknown, never empty.** A token that cannot read properties is a
permission problem, not an organization that divides its repositories into nothing, so nothing is
overwritten and the report says why. A repository whose values could not be carried whole keeps
what it had: half a dividing line is a placement nobody chose.

**Properties scope ABOM's own work, so ABOM reads them itself.** They are not taken from an external
report, because discovery and policy have to be scopeable before any audit has ever been run against
the organization.

## Verification

- Filtering by one unit and summing across all of them, the no-value state included, reconciles with
  the unfiltered total. If it does not, something is being dropped rather than grouped.
- The two routes agree on the same organization, value for value. Where they cannot, the difference
  is stated rather than left to whoever compares two figures.
- Requests spent on the whole organization's properties, against a count of repositories. A per
  repository call is the wrong shape and the number will say so.
- A repository whose property changes is re-grouped on the next cycle without re-analysis, because
  the property is metadata about the repository and not evidence about its code.
