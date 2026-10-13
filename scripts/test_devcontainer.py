"""The Demo Codespace's configuration, and which GitHub credential its Copilot sign-in hands the Demo."""

import json
import os
from pathlib import Path
import re
import shlex
import shutil
import stat
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
DEVCONTAINER = ROOT / ".devcontainer"
CONFIG = DEVCONTAINER / "devcontainer.json"
LOGIN = DEVCONTAINER / "copilot-login.sh"
# Every file that installs or tells someone to install the Demo's tools.
INSTALLERS = [ROOT / "scripts" / "demo.sh", ROOT / "docs" / "demo.md"]


def devcontainer_files():
    return sorted(path for path in DEVCONTAINER.iterdir() if path.is_file()) if DEVCONTAINER.is_dir() else []


def shell_function(path, name):
    match = re.search(rf"^{name}\(\) \{{\n(.*?)^\}}", path.read_text(), re.M | re.S)
    return match.group(1) if match else ""


def commands(path):
    """The script's commands, one per line, with continued lines joined and comments dropped."""
    joined = re.sub(r"\\\n\s*", " ", path.read_text())
    return [line.strip() for line in joined.splitlines() if line.strip() and not line.lstrip().startswith("#")]


def tool(path, body):
    path.write_text(body)
    path.chmod(0o755)


