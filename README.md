# codeql-abom

An Actions Bill of Materials for a GitHub organization, built from CodeQL results.

`codeql-abom` answers questions a list of dependencies cannot:

- **What is the blast radius of an action?** How many repositories reach it, directly and
  through intermediaries.
- **What depends on this workflow?** Every repository, with the exact `uses:` references.
- **What should we fix first?** Severity × reach, and nothing else.
- **What breaks if we bump this?** Which repositories are affected, split into those that name
  the action themselves and those that reach it through someone else's.
- **How are references pinned?** Per owner, repository, workflow and individual `uses:` line.

## What it is not

It never forms its own security verdict. CodeQL rule IDs are the only source of one, and every
claim traces back to the query or advisory that made it. If `actions/unpinned-tag` did not flag
a reference, this tool will not tell you it is safe — it will tell you the rule did not look.

Coverage, unresolved dependencies and unplaceable findings are reported as first-class output.
Silence about what was not analyzed reads as completeness, and would be a lie.

## Quick start

```bash
make build

# Find repositories in an organization that have CodeQL databases.
./bin/codeql-abom discover --org <name> --db abom.db

# Fetch, query and ingest them.
./bin/codeql-abom build --db abom.db

# Follow dependencies upstream, then refresh every aggregate.
./bin/codeql-abom expand-all --db abom.db --query ql/actions/abom/src/AbomEdges.ql

# Read the result.
./bin/codeql-abom report --db abom.db
./bin/codeql-abom serve --db abom.db --ui-dir <published-wwwroot>
```

`codeql-abom --help` lists every subcommand.

## Confirming what is yours

Most figures split into what you can change and what you cannot, once ABOM knows which accounts
are yours. It never asks you: it reads the answer from an authenticated
[`gh`](https://cli.github.com) session.

```bash
gh auth login                                   # once, with the read:org scope
./bin/codeql-abom accounts --db abom.db --refresh
```

An account counts because GitHub says you belong to it, never because it was typed in. That
matters because holding an analyzed repository proves nothing about who can change it: `actions`
owns `actions/cache`, which is in scope for many organizations and controlled by none of them.

Until an identity is verified, every figure is reported undivided — which is not a claim that
you own nothing. If `gh` is missing, logged out, or lacks `read:org`, the interface says so and
names the command that fixes it.

## Layout

| Path | |
| --- | --- |
| `cmd/codeql-abom` | The CLI |
| `internal/store` | SQLite. The only package that writes SQL |
| `internal/graph`, `ingest`, `expand` | Derivation from CodeQL results |
| `internal/serve` | HTTP API |
| `ui` | Blazor WebAssembly interface |
| `ql/actions/abom` | The CodeQL queries that produce the graph |

## Development

```bash
make verify      # corruption, gofmt, vet, lint, Go tests, browser tests, queries, interface
make selfcheck   # run our own analysis over our own workflows
```

Requires Go (see `go.mod`), .NET 10, Node, and the CodeQL CLI at the version in
`.codeql-version`.

## License

MIT. See [LICENSE](LICENSE).
