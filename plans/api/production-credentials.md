# A production instance does not need a person's token

**Status:** Built and measured live on `has-ghas`: with only the App's credentials, a crawl's reads,
the accounts check, linked request examples and Copilot authoring sessions all run, and nothing reads
a person's token or writes. What GitHub answers when an organization's policy refuses App Copilot
requests, and a security manager's own token, are not yet measured.

## Why this exists

A production pod has no `gh auth` session. What it does instead is in
[`docs/architecture.md`](../../docs/architecture.md): each call is minted a token holding only its
own permissions, a permission an installation has not approved refuses its call by name before
GitHub is asked, and nothing falls back to another credential. What is left is measuring it against
GitHub, which needs an owner's approval this plan cannot give itself.

**Copilot through the installation, measured on Copilot CLI 1.0.88 and SDK v1.0.14** (2026-11-19),
the server run with no `gh` reachable (a stub on `PATH` that records and refuses every call), no
`GITHUB_TOKEN` or `GH_TOKEN`, and git's system and global configuration hidden — the keychain held a
person's github.com credential that git would otherwise have used. The CLI's session-level token
provider refuses an installation token three ways, each only visible against the real runtime: it
names the host as `https://github.com`, it requires more than 3600 s left where an installation
token lives 3600 s at most, and it refuses any credential "without a stable account identity",
which an installation token is. The same token as the client token authenticates (`type: token`, no
login), and the `has-ghas` installation is offered two models: `auto` and `gpt-5.3-codex`. A GPT
model searches with `rg` and writes with `apply_patch`, so a loop offering Claude's `grep`, `create`
and `edit` alone left the agent unable to write. With both families offered and confined, sessions
on request 4 (`acme/insecure-workflow-commands`, filed from zizmor's `insecure-commands` audit) ran
through the App, metered in AI credits, with the model each call ran on recorded.

Measured on the crawled organization (2026-11-19), serving a copy with the App beside it: readiness
names the installation token and refuses Copilot with `organization_copilot_requests: read` not
granted on `has-ghas`; the identity is the App's one installation, in 0.43 s, and the declaration it
does not cover reads *cannot be checked*, not contradicted; every graph total, the owned set and
both declarations' honored state are unchanged. The App's registration, read the same day, asks for
every read plus `organization_copilot_requests: read` and no write. Once the `has-ghas`
installation accepted it, readiness turned from that refusal to ready on the same copy, with no
change to any graph total.

## Decided

**The App writes nothing.** A dependency snapshot and default setup belong to the repository: the
newer route is that a repository adopts `actions.lock` itself, and its code scanning configuration is
its owner's, outside ABOM. So the App asks for no write, a server reading as it refuses to start when
given `--submit-targets`, `--enable-targets` or `--enable-accounts`, and its snapshot client approves
nothing. Without an App, the existing operator-token writes and their guards are as they were.

## Pending

1. **An organization that refuses App Copilot requests.** `has-ghas` allows them, so what GitHub
   answers when an organization's policy does not — at the mint, at client start, or at the first
   turn — is unmeasured, and readiness reports only a refused mint. Measure it on an organization
   whose owner turns the policy off, and make readiness say that refusal in its own words. Depends
   on nothing.
2. **A security manager's own token.** On the operator's `read:org` token every one of 19 member
   organizations reads *security manager: cannot say*, because both readings — the organization
   roles and the security managers teams GitHub says are closing down — need `admin:org` of a
   classic token, and a token reporting scopes without it is not spent asking. Measure a token
   carrying `admin:org` held by an actual security manager: that the role reads *yes*, that the
   picker then offers the organization, and that the security managers endpoint still answers. If
   it has gone, the organization roles reading is the only one, and a security manager whose token
   cannot read roles cannot be confirmed at all. Depends on nothing.

## The constraint that must not be broken

**A missing user token is not a denial and not an empty membership.** Production with an App and
no `gh` starts and reads. It authors, or it says which grant or org policy is missing. It does not
fall back to an ambient token, and it does not record that the operator belongs to nothing because
the CLI was absent.

**A token is never stored, logged, or written into an event.** The Copilot child receives the
installation token minted for its session as the client token, inherits no GitHub token from the
server's environment, and has no logged-in user to fall back to.

**An installation token is not refreshed under a session.** The client token is the only route
the CLI accepts for it, and that route takes no refresh, so a session ends a few minutes before the
hour its token was minted for. A longer `--authoring-limit` is cut to that hour rather than failing
mid-turn. The user-token route — readers signing in through the App, in
[enterprise-server.md](enterprise-server.md) — is what the provider was built for.

**A server reading as the App writes nothing.** Not with the App, which asks for no write, and not
with the operator's token, which a production pod does not have. A write target named beside an App
stops the server rather than being ignored. [dependency-submission.md](dependency-submission.md)
still defers any new publisher.

**A role makes an account eligible; it never declares it.** A security manager or maintainer role,
and an App installation, are evidence about access. One that cannot be read is *cannot say*, never
*no*, and a check that a role no longer supports a declaration changes nothing about it.

## Verification

- Items 1 and 2 each state their expectation before the session or the read, and record what
  GitHub answered beside it.
