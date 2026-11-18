# An ownership claim names the role that made it

**Status:** A personal owner or an organization admin may declare. A security manager comes back as
a member and may not. A repository maintainer is not consulted.

## Why this exists

`CanDeclare` in [accounts.go](../../internal/store/accounts.go) is `owner` or `admin`, with a
comment that finer roles belong with scoped-token support. That is this plan.
`GET /user/memberships/orgs` returns `admin` or `member` only, so a security manager is recorded
as `member` and the picker refuses the organization. Repository permission is never read.
[owned-scope.md](../db/owned-scope.md) deferred a member declaring the repositories they
administer for a different reason, measured on the operator's own token: every such repository
checked was private (`ps-resources/es-offerings-ops`, `ps-resources/github-msft`,
`TaxPilotAI/TaxLing`, `els-ai/ghas-guzzlers`), and only an organization owner can install an App
that asks for organization permissions. A declaration made then would have been listed and never
read.

Finding those repositories is not one cheap call. `GET /user/repos?affiliation=collaborator`
is one call and finds direct grants only — 11 outside owned organizations in that measurement.
Team grants found 208 more, at 31 teams. Listing a whole organization and keeping
`permissions.admin` is complete and costs the organization's size: `dsp-testing` was 4,454
repositories, 45 pages. Fourteen of those were temporary GHSA advisory forks (`…-ghsa-…`), which
are not repositories anyone should declare. Enumeration still lists an include-only account whole
before filtering it.

This lands in the same implementation as [production-credentials.md](production-credentials.md).
A production pod that can read and still cannot say who may declare has not stopped needing a
person. The gh-auth role rules and the App eligibility rules are one change to the picker, not a
follow-up PR.

## Pending

1. **gh-auth roles, decided.** Personal account: the authenticated user, as now (`owner`).
   Organization: membership role `admin` — GitHub's name for an organization owner — or the
   security-manager role. `member` alone still cannot declare the organization. Repository, inside
   an organization the user cannot declare whole: permission `admin` or `maintain` on that
   repository, saved as an include list, not as a declaration of the account. Write, triage and
   read do not qualify. There is no repository role named Owner; an organization owner appears as
   `admin` on the repo. Advisory-fork grants stay excluded. Depends on nothing for the rule. The
   picker that offers it in production depends on 2 and on
   [production-credentials.md](production-credentials.md).
2. **Security manager is a second read, not a membership role.** `user/memberships/orgs` cannot
   say it. The check is the organization's security-manager assignment — the teams
   `GET /orgs/{org}/security-managers` returns, and a direct organization-role assignment where
   the token can read one. A failed read is "cannot say", shown beside the account, not a
   contradiction and not a denial. A fine-grained token that reports no classic scopes already
   must not be treated as a complete membership list; the same rule applies here. Depends on 1.
3. **Repository permission is a second read too.**
   `GET /repos/{owner}/{repo}/collaborators/{username}/permission` is `admin` or `maintain`, or it
   is not a claim. A 404 from a token that cannot see the repository is "cannot say", not "not a
   maintainer". The listing that offers names still comes from GitHub's answer, never from the
   store. The route that finds team grants is the expensive one; do not ship the collaborator
   affiliation list as if it were complete, and do not list an organization's every repository to
   discover eleven. Depends on 1.
4. **An App installation makes an organization eligible. It does not declare it.** When an App is
   configured, an organization is offered for an org-level claim only when this App is installed
   on it. GitHub already required an organization owner to install an App that requests
   organization permissions. Record the installer from the installation delivery's `sender.login`
   on the installation evidence, which [appinstallation.go](../../internal/store/appinstallation.go)
   does not store today. A delivery with no sender leaves the installer unknown, which is not
   "nobody installed". Do not re-check the creator on a timer. Saving is still an explicit
   declaration; nothing is preselected. A security manager cannot install that App and cannot
   originate the org-level production claim. They declare through the gh-auth path, and they
   operate an instance an owner installed. Depends on
   [production-credentials.md](production-credentials.md) for the process having an App and no
   `gh`, and on 1 for what "eligible" then allows the picker to offer.
5. **A repository is readable in production only when the installation includes it.** A maintainer
   cannot widen the installation. An include naming a repository the App is not installed on stays
   declared and unread — listed, marked, not crawled. The person's token does not become a crawl
   credential. That was the resumption condition owned-scope named, and it is refused. Depends on 4.
6. **The pre-flight checks the role the declaration claims.** Verification is recorded and shown
   beside each declaration. The gate still asks whether a scope is declared; this check says
   whether the recorded role still supports it, and a failure to read the role is "cannot say",
   not a crawl that will not start. It is not what bounds discovery. That item lived on
   owned-scope and is this plan's. Depends on 1 and 2.

## Decided

**Two paths, one picker.** gh-auth: owner, admin, security manager at the organization;
admin or maintainer at the repository, as an include. App: an organization is eligible when this
App is installed, because the installer was an organization owner. Installation remains evidence
beside the declaration, never the declaration. [enterprise-server.md](enterprise-server.md) already
states that, and this plan does not grow a second reader of `abom_owned_scope_honored`.

**A security manager does not originate a production org claim.** Doing so would need a user
identity in production, which [production-credentials.md](production-credentials.md) exists to
remove.

## The constraint that must not be broken

**Membership still decides nothing on its own.** A role makes an account eligible to be offered.
Nothing becomes owned until a selection is saved. The picker offers; it never declares.

**A declaration is the only source of ownership.** Every "yours" figure still reads the honored
view. An installation, a security-manager assignment, and a repository permission are evidence
about access. Provenance is still not ownership on screen.

**Absent is not a denial.** A token that cannot list security managers, or cannot see a
repository's permission, does not remove a saved claim and does not contradict one.

**The person's token is not a crawl credential.** Listing for the picker may use it on the gh-auth
path. Discovery and fetch do not. A private repository the App is not installed on stays unread.

## Verification

- A `member` cannot declare the organization. A security manager can, on the gh-auth path, even
  though memberships says `member`. A maintainer can declare that repository and no other. Write,
  triage and read cannot. An advisory fork is not offered.
- An installation offers the organization and does not save it until someone does. The installer
  is the delivery's sender when one was present, and unknown when it was not. A security manager
  with no installation is not offered the organization on the App path.
- A failed permission read does not delete a saved claim and does not stop discovery by itself.
- An include for a repository the App is not installed on is marked unread. No crawl request for
  it carries the person's token.
- With nothing declared, discovery is still refused. Declared-but-disabled is still refused. An
  empty include list on an include-governed account still scopes to nothing.
