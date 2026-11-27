# Who may use a hosted instance, and how each permission is checked where it is used

**Status:** Built and drilled on kind, and GitHub sign-in used on `has-ghas`: people sign in through
an OpenID Connect provider or through GitHub with the reading App, every route and command is a
signed decision checked against the caller's role, every control the role does not allow is offered
disabled, sessions are kept by digest so a restart signs nobody out, a retired key's decision is
honored only under the chain a key still deciding checkpointed, and without sign-in the binary listens
on loopback alone. What is open is what only a real cloud and a second account can exercise.

## Why this exists

The reported defect was that the command line declared scope the interface would refuse. It was a
check at one producer trusted by every consumer, so every use of a permission now verifies a signed
decision, and every entry point reaches the same decision. A hosted instance is reached by AppSec,
Cyber, Risk and Audit, who will not accept an interface anyone on the network can read or change.

## Pending

1. **A role taken away within the window, on a real organization.** GitHub sign-in reads a role again
   before a change once it is older than `--github-role-refresh`; an identity provider's session
   lasts an hour, since its roles are read only at sign-in. Remove a second account from
   `has-ghas/security` while it is signed in, and show its next change is refused within five
   minutes; do the same through an identity provider within the hour. Only a second account can.
   Depends on nothing.
2. **The KMS signer on EKS.** Decisions signed with an AWS KMS key, through the coordinator's own
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

- After a provider removes a role, a change made with it is refused within the window in 1.
- A rollout of the coordinator on kind leaves a signed-in session signed in.
- On EKS, `audit verify` passes over decisions the KMS key signed, and the scope gate honors a
  declaration the local key decided before the move.
