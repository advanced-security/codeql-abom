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

## Run a Demo in a Codespace

[![Open in GitHub Codespaces](https://github.com/codespaces/badge.svg)](https://codespaces.new/advanced-security/codeql-abom?quickstart=1)

The repository's dev container is the Demo, so **Code → Codespaces → Create codespace on main**
or the link above starts one. Without a prebuild, creating it builds the image, installs CodeQL,
the Copilot CLI and ql-mcp, then builds ABOM, which takes about seven minutes. Each start of the
Codespace starts a Demo over a new dataset under `/tmp/abom-demo`. Each time you attach, the
terminal prints the Demo's address, which is the forwarded **ABOM Demo** port (8099) in the Ports
panel, and says whether Copilot authoring is ready. The address is private to you, and the
simulated GitHub is never forwarded. If the Demo did not start, the terminal says so instead,
with the last line of its log.

If the Codespace opens in **recovery mode**, its setup stopped, and the recovery container has none
of ABOM's tools. **View Creation Log** shows why: the last `on-create:` or `update-content:` line
names the step, and a step that downloads has already been tried four times. **Codespaces: Rebuild
Container**, from the Command Palette, runs the setup again.

### Sign in for Copilot authoring

Copilot authoring uses your own GitHub sign-in, never the Codespace's `GITHUB_TOKEN`, which belongs
to this repository rather than to you. In the Codespace's terminal, run:

```bash
.devcontainer/copilot-login.sh
```

It signs the GitHub CLI in with a one-time code you enter in your browser, then gives that
sign-in to the Demo. `gh` keeps the sign-in in your Codespace, so later starts give it to each new
Demo without asking again. Deleting the Codespace deletes that copy, but does not revoke the
sign-in. For a credential you can revoke on its own, `.devcontainer/copilot-login.sh --token` takes
a fine-grained token with only the **Copilot Requests** permission; delete it on GitHub when you are
done. Classic `ghp_` tokens are refused.

### Stopping, restarting and deleting

Stopping the Codespace discards the exercise, and the next start begins a new dataset.
`.devcontainer/start-demo.sh` restarts the Demo without discarding it, `--fresh` discards it, and
`--stop` stops it; it reads `ABOM_DEMO_CREDITS` as `make demo` does. If the browser is refused,
restart it from a terminal: the Demo admits only the forwarded address the start named.
`.devcontainer/status.sh` prints what each attach does. Logs are under `/tmp/abom-demo-logs`. To
have the Codespace deleted as soon as it stops, create it with a zero retention period; one created
from the browser keeps your account's default retention unless an organization policy sets one:

```bash
gh codespace create --repo advanced-security/codeql-abom --retention-period 0h
```

The same Codespace has what building ABOM needs, so contributors can use it too. `make start` will
not run beside the Demo's server, so stop the Demo first with `.devcontainer/start-demo.sh --stop`.
`make lint` needs golangci-lint, which the image leaves out: `go install` the version
[`copilot-setup-steps.yml`](../.github/workflows/copilot-setup-steps.yml) names. There is no
Docker, so `make test-postgres` runs in CI rather than there.

### What keeps a Demo away from GitHub

- The server is composed only of local backends: the marked database, the CodeQL CLI, and the
  simulated GitHub. No GitHub reader, App, token lookup, discovery loop, webhook or relay is built.
- Its environment holds no token, proxy or credential helper, and its own HTTP clients refuse
  anything but loopback.
- In a Codespace it runs as its own user, and a firewall limits that user to loopback and refuses
  its name lookups: a Codespace's container shares its VM's network, where loopback also holds the
  VM's resolver. The start script proves both by trying them, and leaves Copilot off if either
  works.
- Copilot sessions reach the network only through `codeql-abom demo egress`, described in
  [What reaches the network](#what-reaches-the-network).
  Without that proxy, authoring is unavailable.
- The server refuses every control a practice exercise does not need, including any added
  later, and every download.

### Setting up prebuilds (repository administrators)

A prebuild does that work ahead of time, so creating a Codespace does not wait for it. Prebuilds
run as GitHub Actions workflows, so Actions must be enabled for the repository, and an organization
needs a GitHub Team or Enterprise plan with a Codespaces payment method and spending limit.

1. In the repository's **Settings → Codespaces**, under "Prebuild configuration", click **Set up
   prebuild**.
2. Choose the `main` branch and the `.devcontainer/devcontainer.json` configuration.
3. Choose **Every push**. The prebuild builds ABOM from the branch, and **On configuration change**
   would rebuild only when `devcontainer.json` or the `Dockerfile` changes, leaving the prebuilt
   ABOM behind the code. **Scheduled** costs less and lags by up to its interval.
4. Under **Reduce prebuild available to only specific regions**, choose the regions your users
   create Codespaces in: each region's prebuild is billed for storage.
5. Keep **Template history** at its default of 2, add whoever should hear about failed prebuilds,
   and click **Create**. Follow the first prebuild in the repository's **Actions** tab.

A prebuild contains the tools, ABOM and every query pack a Demo resolves, and never a user's
credential or exercise. For Codespaces the organization pays for, set policies that restrict port
visibility to private and set the maximum retention period. Organization policies do not cover
Codespaces a user pays for, so they need the zero-retention command above.

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
`codeql-linux-arm64.zip` from the [CodeQL CLI release](https://github.com/github/codeql-cli-binaries/releases)
that `.codeql-version` names, and put its `codeql` directory on `PATH`.

For Copilot authoring, install the Copilot CLI and the CodeQL Development MCP Server release that
matches `.codeql-version`, from the extracted directory:

```bash
npm install --global @github/copilot@1.0.88 "codeql-development-mcp-server@$(cut -c2- .codeql-version)"
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
`0`, no limit; Copilot refuses a limit below 30).

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
