import importlib.util
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("check_changed", Path(__file__).with_name("check-changed.py"))
check = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check)

M = "github.com/advanced-security/codeql-abom"
# failure is imported by expand and, through it, by serve; store by serve; authz by store.
PACKAGES = {
    M + "/internal/failure": set(),
    M + "/internal/authz": set(),
    M + "/internal/access": set(),
    M + "/internal/store": {M + "/internal/authz"},
    M + "/internal/expand": {M + "/internal/failure"},
    M + "/internal/serve": {M + "/internal/expand", M + "/internal/failure", M + "/internal/store", M + "/internal/authz"},
    M + "/cmd/codeql-abom": {M + "/internal/serve", M + "/internal/store", M + "/internal/authz"},
}
DIRS = {path.removeprefix(M + "/"): path for path in PACKAGES}


def chosen(*paths):
    return check.plan(list(paths), PACKAGES, DIRS)


class CheckChangedTest(unittest.TestCase):
    def test_a_go_change_runs_its_package_and_everything_that_imports_it(self):
        p = chosen("internal/failure/failure.go")
        self.assertEqual(p.packages, [M + "/internal/expand", M + "/internal/failure", M + "/internal/serve"])
        self.assertEqual(p.specs(), ["go:go-test", "checks:check-corrupt,check-versions,fmt,vet,lint"])

    def test_a_file_a_package_reads_is_a_change_to_that_package(self):
        self.assertEqual(chosen("internal/expand/testdata/case/workflow.yml").packages,
                         [M + "/internal/expand", M + "/internal/serve"])

    def test_the_store_on_postgresql_runs_when_anything_it_imports_changed(self):
        self.assertIn("postgres", chosen("internal/authz/key.go").groups)
        self.assertNotIn("postgres", chosen("internal/failure/failure.go").groups)

    def test_the_interface_runs_its_component_tests_and_style(self):
        self.assertEqual(chosen("ui/Pages/Settings.razor").specs(), ["interface:ui-test", "checks:check-versions,style"])
        self.assertEqual(chosen("ui/wwwroot/css/app.css").specs(), ["checks:check-versions,style"])
        self.assertEqual(chosen("ui/wwwroot/js/graph.js").specs(), ["checks:check-versions,js,style"])

    def test_each_kind_of_file_reaches_the_check_that_reads_it(self):
        for path, spec in {
            "ql/actions/lib/abom/actions/LockCoverage.qll": "ql:ql",
            "infra/k8s/helm/codeql-abom/values.yaml": "checks:check-versions,chart",
            "infra/tests/kind/values.yaml": "checks:check-versions,chart",
            ".github/workflows/self-audit.yml": "checks:check-versions,pins,workflows",
            ".github/actions/setup-codeql/action.yml": "checks:check-versions,pins,workflows",
            ".github/actionlint.yaml": "checks:check-versions,workflows",
            "infra/qlaas-repo-template/workflows/test.yml": "checks:check-versions,workflows",
            "scripts/check-workflows.sh": "checks:check-versions,workflows",
            "scripts/with-postgres.py": "checks:check-versions,script-test",
            "infra/docker/Dockerfile": "checks:check-versions",
        }.items():
            self.assertIn(spec, chosen(path).specs(), path)

    def test_signing_in_runs_the_browser_suite_with_sign_in_on(self):
        for path in ("ui/tests/e2e/signin/signin.spec.mjs", "ui/tests/e2e/playwright.signin.config.mjs",
                     "internal/access/access.go"):
            self.assertIn("e2e-signin", chosen(path).groups.get("interface", []), path)
        self.assertNotIn("interface", chosen("ui/tests/e2e/specs/tips.spec.mjs").groups)

    def test_what_only_people_read_runs_nothing(self):
        p = chosen("docs/architecture.md", "plans/next.md", ".github/skills/x/SKILL.md", "infra/tests/kind/up.sh")
        self.assertEqual(p.groups, {})

    # Wider is the only safe way to be wrong.
    def test_what_reaches_everything_or_cannot_be_placed_runs_everything(self):
        for path in ["Makefile", "go.mod", ".codeql-version", "testdata/corpus/x.json", "somewhere/new.txt",
                     "internal/gone/removed.go"]:
            self.assertIsNone(chosen("internal/failure/failure.go", path), path)


if __name__ == "__main__":
    unittest.main()