class ConfigurationTests(unittest.TestCase):
    def config(self):
        return json.loads(CONFIG.read_text())

    # "Create codespace" builds only from the default location; a configuration anywhere else is
    # reachable only through "New with options", and the button gives everyone GitHub's generic image.
    def test_the_demo_is_the_repository_default_and_only_configuration(self):
        self.assertTrue(CONFIG.is_file(), "no .devcontainer/devcontainer.json; Codespaces would use its generic image")
        others = sorted(str(path.relative_to(ROOT)) for path in ROOT.glob(".devcontainer/*/devcontainer.json"))
        if (ROOT / ".devcontainer.json").exists():
            others.append(".devcontainer.json")
        self.assertEqual(others, [], "a second configuration makes the Demo one choice among several")

    def test_every_lifecycle_script_exists_and_is_executable(self):
        config = self.config()
        for key in ["onCreateCommand", "updateContentCommand", "postStartCommand", "postAttachCommand"]:
            with self.subTest(key=key):
                match = re.fullmatch(r"bash (\.devcontainer/[\w.-]+\.sh)", config.get(key, ""))
                self.assertIsNotNone(match, f"{key} is {config.get(key)!r}")
                script = ROOT / match.group(1)
                self.assertTrue(script.is_file(), f"{script} does not exist")
                self.assertTrue(os.access(script, os.X_OK), f"{script} is not executable")

    # Without gh, nobody in the Codespace can sign in to give Copilot a credential.
    def test_the_image_has_the_github_cli(self):
        features = self.config().get("features", {})
        self.assertTrue(any(name.startswith("ghcr.io/devcontainers/features/github-cli:") for name in features))

    # The base image lets vscode run commands as root only, and a lifecycle command cannot type a
    # password: without its own rule, every `sudo -u abom-demo` stopped the Codespace's setup.
    def test_the_image_lets_the_editor_run_commands_as_the_demo_user(self):
        users = {user for path in devcontainer_files() for user in re.findall(r"sudo -u ([\w-]+)", path.read_text())}
        self.assertEqual(users, {"abom-demo"})
        self.assertRegex((DEVCONTAINER / "Dockerfile").read_text(), r"vscode ALL=\(abom-demo\) NOPASSWD:")

    # The simulated GitHub and the egress proxy answer only inside the Codespace.
    def test_only_the_interface_port_is_forwarded(self):
        config = self.config()
        self.assertEqual(config["forwardPorts"], [8099])
        self.assertEqual(config["otherPortsAttributes"]["onAutoForward"], "ignore")
        self.assertIs(config["customizations"]["vscode"]["settings"]["remote.autoForwardPorts"], False)

    # A version written twice drifts: the image pinned CodeQL 2.27.1 after .codeql-version moved to
    # 2.27.2, and every Codespace creation stopped in onCreateCommand.
    def test_codeql_and_ql_mcp_versions_are_read_from_codeql_version(self):
        for path in devcontainer_files() + INSTALLERS:
            text = path.read_text()
            with self.subTest(path=str(path.relative_to(ROOT))):
                self.assertNotRegex(text, r"codeql-development-mcp-server@v?\d")
                self.assertNotRegex(text, r"CODEQL_VERSION=v?\d")
                self.assertNotRegex(text, r"codeql-cli-binaries/releases/(download|tag)/v\d")

    def test_every_copilot_cli_pin_names_one_version(self):
        pins = set()
        for path in devcontainer_files() + INSTALLERS:
            pins.update(re.findall(r"@github/copilot@([\w.-]+)", path.read_text()))
        self.assertEqual(len(pins), 1, f"the Copilot CLI is pinned at {sorted(pins)}")

    # Every run resolves ABOM's default findings suite, which imports codeql/actions-queries. The Demo's
    # user cannot download once its network is confined, so with nothing installed every run was refused.
    def test_the_demo_user_has_the_packs_of_the_default_findings_suite(self):
        pack = re.search(r'const DefaultSuite = "([^":]+):', (ROOT / "internal/runner/analyze.go").read_text()).group(1)
        home = next(path.parent.relative_to(ROOT) for path in ROOT.glob("ql/**/codeql-pack.yml")
                    if re.search(rf"^name: {re.escape(pack)}$", path.read_text(), re.M))
        script = (DEVCONTAINER / "update-content.sh").read_text()
        roots = re.findall(r"find (ql[\w/-]*) -name codeql-pack\.yml", script)
        self.assertTrue(any(home.is_relative_to(root) for root in roots), f"nothing installs {home} for the Demo")
        # From the lock: the Demo's user cannot write the checkout, and install rewrites its locks.
        self.assertRegex(script, r'as_demo codeql pack ci "\$')

    # A Codespace's container shares the VM's network, so the firewall's owner rule confines that
    # user ID across the whole VM, and a system ID could belong to one of the VM's own services.
    def test_the_demo_user_id_is_outside_the_range_system_services_use(self):
        match = re.search(r"useradd\b[^\n]*--uid (\d+)[^\n]*\babom-demo\b", (DEVCONTAINER / "Dockerfile").read_text())
        self.assertIsNotNone(match, "abom-demo is created without a chosen user ID")
        self.assertTrue(1000 < int(match.group(1)) < 60000, f"user ID {match.group(1)}")

    # The same shared network puts the VM's DNS resolver on loopback. Admitting loopback alone let
    # the Demo's user look up any name, and the lookup leaves the VM.
    def test_the_demo_user_can_look_up_no_name(self):
        confine = shell_function(DEVCONTAINER / "start-demo.sh", "confine")
        lines = confine.splitlines()
        lookups = [i for i, line in enumerate(lines) if "--dport 53" in line and "REJECT" in line]
        loopback = [i for i, line in enumerate(lines) if "-o lo -j ACCEPT" in line]
        self.assertTrue(lookups, "the firewall refuses no name lookup")
        self.assertTrue(loopback, "the firewall admits no loopback")
        self.assertLess(max(lookups), min(loopback), "loopback is admitted before lookups are refused")
        self.assertRegex(confine, r"\budp\b")
        self.assertRegex(confine, r"\btcp\b")
        self.assertIn("getent hosts", confine, "the proof never tries a lookup")

    # Otherwise the Go feature builds ten tools at their latest versions and pipes golangci-lint's
    # installer into a root shell, and the Node feature installs pnpm at its latest version with its
    # install scripts refused: minutes of each Codespace's creation, unpinned, for tools the Demo never
    # runs, and failures that leave a recovery container.
    def test_the_image_installs_no_extra_tools_at_their_latest_versions(self):
        features = self.config()["features"]
        self.assertIs(features["ghcr.io/devcontainers/features/go:1"].get("installGoTools"), False)
        self.assertEqual(features["ghcr.io/devcontainers/features/node:1"].get("pnpmVersion"), "none")

    # In a Codespace a lifecycle command that fails leaves a recovery container holding none of the
    # tools, so one dropped connection during creation cost the whole Codespace.
    def test_every_network_step_of_creation_is_retried(self):
        network = re.compile(r"\b(npm install|setup-packs|codeql pack|go mod download|make download|dotnet restore)\b")
        for name in ["on-create.sh", "update-content.sh"]:
            script = DEVCONTAINER / name
            with self.subTest(script=name):
                self.assertRegex(script.read_text(), r'\. "[^"\n]*/lib\.sh"', "retry comes from lib.sh")
                steps = [line for line in commands(script) if network.search(line)]
                self.assertTrue(steps, "no network step found")
                for step in steps:
                    self.assertTrue(step.startswith("retry "), step)
                for step in [line for line in commands(script) if re.search(r"\bcurl\b", line)]:
                    self.assertIn("--retry", step)

    # make builds with the toolchain the Makefile pins, which go downloads the first time it runs. A
    # download outside make fetched only the modules, so the build still reached the network unretried.
    def test_the_build_downloads_nothing_that_was_not_retried(self):
        lines = commands(DEVCONTAINER / "update-content.sh")
        downloads = [i for i, line in enumerate(lines) if line == "retry make download"]
        builds = [i for i, line in enumerate(lines) if line == "make build"]
        self.assertTrue(downloads, "nothing fetches the pinned toolchain under retry")
        self.assertTrue(builds)
        self.assertLess(downloads[0], builds[0])
        self.assertRegex((ROOT / "Makefile").read_text(), r"(?m)^download:\n\t@?go mod download\b")

    # A Codespace runs lifecycle commands in a terminal, so tools draw progress and terminal control
    # codes into the creation log: .NET's redrew itself into 3,500 lines there, the log a recovery
    # container leaves to read.
    def test_creation_writes_no_terminal_noise_into_its_log(self):
        update = (DEVCONTAINER / "update-content.sh").read_text()
        self.assertRegex(update, r"(?m)^export MSBUILDTERMINALLOGGER=off\b")
        self.assertRegex(update, r"(?m)^export DOTNET_NOLOGO=1\b")
        # The first use of an SDK checks workloads ABOM does not use, and says so when it cannot.
        self.assertRegex(update, r"(?m)^export DOTNET_SKIP_WORKLOAD_INTEGRITY_CHECK=true\b")
        # Each .NET process puts a terminal that has a keypad mode into it, writing ESC[?1h ESC= to its
        # output whatever its input is; a dumb terminal has no such mode.
        dumb = re.search(r"(?m)^export TERM=dumb$", update)
        self.assertIsNotNone(dumb, "update-content.sh does not run with a dumb terminal")
        first_dotnet = re.search(r"(?m)^[^#\n]*(\bdotnet\b|\bmake publish\b)", update)
        self.assertIsNotNone(first_dotnet, "no .NET step found")
        self.assertLess(dumb.start(), first_dotnet.start())
        for step in [line for line in commands(DEVCONTAINER / "on-create.sh") if "npm install" in line]:
            self.assertIn("--no-progress", step)

    # A prebuild runs these before any user or Codespace exists: whatever they read from one would be
    # absent there, or worse, the prebuild's own, baked into every Codespace made from it.
    def test_prebuild_steps_need_nothing_only_a_codespace_has(self):
        codespace_only = re.compile(r"CODESPACE_NAME|GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN|GITHUB_TOKEN|GH_TOKEN|"
                                    r"GITHUB_USER|copilot-login|gh auth")
        for name in ["on-create.sh", "update-content.sh", "lib.sh"]:
            with self.subTest(script=name):
                self.assertEqual(codespace_only.findall((DEVCONTAINER / name).read_text()), [])

    # A Codespace stop clears the Demo's directory, so each start gives the new Demo the sign-in again.
    def test_each_start_gives_the_demo_an_existing_sign_in(self):
        self.assertIn("copilot-login.sh\" --if-signed-in", (DEVCONTAINER / "start-demo.sh").read_text())

    # A session spends its user's AI credits, and one nobody limited spends them until its time limit
    # runs out. Whoever opens a Codespace may never have read about either.
    def test_a_codespace_demo_limits_what_one_session_may_spend(self):
        default = re.search(r"(?m)^credits=\$\{ABOM_DEMO_CREDITS:-(\d+)\}$", (DEVCONTAINER / "start-demo.sh").read_text())
        self.assertIsNotNone(default, "the start no longer reads ABOM_DEMO_CREDITS with a default")
        # Copilot refuses a limit below 30 AI credits, so a lower default would refuse every session.
        self.assertGreaterEqual(int(default.group(1)), 30)

    # ABOM's default model is offered on some Copilot plans only, and on any other every session fails
    # as it starts, in a Codespace whose user may be on any plan.
    def test_a_codespace_demo_runs_the_model_it_is_told_to(self):
        script = (DEVCONTAINER / "start-demo.sh").read_text()
        self.assertRegex(script, r"(?m)^model=\$\{ABOM_DEMO_MODEL:-\}$")
        self.assertRegex(script, r'--authoring-model "\$model"')

    # The Demo's user reads what the start writes, such as the egress proxy's certificate. In a
    # Codespace it could not, so whoever starts the script must not decide that through their umask.
    def test_what_the_start_writes_is_readable_by_the_demo_user_whoever_starts_it(self):
        lines = (DEVCONTAINER / "start-demo.sh").read_text().splitlines()
        umask = [i for i, line in enumerate(lines) if line.strip() == "umask 022"]
        logs = [i for i, line in enumerate(lines) if line.strip() == 'mkdir -p "$logs"']
        self.assertTrue(umask, "the start leaves its umask to whoever runs it")
        self.assertTrue(logs, "the start no longer makes its log directory")
        self.assertLess(umask[0], logs[0], "the start writes before it sets its umask")
        self.assertIn('chmod 0755 "$logs"', lines, "a directory an earlier start made keeps its mode")
        # A default ACL on the directory decides a new file's mode instead of the umask.
        self.assertRegex("\n".join(lines), r'chmod 0644 "\$ca"', "the certificate's mode is left to inheritance")

    # demo serve refuses to start over a certificate its user cannot read, so a fault on the authoring
    # path took the whole Demo down rather than leaving authoring off.
    def test_authoring_is_offered_only_once_the_demo_user_reads_the_certificate(self):
        script = (DEVCONTAINER / "start-demo.sh").read_text()
        self.assertRegex(script, r"readable_by_demo\(\) \{[^}]*\bas_demo\b[^}]*CERTIFICATE[^}]*\}")
        lines = script.splitlines()
        checks = [i for i, line in enumerate(lines)
                  if 'readable_by_demo "$ca"' in line and line.lstrip().startswith(("if ", "elif "))]
        offers = [i for i, line in enumerate(lines) if "authoring=(--copilot-cli" in line]
        self.assertTrue(checks, "nothing checks the certificate as the Demo's user before offering authoring")
        self.assertTrue(offers)
        self.assertLess(checks[0], offers[0])

    # demo serve that refuses its arguments exits at once, and the start still waited out its 30
    # seconds before saying so.
    def test_a_demo_that_exits_is_reported_as_soon_as_it_exits(self):
        script = (DEVCONTAINER / "start-demo.sh").read_text()
        wait = re.search(r"(?ms)^for _ in \$\(seq 1 30\); do\n(.*?)^done$", script)
        self.assertIsNotNone(wait, "the start no longer waits for the Demo to answer")
        self.assertIn('/proc/$serve', wait.group(1), "the wait never looks at whether the Demo is still running")
        # Something else holding the port can accept a connection and never answer.
        self.assertRegex(wait.group(1), r"curl [^\n]*--max-time \d", "one request can outlast the whole wait")


