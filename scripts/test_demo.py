"""scripts/demo.sh: what a Demo from a checkout or an archive needs, and what it runs."""

import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
DEMO = ROOT / "scripts" / "demo.sh"


class CheckTests(unittest.TestCase):
    """demo.sh check, which make demo-check runs."""

    def check(self, qlmcp_release):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        tools = Path(scratch.name) / "bin"
        tools.mkdir()
        # ql-mcp says its release only as it starts, and stops when its input closes.
        qlmcp = tools / "codeql-development-mcp-server"
        qlmcp.write_text("#!/bin/sh\n"
                         f'echo "[INFO] Starting CodeQL Development MCP McpServer v{qlmcp_release} in stdio mode" >&2\n')
        qlmcp.chmod(0o755)
        environment = dict(os.environ, PATH=f"{tools}{os.pathsep}{os.environ['PATH']}",
                           ABOM_DEMO_DIR=str(Path(scratch.name) / "demo"))
        result = subprocess.run(["bash", str(DEMO), "check"], cwd=ROOT, env=environment,
                                capture_output=True, text=True, timeout=120, check=False)
        lines = [line for line in result.stdout.splitlines() if "codeql-development-mcp-server" in line]
        self.assertEqual(len(lines), 1, result.stdout + result.stderr)
        return lines[0]

    # ql-mcp 2.25.2 writes an extension for a predicate its own PrintAST pack does not declare, so every
    # PrintAST failed, and no session could change its query. The check had passed it as installed.
    def test_a_ql_mcp_from_another_codeql_release_is_reported_rather_than_accepted(self):
        line = self.check("2.25.2")
        self.assertTrue(line.lstrip().startswith("MISSING"), line)
        self.assertIn("2.25.2", line)

    def test_the_ql_mcp_release_matching_codeql_is_accepted(self):
        pin = (ROOT / ".codeql-version").read_text().strip().lstrip("v")
        line = self.check(pin)
        self.assertTrue(line.lstrip().startswith("ok"), line)


class ServeTests(unittest.TestCase):
    """demo.sh serve, which make demo runs."""

    # ABOM's default model is offered on some Copilot plans only, and on any other every session
    # fails as it starts.
    def test_the_demo_runs_the_model_it_is_told_to(self):
        script = DEMO.read_text()
        self.assertRegex(script, r"(?m)^model=\$\{ABOM_DEMO_MODEL:-\}$")
        self.assertRegex(script, r'--authoring-model "\$model"')

    # A ql-mcp from another release would start, and fail at its first PrintAST inside a session, so
    # the Demo runs only a ql-mcp the check above accepts.
    def test_the_demo_runs_only_a_ql_mcp_the_check_accepts(self):
        script = DEMO.read_text()
        bodies = {name: re.search(rf"(?ms)^{name}\(\) \{{\n(.*?)^\}}", script) for name in ("check", "serve")}
        for name, body in bodies.items():
            self.assertIsNotNone(body, name)
            self.assertIn("qlmcp_cli", body.group(1), name)
        self.assertNotIn("command -v codeql-development-mcp-server", bodies["serve"].group(1))


if __name__ == "__main__":
    unittest.main()
