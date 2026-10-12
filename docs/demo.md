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

## Run a Demo from an archive of this repository

These steps take a downloaded archive (for example **Code → Download ZIP**) to a running Demo. A
clone works the same way. Nothing here needs a Git history or a GitHub organization.

### 1. Install the prerequisites

| Tool | macOS (Homebrew) | Linux |
| --- | --- | --- |
| C compiler, `make`, `git`, `curl` | `xcode-select --install` | `sudo apt-get install build-essential git curl` |
| Go 1.21 or later (`make` fetches the pinned 1.26 toolchain) | `brew install go` | [go.dev/dl](https://go.dev/dl/) |
| .NET 10 SDK | `brew install --cask dotnet-sdk` | [dotnet.microsoft.com/download](https://dotnet.microsoft.com/download) |
| Graphviz | `brew install graphviz` | `sudo apt-get install graphviz` |
| Node.js 24.15 or later, for Copilot | `brew install node` | [nodejs.org](https://nodejs.org/) |

Install the CodeQL CLI with the [GitHub CLI](https://cli.github.com/)'s `gh codeql` extension
(`brew install gh`, or see its install page for Linux):

```bash
gh auth login                                   # once; the extension downloads CodeQL from GitHub
gh extension install github/gh-codeql
gh codeql set-version v2.27.2
mkdir -p "$HOME/.local/bin"
gh codeql install-stub "$HOME/.local/bin"       # puts `codeql` on PATH
export PATH="$HOME/.local/bin:$PATH"            # add this line to your shell profile too
codeql version --format=terse                   # prints 2.27.2
```

Any patch release of the minor version in [`.codeql-version`](../.codeql-version) (`v2.27.x`, at
least that file's) is accepted. The Demo runs the CLI behind the `codeql` stub directly, so `gh`
needs no credential while it serves. On Linux arm64, which the extension does not download, unzip
`codeql-linux-arm64.zip` from the
[CodeQL CLI releases](https://github.com/github/codeql-cli-binaries/releases/tag/v2.27.2) and put
its `codeql` directory on `PATH`.

For Copilot authoring, install the Copilot CLI and the CodeQL Development MCP Server:

```bash
npm install --global @github/copilot@1.0.88 codeql-development-mcp-server@2.27.1
```

### 2. Check, build and start

From the extracted directory:

```bash
cd codeql-abom
make demo-check      # lists each prerequisite, and what to install where one is missing
make demo            # builds, creates the Demo the first time, and serves it
```

Open <http://127.0.0.1:8090>. The first `make demo` downloads Go modules, .NET packages and CodeQL
query packs, so it needs the network and takes several minutes; it creates the Demo under
`.abom/demo`. Later runs reuse it. `Ctrl-C` stops the Demo.

### 3. Copilot's credential

`make demo` gives Copilot your GitHub CLI sign-in (`gh auth token`) when no other credential is saved,
so `gh auth login` is all most people need. To use another, run `make demo-login` in a second
terminal and paste a fine-grained token with the **Copilot Requests** permission; classic `ghp_`
tokens are refused. The credential is saved under `.abom/demo/secrets`, readable only by you, and
given only to the Copilot CLI.

### 4. Practice

- **Tutorials:** Home lists the six lessons; the query lessons end by offering to practice for real.
- **Request a rule:** CodeQL → Requests → New request, then start an authoring session on it.
- **Run queries over the inventory:** Inventory → Repositories or Actions → Run queries, choose
  rows and a query version, then Run. Results are real CodeQL results over the fictional
  repositories. An upstream Action such as `actions/checkout` is listed but not held by the
  simulated GitHub, so choosing one offers **Use the repositories that use it**, which is where an
  Actions query looks.

`make demo-reset` starts over with a new dataset (stop the Demo first). It also discards the saved
credential and Tutorial progress. Settings: `ABOM_DEMO_DIR` (default `.abom/demo`), `ABOM_DEMO_ADDR`
(default `127.0.0.1:8090`) and `ABOM_DEMO_CREDITS`, the AI credits one session may spend (default
`0`, no limit).

### What reaches the network

- During `make demo`'s first run: Go, NuGet and the CodeQL package registry.
- While serving: the Demo server reaches loopback only. Copilot sessions reach the network through
  `codeql-abom demo egress`. It tunnels to Copilot's hosts and reads `api.github.com`, forwarding
  only `GET` and `HEAD` under `/copilot_internal/`, where Copilot checks its credential. Everything
  else is refused and logged to `.abom/demo/egress.log`. To read that one host, the proxy presents
  a certificate from an authority made each time it starts. The authority's key never leaves the
  proxy, it lasts a day, and it can sign only `api.github.com`; only the Copilot CLI is told to
  trust it.
- On a workstation nothing stops another program from ignoring the proxy, so the server's own
  refusals and the empty environment are the boundary there. The Codespace adds a firewall.

## In a Codespace (not yet qualified)

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
- Copilot sessions reach the network only through `codeql-abom demo egress`, described above.
  Without that proxy, authoring is unavailable.
- The server refuses every control a practice exercise does not need, including any added
  later, and every download.

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
