# Read in response to events, not on a timer

**Status:** blocked on ingress. Nothing here is built, and the `--recheck-ttl` sweep is the floor
until it is.

## Why this exists

Drift detection already works. `internal/expand/recheck.go` returns analyses whose reference has
moved to the queue, wired into `serve` and paced by `--recheck-ttl`. What is missing is only the
trigger.

The recheck is a **sweep**, not a reaction. Its floor is the TTL: a workflow edited now is noticed
up to 24 hours later, and every probe in between asks a repository that did not change whether it
changed. **1,997 analyses are probeable** — their reference can move — so the sweep spends that
many probes to learn what one event would have said outright.

This is deliberately the *reading* half. Reacting to change by re-reading is reversible and its
worst outcome is a wasted analysis. Reacting by writing — resubmitting a dependency snapshot
without being asked — is neither, and is held separately in `api/dependency-submission.md`.

## Pending

1. **Ingress.** This project runs on one workstation with no public URL, and every item below needs
   one. The address and the identity that would answer at it are [`org-server.md`](org-server.md);
   a plan that assumes either already exists is describing a different deployment.
2. **A receiver for `push`.** The payload names the repository and the files; a push touching
   `.github/workflows/**`, `action.yml` or `action.yaml` is the only kind that can change the
   graph. Everything else is dropped without a probe.
3. **Signature verification**, `X-Hub-Signature-256`, before the payload is parsed at all. An
   unauthenticated endpoint that schedules CodeQL runs is a way to spend somebody else's machine.
   Depends on 2.
4. **Reuse `RequeueRepository`.** The event decides *what* and *when*; the existing expedite and
   requeue decide what happens. Nothing new analyzes anything.
5. **`security_advisory` as a second signal.** A new advisory changes exposure without any
   repository changing, which the recheck can never notice because nothing drifted.
6. **Fall back to the sweep, never replace it.** An event that never arrives — a webhook removed,
   ingress down, a repository nobody registered — must degrade to the current 24-hour behavior
   rather than to silence.

## The constraint that must not be broken

**An event is a hint, not a fact.** It says something may have changed; the probe still decides
whether anything did. Trusting the payload's SHA instead of resolving the reference would let a
replayed or forged event rewrite what the corpus believes it read.

## Verification

- Time from a push touching a workflow to that repository being re-analyzed, against the 24-hour
  TTL floor it replaces.
- The probe count for one event is one repository, not the whole probeable set.
- With the receiver disabled, the sweep still finds the same drift, late.
