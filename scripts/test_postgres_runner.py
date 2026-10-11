import importlib.util
import io
import os
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch


SPEC = importlib.util.spec_from_file_location(
    "postgres_runner", Path(__file__).with_name("with-postgres.py"))
runner = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runner)


class DockerRunnerTests(unittest.TestCase):
    def test_missing_daemon_never_runs_the_suite(self):
        with patch.object(runner.subprocess, "run", side_effect=subprocess.CalledProcessError(1, "docker")):
            with self.assertRaisesRegex(RuntimeError, "running Docker daemon"):
                runner.run([(None, ["go", "test", "./internal/store/..."])])

    def test_suite_gets_loopback_database_and_only_its_container_is_removed(self):
        calls = []

        def execute(args, **kwargs):
            calls.append((args, kwargs))
            if args[:2] == ["docker", "run"]:
                return subprocess.CompletedProcess(args, 0, stdout="abc123\n")
            if args[:2] == ["docker", "port"]:
                return subprocess.CompletedProcess(args, 0, stdout="127.0.0.1:54321\n")
            if args[0] == "contract-suite":
                dsn = kwargs["env"]["ABOM_TEST_POSTGRES_DSN"]
                self.assertEqual(kwargs["env"]["ABOM_TEST_POSTGRES_CONTAINER"], "abc123")
                self.assertRegex(dsn, r"^postgresql://abom_test:[a-f0-9]+@127\.0\.0\.1:54321/abom_test\?sslmode=disable$")
                return subprocess.CompletedProcess(args, 7)
            return subprocess.CompletedProcess(args, 0, stdout="")

        with patch.object(runner.subprocess, "run", side_effect=execute):
            result = runner.run([(None, ["contract-suite", "--one"])])
        self.assertEqual(result, 7)
        run = next(args for args, _ in calls if args[:2] == ["docker", "run"])
        self.assertIn("127.0.0.1::5432", run)
        self.assertIn("/var/lib/postgresql/data", run)
        self.assertIn("@sha256:", run[-1])
        self.assertEqual(calls[-1][0], ["docker", "rm", "--force", "--volumes", "abc123"])
        suite = next(args for args, _ in calls if args[0] == "contract-suite")
        self.assertEqual(suite, ["contract-suite", "--one"])

    def test_failed_readiness_removes_the_container_and_never_runs_the_suite(self):
        calls = []

        def execute(args, **kwargs):
            calls.append(args)
            if args[:2] == ["docker", "run"]:
                return subprocess.CompletedProcess(args, 0, stdout="abc123\n")
            if args[:2] == ["docker", "exec"]:
                return subprocess.CompletedProcess(args, 1)
            return subprocess.CompletedProcess(args, 0, stdout="")

        with patch.object(runner.subprocess, "run", side_effect=execute), \
                patch.object(runner.time, "sleep"), patch.object(runner, "READY_ATTEMPTS", 2):
            with self.assertRaisesRegex(RuntimeError, "did not become ready"):
                runner.run([(None, ["contract-suite"])])
        self.assertNotIn(["contract-suite"], calls)
        self.assertEqual(calls[-1], ["docker", "rm", "--force", "--volumes", "abc123"])

    def test_an_existing_database_target_is_not_replaced(self):
        with patch.dict(os.environ, {"ABOM_TEST_POSTGRES_DSN": "postgresql://existing"}), \
                patch.object(runner.subprocess, "run") as execute:
            with self.assertRaisesRegex(RuntimeError, "already set"):
                runner.run([(None, ["contract-suite"])])
            execute.assert_not_called()

    def test_a_timed_out_launch_removes_only_its_generated_container_name(self):
        calls = []

        def execute(args, **kwargs):
            calls.append(args)
            if args[:2] == ["docker", "run"]:
                raise subprocess.TimeoutExpired(args, 180)
            return subprocess.CompletedProcess(args, 0)

        with patch.object(runner.subprocess, "run", side_effect=execute):
            with self.assertRaises(subprocess.TimeoutExpired):
                runner.run([(None, ["contract-suite"])])
        launch = next(args for args in calls if args[:2] == ["docker", "run"])
        name = launch[launch.index("--name") + 1]
        self.assertTrue(name.startswith("abom-store-test-"))
        self.assertEqual(calls[-1], ["docker", "rm", "--force", "--volumes", name])

    def test_cleanup_failure_is_not_reported_as_a_success(self):
        def execute(args, **kwargs):
            if args[:2] == ["docker", "run"]:
                return subprocess.CompletedProcess(args, 0, stdout="abc123\n")
            if args[:2] == ["docker", "port"]:
                return subprocess.CompletedProcess(args, 0, stdout="127.0.0.1:54321\n")
            if args[:2] == ["docker", "rm"]:
                raise subprocess.CalledProcessError(1, args)
            return subprocess.CompletedProcess(args, 0, stdout="")

        with patch.object(runner.subprocess, "run", side_effect=execute), \
                patch("sys.stderr", new_callable=io.StringIO):
            with self.assertRaisesRegex(RuntimeError, "removing PostgreSQL test container"):
                runner.run([(None, ["contract-suite"])])


    def test_replica_is_removed_before_its_primary(self):
        calls = []

        def execute(args, **kwargs):
            calls.append(args)
            if args[:2] == ["docker", "run"]:
                return subprocess.CompletedProcess(args, 0, stdout="abc123\n")
            if args[:2] == ["docker", "port"]:
                return subprocess.CompletedProcess(args, 0, stdout="127.0.0.1:54321\n")
            if args[0] == "contract-suite":
                self.assertEqual(kwargs["env"][runner.REPLICA_CONTAINER_ENV], "def456")
                self.assertIn("127.0.0.1:54322", kwargs["env"][runner.REPLICA_ENV])
            return subprocess.CompletedProcess(args, 0, stdout="")

        with patch.object(runner.subprocess, "run", side_effect=execute), \
                patch.object(runner, "replica", return_value=("def456", "127.0.0.1:54322")):
            self.assertEqual(runner.run([(None, ["contract-suite"])], replicas=True), 0)
        self.assertEqual(calls[-2:], [
            ["docker", "rm", "--force", "--volumes", "def456"],
            ["docker", "rm", "--force", "--volumes", "abc123"],
        ])

    def test_failed_standby_setup_never_runs_contracts_and_cleans_primary(self):
        calls = []

        def execute(args, **kwargs):
            calls.append(args)
            output = "abc123\n" if args[:2] == ["docker", "run"] else "127.0.0.1:54321\n"
            return subprocess.CompletedProcess(args, 0, stdout=output)

        with patch.object(runner.subprocess, "run", side_effect=execute), \
                patch.object(runner, "replica", side_effect=RuntimeError("standby failed")):
            with self.assertRaisesRegex(RuntimeError, "standby failed"):
                runner.run([(None, ["contract-suite"])], replicas=True)
        self.assertNotIn(["contract-suite"], calls)
        self.assertEqual(calls[-1], ["docker", "rm", "--force", "--volumes", "abc123"])


    def test_each_action_selects_the_store_backend_for_its_own_packages(self):
        for action, expected in [
            ("suite", [("sqlite", ["./..."]), ("postgres", ["./internal/store"])]),
            ("test", [("postgres", ["./internal/store", "./internal/serve"])]),
        ]:
            with self.subTest(action=action):
                runs = []

                def execute(args, **kwargs):
                    if args[:2] == ["docker", "run"]:
                        return subprocess.CompletedProcess(args, 0, stdout="abc123\n")
                    if args[:2] == ["docker", "port"]:
                        return subprocess.CompletedProcess(args, 0, stdout="127.0.0.1:54321\n")
                    if args[0] == "go":
                        self.assertIn("-timeout=20m", args)
                        self.assertTrue(kwargs["env"][runner.REPLICA_ENV])
                        packages = [arg for arg in args[2:] if arg.startswith("./")]
                        runs.append((kwargs["env"]["ABOM_TEST_STORE_BACKEND"], packages))
                    return subprocess.CompletedProcess(args, 0, stdout="")

                with patch.object(runner.subprocess, "run", side_effect=execute), \
                        patch.object(runner, "replica", return_value=("def456", "127.0.0.1:54322")), \
                        patch("sys.stdout", new_callable=io.StringIO):
                    self.assertEqual(runner.run(runner.COMMANDS[action], replicas=True), 0)
                self.assertEqual(runs, expected)


if __name__ == "__main__":
    unittest.main()
