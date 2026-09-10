# Every action has an API, and the client only fetches and renders

**Status:** the invariant holds and nothing enforces it. Every action the interface offers is
behind an endpoint because each one was built that way, not because anything fails when it is not.

## Why this exists

The interface was the first client and for a while the only one. Two more are coming — the
verdict action in `api/verdict-gate.md`, and event-triggered submission in
`api/dependency-submission.md` — and both need to perform actions the interface already performs,
from a caller that has no browser.

Anything the interface can do that has no endpoint behind it is work that has to be built a second
time, differently, for that caller. The rule is therefore not "add an API layer". It is: **no
capability may exist only in a client.**

## Pending

1. **A test that fails when a capability appears only in a client.** The requirement is that adding
   a control with nothing behind it breaks the build. Candidates: a generated inventory checked
   into the tree and diffed in CI; a bUnit test asserting that every element carrying a control
   hook maps to a known route; a lint over the Razor sources. The first is the least clever and the
   most checkable, and being checkable matters more here than being thorough.
2. **An endpoint inventory in one place**, so a new client can be written against a list rather
   than by reading Razor pages. Most of item 1, if item 1 takes that shape.
3. **Control-guard consistency.** Reads are unguarded, writes carry the control header. A third
   client will find every inconsistency the interface never exercised.

## The constraints that must not be broken

- **No capability exists only in a client.** If the interface can do it, an endpoint can do it.
- **A client renders; it does not decide.** Filtering, folding and layout are rendering. Anything
  that changes what the corpus holds, or what it asserts, belongs behind the API.
- **Guarding is a property of the endpoint, never of the caller.** A control-guarded action must
  be guarded for the action, the CLI and the interface identically, because the reason for the
  guard — this spends the operator's machine or writes to GitHub — does not depend on who asked.

## Verification

- Every write path the interface offers is reachable with `curl` and no browser.
- The action in `api/verdict-gate.md` is implementable without adding an endpoint that exists only
  for it — if it needs one the interface could have used, that is a gap this plan should have
  caught.
- A deliberately client-only capability is introduced and the test in item 1 fails. Written first,
  and watched to fail, or it is proving nothing.
