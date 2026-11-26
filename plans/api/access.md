# Who may use a hosted instance, and how each permission is checked where it is used

**Status:** Built and drilled on kind: people sign in through an OpenID Connect provider or through
GitHub with the reading App, every route and command is a signed decision checked against the
caller's role, and a declaration's decision names who asked and cites the decision that admitted
them. What is open is how long a role holds after the provider takes it away, sessions surviving
a restart, and the parts only a real cloud and a real organization can exercise.

## Why this exists

The reported defect was that the command line declared scope the interface would refuse. It was a
check at one producer trusted by every consumer, so every use of a permission now verifies a signed
decision, and every entry point reaches the same decision. A hosted instance is reached by AppSec,
Cyber, Risk and Audit, who will not accept an interface anyone on the network can read or change.

## Pending

1. **A role taken away is taken away promptly.** GitHub sign-in reads a role again before a
   change once it is older than `--github-role-refresh`. OpenID Connect roles are read once, from the
   ID token at sign-in, and hold for the session's eight hours. Choose one:
   - a shorter session for OpenID Connect, which signs people in more often;
   - a refresh token kept server-side and redeemed for a new ID token before a change, as GitHub
     sign-in re-reads, which keeps a credential the server currently never holds.
   Depends on nothing.
2. **Sessions survive the coordinator.** Sessions are held in the coordinator's memory, so a rollout
   or a restart signs everyone out, and a session ends only at its absolute expiry. A table holding
   each session's cookie digest, who it is, its role and when it was last used, with an idle expiry
   beside the absolute one. The cookie's value is never stored. Depends on nothing.
3. **The single binary on a network.** Without sign-in the interface admits loopback alone, unless
   `--allow-remote-control` names otherwise; the chart already refuses to listen beyond loopback
   without sign-in. Decide whether the binary refuses a non-loopback listener without sign-in too, or
   keeps the flag for a trusted network and says so at start. Depends on nothing.
4. **GitHub sign-in on a real organization.** The reading App gains `members: read`, an owner
   approves it, its callback URL and client secret are set, and a team's members sign in with the role
   the map gives them; someone removed from the team loses it within the refresh. Only a real
   organization exercises this. Depends on nothing.
5. **The KMS signer on EKS.** Decisions signed with an AWS KMS key, through the coordinator's own
   identity, by the [runbook](../../docs/deployment/kubernetes.md#on-eks), and `audit verify`
   passing over them. Depends on an EKS cluster.

## The constraints that must not be broken

**Verification happens where a permission is used, not where it was granted.** A consumer that acts
on a row because a producer checked it is the defect this plan exists to remove.

**Unknown is not permission.** An eligibility, role or licence that could not be read refuses, and
says it could not be read; it never reads as granted. A role GitHub cannot be asked about is no role
for a change.

**A person is named by what their provider keeps for them.** Decisions record the issuer and subject,
or the GitHub account ID, with what they were called as evidence; an address or login that can change
is never the identity.

**A signature is never made over a decision nobody made.** No migration, retry or repair signs on
someone's behalf. A declaration no decision holds for stays inert until someone makes it again.

**The log is evidence, so nothing edits it.** Corrections are new entries.

**This is not a claim against a compromised coordinator.** A process that holds the signing key can
sign anything; the log and the KMS audit trail make that visible after the fact.

## Verification

- After a provider removes a role, a change made with it is refused within the window chosen in 1.
- A rollout of the coordinator leaves a signed-in session signed in, and an idle one ends.
- On EKS, `audit verify` passes over decisions the KMS key signed.
