---
name: add-a-guarded-operation
description: 'Add something in codeql-abom that changes state — an API route that mutates, a command that writes, a content decision a handler makes — so it is a signed decision checked against the caller''s role. Use when adding a POST/PUT/DELETE route, a mutating CLI command, a new authz operation, or a role, or when a test such as TestEveryAPIMutationIsARegisteredOperation or the decide guard fails. Covers the registry in internal/authz, controlOperations in serve, commandDecisions in cmd, route and command decisions, content decisions citing the requester, the interface''s role-disabled controls, and the tests that fail when a step is missed.'
---

# Add a guarded operation

Every change ABOM makes is a decision signed before it is acted on, naming who asked, and every
entry point reaches the same decision. The defect this exists to prevent shipped once: the command
line declared scope the interface refused. A route or command added without a decision is that
defect again, so the build refuses it.

## The pieces

| Piece | File | What it holds |
| --- | --- | --- |
| Operation | `internal/authz/decision.go`, `init()` | Name `abom/<verb>`, the least role, and what its evidence records |
| Route | `internal/serve/controldecision.go`, `controlOperations` | `"METHOD /path"` to operation |
| Command | `cmd/codeql-abom/decide.go`, `commandDecisions` | Command name to operation; open the store with `openDecided` |
| Interface | `ui/Services/RoleService.cs`, the control's component | `Roles.RefusalAsync(op)`, then `StateHasChanged()`, then the reason on a `Tip` outside the disabled control |

## Procedure

1. **Register the operation** with the least role that may do it. `Register` panics on a role it
   does not know, so a typo fails at start, not in production.
2. **Route.** Add the pattern to `controlOperations`. The middleware signs the route decision before
   the handler runs, refuses a role below the operation's with `role-too-low`, and passes the
   decision to the handler as `authz.RequesterOf(ctx)`.
3. **Command.** Add it to `commandDecisions` and open the store with `openDecided`, which decides
   before returning the store. In a pod, the command line is named by its ServiceAccount.
4. **A handler that decides about content** — a declaration, an export — decides again over what it
   read, as the same principal, and cites the route or command decision's digest in its evidence
   (`Route`). Two unlinked decisions for one request read as two acts in the log.
5. **The interface** offers the control disabled where the role does not allow it, naming the role,
   and draws that as soon as the role is known, before the component's other reads finish.
6. **Problems.** A refusal the handler adds is a code in `internal/serve/problems.go`; the closed list
   test fails on a code used and not declared, or declared and not used.

## What fails when a step is missed

- `TestEveryAPIMutationIsARegisteredOperation`: a mutating route with no operation, or one served by
  nothing.
- The decide guard in `cmd/codeql-abom`: a command that writes without `openDecided` or
  `decideCommand`.
- `TestEveryEntryPointReachesTheSameDecisionAboutADeclaration`: entry points judging one declaration
  differently.
- `TestNoExportedNameInInternalIsOnlyUsedByTests` and `TestEveryInternalPackageIsReachableFromABinary`:
  a helper only tests reach.

## Rules that are easy to break

- **A signed string is contract data.** Principal IDs, operation names and refusal reasons are signed
  into the log; change one only on purpose, never as a wording pass.
- **Never sign on someone's behalf.** No migration or repair makes a decision nobody made.
- **Unknown is not permission.** A role, eligibility or licence that could not be read refuses.
