"""scripts/release/notices.py: the notices a release ships for what it redistributes."""

import contextlib
import importlib.util
import io
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
import unittest.mock


ROOT = Path(__file__).resolve().parent.parent
NOTICES = ROOT / "scripts" / "release" / "notices.py"
VENDORED = ROOT / "ui" / "wwwroot" / "js"
RULE, UNDER = "=" * 78, "-" * 78

SPEC = importlib.util.spec_from_file_location("notices", NOTICES)
notices = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(notices)


def web_in(folder):
    """What `notices.py web` does with the scripts in folder: its exit status, and what it wrote."""
    out, err = io.StringIO(), io.StringIO()
    with unittest.mock.patch.object(notices, "VENDORED", folder), contextlib.redirect_stdout(out), \
            contextlib.redirect_stderr(err):
        status = notices.main(["notices.py", "web"])
    return status, out.getvalue(), err.getvalue()


class WebNoticesTests(unittest.TestCase):
    """notices.py web, for the scripts vendored into the interface."""

    # No package manager restored these scripts, so no other notice in a release names them, and
    # 3d-force-graph's build bundles the dozens of packages it depends on.
    def test_every_script_vendored_into_the_interface_ships_with_its_license(self):
        result = subprocess.run([sys.executable, str(NOTICES), "web"], cwd=ROOT, capture_output=True,
                                text=True, timeout=60, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        scripts = sorted(VENDORED.glob("*.min.js"))
        self.assertTrue(scripts, "nothing is vendored, so this test proves nothing")
        for script in scripts:
            self.assertRegex(result.stdout, rf"(?m)^{re.escape(script.name)}: \S+ \S+$")
        self.assertIn("Permission is hereby granted", result.stdout)
        self.assertRegex(result.stdout, r"(?m)^3d-force-graph\.min\.js bundles: three \S+$")

    def test_a_vendored_script_without_a_notice_stops_the_release(self):
        with tempfile.TemporaryDirectory() as scratch:
            folder = Path(scratch)
            shutil.copy(VENDORED / "THIRD_PARTY_NOTICES.txt", folder)
            (folder / "three.min.js").write_text("")
            (folder / "newer-library.min.js").write_text("")
            status, _, err = web_in(folder)
        self.assertEqual(status, 1)
        self.assertIn("newer-library.min.js", err)
        self.assertNotIn("three.min.js", err)

    # A heading with nothing under it names a license without giving the text it requires.
    def test_a_notice_that_names_a_license_without_its_text_does_not_count(self):
        with tempfile.TemporaryDirectory() as scratch:
            folder = Path(scratch)
            (folder / "THIRD_PARTY_NOTICES.txt").write_text(f"{RULE}\nthree.min.js: three 0.147.0\n{UNDER}\n\n")
            (folder / "three.min.js").write_text("")
            status, _, err = web_in(folder)
        self.assertEqual(status, 1)
        self.assertIn("three.min.js", err)

    def test_a_release_carries_the_notices_for_its_vendored_scripts(self):
        recipe = re.search(r"(?ms)^release-ui:.*?(?=^\S)", (ROOT / "Makefile").read_text())
        self.assertIsNotNone(recipe, "the Makefile has no release-ui target")
        writes = [line for line in recipe.group(0).replace("\\\n", " ").splitlines()
                  if "ui-notices.txt" in line]
        self.assertTrue(any("notices.py web" in line for line in writes), writes)


if __name__ == "__main__":
    unittest.main()