class CopilotLoginTests(unittest.TestCase):
    """copilot-login.sh hands the Demo the user's own GitHub sign-in, never the Codespace's token."""

    def setUp(self):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        base = Path(scratch.name)
        self.root = base / "demo"
        (self.root / "secrets").mkdir(parents=True)
        (self.root / "demo.json").write_text("{}\n")
        self.calls = base / "gh-calls.txt"
        self.stored = base / "gh-stored-login"
        self.unauthorized = base / "gh-unauthorized"
        self.opened = base / "browser-opened.txt"
        tools = base / "bin"
        tools.mkdir()
        # gh as it behaves: an exported token answers before a stored login, and login refuses
        # while GITHUB_TOKEN is exported, which is how a Codespace starts. Its device flow says what
        # gh 2.102.0 says on Linux, where it tries a clipboard unless told not to, then waits.
        self.tool(tools / "gh", f"""#!/bin/sh
printf '%s|GITHUB_TOKEN=%s|GH_TOKEN=%s|PROMPTS=%s\\n' "$*" "${{GITHUB_TOKEN-unset}}" \\
    "${{GH_TOKEN-unset}}" "${{GH_PROMPT_DISABLED-unset}}" >> '{self.calls}'
case "$1 $2" in
"auth token")
    if [ -n "${{GH_TOKEN-}}" ]; then echo "$GH_TOKEN"; exit 0; fi
    if [ -n "${{GITHUB_TOKEN-}}" ]; then echo "$GITHUB_TOKEN"; exit 0; fi
    if [ -s '{self.stored}' ]; then cat '{self.stored}'; exit 0; fi
    echo "no oauth token found for github.com" >&2; exit 1 ;;
"auth login")
    if [ -n "${{GITHUB_TOKEN-}}" ]; then echo "GITHUB_TOKEN is being used for authentication" >&2; exit 1; fi
    echo >&2
    case " $* " in
    *" --clipboard=false "*) ;;
    *) printf '! Failed to copy one-time code to clipboard\\n  No clipboard utilities available.\\n' >&2 ;;
    esac
    echo '! First copy your one-time code: C649-B33F' >&2
    echo 'Open this URL to continue in your web browser: https://github.com/login/device' >&2
    sleep 0.3
    if [ -f '{self.unauthorized}' ]; then echo 'error: the one-time code expired' >&2; exit 1; fi
    echo gho_signedin > '{self.stored}'
    printf '✓ Authentication complete.\\n✓ Logged in as octocat\\n' >&2
    exit 0 ;;
esac
exit 2
""")
        # The browser a VS Code terminal names in $BROWSER: in a Codespace it opens the page in the
        # browser running the Codespace.
        self.browser = tools / "browser"
        self.tool(self.browser, f"""#!/bin/sh
printf '%s\\n' "$*" >> '{self.opened}'
""")
        # sudo as this user: the test cannot switch to the Demo's.
        self.tool(tools / "sudo", """#!/bin/sh
while [ "$#" -gt 0 ]; do
    case "$1" in
    -u) shift 2 ;;
    -H|-n) shift ;;
    *) break ;;
    esac
done
exec "$@"
""")
        self.environment = dict(os.environ, PATH=f"{tools}{os.pathsep}{os.environ['PATH']}",
                                ABOM_DEMO_ROOT=str(self.root), GITHUB_TOKEN="ghu_codespace")
        self.environment.pop("GH_TOKEN", None)
        # Run from a VS Code terminal, the tests would open real pages.
        self.environment.pop("BROWSER", None)

    @staticmethod
    def tool(path, body):
        path.write_text(body)
        path.chmod(0o755)

    def login(self, *args, stdin=""):
        return subprocess.run(["bash", str(LOGIN), *args], input=stdin, env=self.environment,
                              capture_output=True, text=True, timeout=30, check=False)

    def saved(self):
        token = self.root / "secrets" / "copilot-token"
        return token.read_text().strip() if token.exists() else None

    def gh_calls(self):
        return self.calls.read_text().splitlines() if self.calls.exists() else []

    def pages_opened(self):
        return self.opened.read_text().splitlines() if self.opened.exists() else []

    # Copying a page address and a code out of a terminal was the whole sign-in, and the address was
    # the half a terminal can open itself.
    def test_without_a_sign_in_it_shows_the_code_then_opens_the_sign_in_page_in_the_terminals_browser(self):
        self.environment["BROWSER"] = str(self.browser)
        result = self.login(stdin="\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("C649-B33F", result.stdout)
        self.assertEqual(self.pages_opened(), ["https://github.com/login/device"])
        self.assertIn("octocat", result.stdout)
        self.assertEqual(self.saved(), "gho_signedin")

    def test_without_a_browser_it_names_the_page_and_the_code(self):
        result = self.login()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("https://github.com/login/device", result.stdout)
        self.assertIn("C649-B33F", result.stdout)
        self.assertEqual(self.pages_opened(), [])

    # gh 2.102.0 copies the code to a clipboard by default, and a Codespace has none that reaches the
    # browser, so every sign-in began with a failure and advice to install one.
    def test_a_sign_in_mentions_no_clipboard(self):
        self.environment["BROWSER"] = str(self.browser)
        result = self.login(stdin="\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("clipboard", (result.stdout + result.stderr).lower())

    def test_a_sign_in_that_is_not_completed_saves_nothing_and_says_why(self):
        self.unauthorized.touch()
        result = self.login(stdin="\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("expired", result.stderr)
        self.assertIsNone(self.saved())

    def test_an_existing_sign_in_is_saved_and_the_codespace_token_is_not(self):
        self.stored.write_text("gho_user\n")
        result = self.login()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.saved(), "gho_user")
        self.assertTrue(self.gh_calls())
        for call in self.gh_calls():
            self.assertIn("|GITHUB_TOKEN=unset|GH_TOKEN=unset|", call)

    def test_without_a_sign_in_it_signs_in_through_the_browser_with_the_codespace_token_unset(self):
        result = self.login()
        self.assertEqual(result.returncode, 0, result.stderr)
        logins = [call for call in self.gh_calls() if call.startswith("auth login")]
        self.assertEqual(len(logins), 1, self.gh_calls())
        self.assertIn("--web", logins[0])
        self.assertIn("|GITHUB_TOKEN=unset|GH_TOKEN=unset|PROMPTS=1", logins[0])
        self.assertEqual(self.saved(), "gho_signedin")

    def test_the_saved_credential_is_readable_only_by_its_owner(self):
        self.stored.write_text("gho_user\n")
        self.assertEqual(self.login().returncode, 0)
        mode = stat.S_IMODE((self.root / "secrets" / "copilot-token").stat().st_mode)
        self.assertEqual(mode, 0o600)

    # A start must never stop to ask: without a sign-in it leaves authoring unavailable, as before.
    def test_if_signed_in_saves_an_existing_sign_in_and_never_asks_for_one(self):
        result = self.login("--if-signed-in")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIsNone(self.saved())
        self.assertFalse([call for call in self.gh_calls() if call.startswith("auth login")])
        self.stored.write_text("gho_user\n")
        self.assertEqual(self.login("--if-signed-in").returncode, 0)
        self.assertEqual(self.saved(), "gho_user")

    def test_a_pasted_fine_grained_token_is_saved(self):
        result = self.login("--token", stdin="github_pat_finegrained\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.saved(), "github_pat_finegrained")
        self.assertFalse(self.gh_calls())

    # Copilot refuses classic tokens, so saving one would only fail later, inside a session.
    def test_a_classic_token_is_refused_wherever_it_comes_from(self):
        pasted = self.login("--token", stdin="ghp_classic\n")
        self.stored.write_text("ghp_classic\n")
        signed_in = self.login()
        for result in [pasted, signed_in]:
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("classic", result.stderr)
        self.assertIsNone(self.saved())

    def test_nothing_is_saved_without_a_running_demo(self):
        (self.root / "demo.json").unlink()
        self.stored.write_text("gho_user\n")
        result = self.login()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("No Demo", result.stderr)
        self.assertIsNone(self.saved())


class RetryTests(unittest.TestCase):
    """retry, from lib.sh: what creating a Codespace does with a step that reaches the network."""

    def attempt(self, failures):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        count = Path(scratch.name) / "attempts"
        script = f"""
. {shlex.quote(str(DEVCONTAINER / "lib.sh"))}
download() {{
    n=$(cat {shlex.quote(str(count))} 2>/dev/null || echo 0)
    echo $((n + 1)) > {shlex.quote(str(count))}
    [ "$n" -ge {failures} ]
}}
retry download
"""
        result = subprocess.run(["bash", "-c", script], env=dict(os.environ, ABOM_RETRY_DELAY="0"),
                                capture_output=True, text=True, timeout=30, check=False)
        return result, int(count.read_text())

    def test_a_step_that_fails_and_then_succeeds_succeeds(self):
        result, attempts = self.attempt(failures=2)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(attempts, 3)

    def test_a_step_that_keeps_failing_fails_after_four_attempts_and_names_itself(self):
        result, attempts = self.attempt(failures=10)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(attempts, 4)
        self.assertIn("download", result.stderr)


class StatusTests(unittest.TestCase):
    """status.sh, which each attach runs: whether a browser can use the Demo yet, and if not, why."""

    def setUp(self):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        tools = Path(scratch.name)
        self.logs = tools / "logs"
        self.logs.mkdir()
        # The Demo as curl reaches it: loopback is admitted, and the forwarded address as told.
        tool(tools / "curl", """#!/bin/sh
[ -z "${DEMO_DOWN:-}" ] || exit 7
case "$*" in
*"Host: "*) printf '%s' "$FORWARDED_ANSWER" ;;
*/api/instance*) printf '%s' '{"mode":"demo","authoring":"copilot"}' ;;
*/api/authoring/readiness*) printf '%s' '{"ready":true}' ;;
*) exit 22 ;;
esac
""")
        self.environment = dict(os.environ, PATH=f"{tools}{os.pathsep}{os.environ['PATH']}", CODESPACES="true",
                                CODESPACE_NAME="fluffy-space", ABOM_DEMO_LOGS=str(self.logs),
                                GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN="app.github.dev")

    def status(self, forwarded_answer, **environment):
        return subprocess.run(["bash", str(DEVCONTAINER / "status.sh")],
                              env=dict(self.environment, FORWARDED_ANSWER=forwarded_answer, **environment),
                              capture_output=True, text=True, timeout=30, check=False)

    # The Ports panel is one more thing to find; the terminal can print the address itself. Codespaces
    # masks the Codespace's name in some output, so the line names the port's panel entry as well.
    def test_in_a_codespace_it_names_the_address_to_open(self):
        result = self.status("200")
        self.assertEqual(result.returncode, 0, result.stderr)
        address = [line for line in result.stdout.splitlines() if line.startswith("ABOM Demo: https:")]
        self.assertEqual(len(address), 1, result.stdout)
        self.assertIn("https://fluffy-space-8099.app.github.dev/", address[0])
        self.assertIn("Ports panel", address[0])

    # "See serve.log" sent people to read a file for one line: the reason the Demo stopped.
    def test_a_demo_that_is_not_answering_shows_why_from_its_log(self):
        (self.logs / "serve.log").write_text("serve: listening\ndemo serve: egress-ca.pem holds no certificate\n")
        result = self.status("200", DEMO_DOWN="1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("not answering", result.stdout)
        self.assertIn("demo serve: egress-ca.pem holds no certificate", result.stdout)
        self.assertNotIn("serve: listening", result.stdout)

    # The Demo admits loopback and the one forwarded address it was started with. Started where the
    # environment named none, it refuses every request the browser makes.
    def test_a_demo_that_refuses_its_forwarded_address_says_how_to_restart_it(self):
        result = self.status("403")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("fluffy-space-8099.app.github.dev", result.stdout)
        self.assertIn(".devcontainer/start-demo.sh", result.stdout)

    def test_a_demo_that_admits_its_forwarded_address_says_only_whether_authoring_is_ready(self):
        result = self.status("200")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("refuses", result.stdout)
        self.assertIn("Copilot authoring: ready.", result.stdout)

    def test_outside_a_codespace_no_forwarded_address_is_checked(self):
        for name in ["CODESPACES", "CODESPACE_NAME", "GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN"]:
            self.environment.pop(name)
        result = self.status("403")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("refuses", result.stdout)


class PostStartTests(unittest.TestCase):
    """post-start.sh, which each start of a Codespace runs."""

    # A postStartCommand that fails makes Codespaces skip postAttachCommand, so the status that says
    # what went wrong and what to run never reached whoever was waiting for the Demo.
    def test_a_demo_that_fails_to_start_does_not_fail_the_codespace_start(self):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        here = Path(scratch.name)
        shutil.copy(DEVCONTAINER / "post-start.sh", here / "post-start.sh")
        tool(here / "start-demo.sh", '#!/bin/sh\necho "start-demo $*"\nexit 1\n')
        result = subprocess.run(["bash", str(here / "post-start.sh")], capture_output=True, text=True,
                                timeout=30, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("start-demo --fresh", result.stdout)


if __name__ == "__main__":
    unittest.main()
