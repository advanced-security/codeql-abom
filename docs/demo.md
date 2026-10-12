# ABOM Demo

A Demo is an ABOM instance over practice data: fictional repositories on a simulated GitHub,
real CodeQL databases and results built from them, and, where it can be confined, real Copilot
authoring. Use it to take the [tutorials](tutorials.md) and then practice the same steps for
real without touching an organization's repositories.

Every page carries a **Demo — synthetic training data** banner. It cannot be dismissed, because
Demo is a fact about the server, not a setting. Its facts say what is real and what is simulated:

| | In a Demo |
| --- | --- |
| Repositories and their owners | Fictional, served by a simulated GitHub on loopback |
| CodeQL databases, query tests and results | Real: built and evaluated by the CodeQL CLI over those repositories |
| Authoring sessions | Real Copilot, when the Demo is started with it and confined; otherwise unavailable, with the reason |
| Your identity | `demo-practitioner`, a fictional administrator of every fictional account |
| Discovery, code scanning, dependency submissions, webhooks | Not offered |
| Downloads and exports | Not offered: a Demo never helps share what you changed in it |

## In a Codespace

The `.devcontainer/demo` configuration builds a disposable Demo Codespace: create one from this
repository with **that** dev container, and every start of it begins a new dataset under
`/tmp/abom-demo`. Stopping the Codespace discards the exercise; a rebuild discards it too.

```text
gh codespace create --repo advanced-security/codeql-abom --branch <branch> \
  --devcontainer-path .devcontainer/demo/devcontainer.json --retention-period 0h
```

The zero retention period deletes the Codespace once it stops. A Codespace created from the
browser keeps its account's default retention unless an organization policy sets it.

Open the forwarded **ABOM Demo** port (8099) from the Ports panel. It is private to you; the
simulated GitHub is never forwarded.

To use Copilot authoring, supply a credential after the Codespace starts:

```text
.devcontainer/demo/copilot-login.sh
```

It asks for a fine-grained personal access token with the **Copilot Requests** permission, keeps
it only in the Demo's private directory, and hands it only to the Copilot CLI. The repository's
automatic Codespaces token is not used. Revoke the token on GitHub when you are done.

`.devcontainer/demo/start-demo.sh` restarts the Demo and keeps the current exercise;
`start-demo.sh --fresh` discards it. Logs are under `/tmp/abom-demo-logs`.

### What keeps a Demo away from GitHub

- The server is composed only of local backends: the marked database, the CodeQL CLI, and the
  simulated GitHub. No GitHub reader, App, token lookup, discovery loop, webhook or relay is built.
- Its environment holds no token, proxy or credential helper, and its own HTTP clients refuse
  anything but loopback.
- In a Codespace it runs as its own user, and a firewall limits that user to loopback. The start
  script proves it by trying to reach `api.github.com` and refusing to enable Copilot if it can.
- Copilot sessions reach the network only through `codeql-abom demo egress`, a proxy that opens
  tunnels to Copilot's hosts and refuses, and logs, every other host. Without that proxy,
  authoring is unavailable.
- The server refuses every control a practice exercise does not need, including any added
  later, and every download.

## On a workstation

```text
make build publish
bin/codeql-abom demo init --dir ~/abom-demo
bin/codeql-abom demo serve --dir ~/abom-demo --ui-dir .abom/ui/wwwroot --codeql /path/to/codeql
```

`demo init` builds the dataset from `testdata/corpus` in this checkout and refuses any directory
that is neither empty nor already a Demo; `--replace` starts a Demo directory over with a new
dataset. `demo serve` takes its database, query pack and work directories from that directory and
refuses the flags of an ordinary server. Name the CodeQL CLI itself, not a `gh codeql` shim: a
Demo gives `gh` no credential to use.

Copilot authoring needs `--copilot-cli`, an installed `--ql-mcp` (a Demo never runs `npx`), and
`--egress-proxy` pointing at a running `codeql-abom demo egress`. On a workstation nothing
confines the server's children to that proxy, so prefer the Codespace for authoring.

## Leaving Demo

A Demo is not switched off from the interface. Stop the server and start ABOM on your own
database. An ordinary `serve`, and every other command, refuses a Demo database, and `demo serve`
refuses an ordinary one.

## Tutorials in a Demo

Tutorials work exactly as elsewhere, and their progress is kept per Demo dataset, so a new Demo
starts with nothing marked done. At the end of **Request a new detection rule** and **Fix false
positives or false negatives**, a Demo offers to practice the same steps for real.

## Not yet in a Demo

- Comparing a run with code scanning: fictional repositories have never been scanned, and a
  simulated comparison must not satisfy a review gate. Runs and their results work.
- Linking an example from a repository; paste examples instead.
- A scripted, offline authoring profile for workstations without Copilot.
