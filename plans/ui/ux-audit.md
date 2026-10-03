# Every page holds to the same rules

**Status:** every page names what each column holds and what each action does, in sentences that
`check-style` and `specs/tips.spec.mjs` enforce; severity has its own column; every opened row is
the same card; the Actions unit lists what starting points run or hold, and "what you own" is what
the declared repositories hold and run, in every unit and the Explorer.

## Why this exists

Each rule below was learned on one page and fixed on that page only. The next page to be built, or
read, repeats the mistake, and the reader meets the same problem in a different place. The rules are
the `verify-abom-ui` invariants; this plan is the sweep that applies them everywhere at once.

## Pending

1. **A run's results scroll inside its row rather than page.** The run's card holds a table of every
   repository it covered; 52 on the crawled organization, in a window of its own.
2. **The screenshot pass against invariants 1 to 20, page by page.** 21 to 24 and the tips are
   enforced by tests; the rest have not been looked at across every page since the sweep began.

## Verification

- Every page's screenshot pass against invariants 1 to 24, each PASS, FAIL with the region, or N/A.
