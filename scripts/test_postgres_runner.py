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

    def test_socket_only_initialization_is_not_ready_for_standby_setup(self):
        for becomes_ready in (True, False):
            with self.subTest(tcp_becomes_ready=becomes_ready):
                calls = []
                listening = False

                def execute(args, **kwargs):
                    calls.append(args)
                    if args[:2] == ["docker", "run"]:
                        return subprocess.CompletedProcess(args, 0, stdout="abc123\n")
                    if args[:4] == ["docker", "exec", "abc123", "pg_isready"]:
                        tcp = "--host" in args and args[args.index("--host") + 1] == "127.0.0.1"
                        return subprocess.CompletedProcess(args, 0 if listening or not tcp else 2)
                    if args[:2] == ["docker", "port"]:
                        return subprocess.CompletedProcess(args, 0, stdout="127.0.0.1:54321\n")
                    if args[0] == "contract-suite":
                        self.assertTrue(listening, "contracts started against the initialization server")
                    return subprocess.CompletedProcess(args, 0, stdout="")

                def finish_initialization(_seconds):
                    nonlocal listening
                    listening = becomes_ready

                def start_standby(primary, _password):
                    if not listening:
                        raise subprocess.CalledProcessError(2, [
                            "docker", "exec", primary, "psql", "-U", "abom_test",
                            "-d", "postgres", "-c", "SELECT pg_reload_conf();",
                        ])
                    return "def456", "127.0.0.1:54322"

                with patch.object(runner.subprocess, "run", side_effect=execute), \
                        patch.object(runner.time, "sleep", side_effect=finish_initialization), \
                        patch.object(runner, "READY_ATTEMPTS", 2), \
                        patch.object(runner, "replica", side_effect=start_standby) as standby:
                    if becomes_ready:
                        self.assertEqual(runner.run([(None, ["contract-suite"])], replicas=True), 0)
                        standby.assert_called_once()
                    else:
                        with self.assertRaisesRegex(RuntimeError, "did not become ready"):
                            runner.run([(None, ["contract-suite"])], replicas=True)
                        standby.assert_not_called()
                        self.assertNotIn(["contract-suite"], calls)

                probes = [args for args in calls if "pg_isready" in args]
                self.assertEqual(len(probes), 2)
                for args in probes:
                    self.assertEqual(args[args.index("--port") + 1], "5432")
                self.assertEqual(calls[-1], ["docker", "rm", "--force", "--volumes", "abc123"])

    def test_primary_reload_errors_are_not_hidden_from_the_ci_log(self):
        def execute(args, **kwargs):
            if args[:2] == ["docker", "inspect"]:
                return subprocess.CompletedProcess(args, 0, stdout="172.17.0.2\n")
            if "SELECT pg_reload_conf();" in args:
                self.assertFalse(kwargs.get("capture_output", False))
                self.assertIsNone(kwargs.get("stderr"))
                raise subprocess.CalledProcessError(2, args)
            return subprocess.CompletedProcess(args, 0, stdout="")

        with patch.object(runner.subprocess, "run", side_effect=execute):
            with self.assertRaises(subprocess.CalledProcessError):
                runner.replica("abc123", "test-password")

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


    def test_backend_test_is_the_sharded_store_suite(self):
        self.assertIs(runner.COMMANDS["test"], runner.COMMANDS["store"])
        self.assertEqual([backend for backend, _ in runner.COMMANDS["store"]], ["postgres", "postgres"])


class StoreShardTests(unittest.TestCase):
    """The PostgreSQL store suite is one compile and several concurrent processes on one primary."""

    NAMES = [f"TestStore{i:02d}" for i in range(10)] + ["BenchmarkIgnored", "ExampleIgnored"]

    def shard(self, shards, failing=None, listed=None, refuse_start=None):
        """Run the store action against fakes, keeping what it did on self for after a raise."""
        self.calls, self.launched, self.opened = [], [], []
        launched = self.launched

        def execute(args, **kwargs):
            self.calls.append((args, kwargs))
            if args[:2] == ["docker", "run"]:
                return subprocess.CompletedProcess(args, 0, stdout="abc123\n")
            if args[:2] == ["docker", "port"]:
                return subprocess.CompletedProcess(args, 0, stdout="127.0.0.1:54321\n")
            if len(args) > 1 and args[1] == "-test.list":
                return listed or subprocess.CompletedProcess(args, 0, stdout="\n".join(self.NAMES) + "\nok\n")
            return subprocess.CompletedProcess(args, 0, stdout="")

        class Process:
            def __init__(self, args, **kwargs):
                if refuse_start is not None and len(launched) == refuse_start:
                    raise OSError("the shard could not start")
                launched.append(self)
                self.args, self.kwargs = args, kwargs
                self.code, self.killed = None, False

            def poll(self):
                return self.code

            def wait(self, timeout=None):
                run = self.args[self.args.index("-test.run") + 1]
                self.code = 3 if failing and failing in run else 0
                return self.code

            def kill(self):
                self.killed = True

        def tracked(*args, **kwargs):
            file = open(*args, **kwargs)
            self.opened.append(file)
            return file

        with patch.object(runner.subprocess, "run", side_effect=execute), \
                patch.object(runner.subprocess, "Popen", Process), \
                patch.object(runner, "replica", return_value=("def456", "127.0.0.1:54322")), \
                patch.object(runner, "open", tracked, create=True), \
                patch.dict(os.environ, {runner.SHARDS_ENV: str(shards)}), \
                patch("sys.stdout", new_callable=io.StringIO):
            return runner.run(runner.COMMANDS["store"], replicas=True)

    def scratch(self):
        """The directory the suite was compiled into, which must not outlive the run."""
        compiled = next(args for args, _ in self.calls if args[:3] == ["go", "test", "-c"])
        return os.path.dirname(compiled[compiled.index("-o") + 1])

    def assert_cleaned_up(self):
        self.assertTrue(self.opened, "no shard log was opened, so nothing below was checked")
        self.assertTrue(all(file.closed for file in self.opened), "a shard log was left open")
        self.assertFalse(os.path.exists(self.scratch()), "the scratch directory outlived the run")
        self.assertEqual(self.calls[-1][0], ["docker", "rm", "--force", "--volumes", "abc123"])

    def test_the_store_suite_is_compiled_once_with_the_race_detector(self):
        self.assertEqual(self.shard(4), 0)
        compiles = [args for args, _ in self.calls if args[:3] == ["go", "test", "-c"]]
        self.assertEqual(len(compiles), 1)
        self.assertIn("-race", compiles[0])
        self.assertIn("./internal/store", compiles[0])

    def test_every_test_runs_in_exactly_one_concurrent_shard(self):
        self.assertEqual(self.shard(4), 0)
        self.assertEqual(len(self.launched), 4)
        seen = []
        for process in self.launched:
            args, kwargs = process.args, process.kwargs
            self.assertEqual(kwargs["env"][runner.BACKEND_ENV], "postgres")
            self.assertTrue(kwargs["env"][runner.DSN_ENV].startswith("postgresql://"))
            self.assertTrue(kwargs["cwd"].endswith(os.path.join("internal", "store")))
            self.assertIn("-test.count=1", args)
            run = args[args.index("-test.run") + 1]
            self.assertRegex(run, r"^\^\(.*\)\$$")
            seen += run[2:-2].split("|")
        self.assertEqual(sorted(seen), [n for n in self.NAMES if n.startswith("Test")])

    def test_more_shards_than_tests_starts_no_empty_process(self):
        self.assertEqual(self.shard(50), 0)
        self.assertEqual(len(self.launched), 10)

    def test_the_standby_test_runs_once_on_postgres_before_the_shards(self):
        # After the shards the standby is still replaying their schemas and would time out.
        self.shard(2)
        commands = [args for args, _ in self.calls if args[:1] == ["go"]]
        standby = [i for i, args in enumerate(commands) if "./internal/serve" in args]
        compile_at = next(i for i, args in enumerate(commands) if args[:3] == ["go", "test", "-c"])
        self.assertEqual(len(standby), 1)
        self.assertLess(standby[0], compile_at)
        args, kwargs = next((a, k) for a, k in self.calls if "./internal/serve" in a)
        self.assertIn("-race", args)
        self.assertEqual(args[args.index("-run") + 1], "^TestPhysicalReplica")
        self.assertEqual(kwargs["env"][runner.BACKEND_ENV], "postgres")
        self.assertTrue(kwargs["env"][runner.REPLICA_ENV])

    def test_a_failing_shard_fails_the_suite_after_every_shard_reports(self):
        self.assertNotEqual(self.shard(4, failing="TestStore03"), 0)
        self.assertEqual(len(self.launched), 4)
        self.assert_cleaned_up()

    def test_a_passing_run_leaves_no_shard_log_open_or_scratch_behind(self):
        self.assertEqual(self.shard(4), 0)
        self.assert_cleaned_up()

    # Popen raising after the shard's log was opened leaked that log, and left the shards already
    # started running against a container the harness was about to remove.
    def test_a_shard_that_cannot_start_leaves_nothing_running_or_open(self):
        with self.assertRaisesRegex(OSError, "could not start"):
            self.shard(4, refuse_start=1)
        self.assertEqual(len(self.launched), 1)
        self.assertTrue(self.launched[0].killed, "a started shard outlived the run")
        self.assert_cleaned_up()

    # A raised CalledProcessError names the command, not what the binary printed about why.
    def test_a_listing_that_fails_says_what_the_test_binary_printed(self):
        refused = subprocess.CompletedProcess([], 2, stdout="", stderr="panic: the store would not open\n")
        with self.assertRaisesRegex(RuntimeError, "panic: the store would not open"):
            self.shard(4, listed=refused)
        self.assertEqual(self.launched, [])
        self.assertEqual(self.calls[-1][0], ["docker", "rm", "--force", "--volumes", "abc123"])

    # A suite that ran nothing would otherwise be reported as a pass.
    def test_a_listing_of_no_tests_is_refused_rather_than_passed(self):
        empty = subprocess.CompletedProcess([], 0, stdout="ok\n")
        with self.assertRaisesRegex(RuntimeError, "listed no tests"):
            self.shard(4, listed=empty)
        self.assertEqual(self.launched, [])


class ShardCountTests(unittest.TestCase):
    """ABOM_TEST_SHARDS chooses how many processes run the store suite."""

    def count(self, value):
        with patch.dict(os.environ, {runner.SHARDS_ENV: value}):
            return runner.shard_count()

    def test_unset_or_empty_is_four_on_every_machine(self):
        # One split everywhere, so a failure between tests that share a shard reproduces anywhere.
        with patch.object(runner.os, "cpu_count", return_value=2), patch.dict(os.environ):
            os.environ.pop(runner.SHARDS_ENV, None)
            self.assertEqual(runner.shard_count(), 4)
            self.assertEqual(self.count(""), 4)

    def test_a_positive_integer_is_taken(self):
        self.assertEqual(self.count("1"), 1)
        self.assertEqual(self.count(" 6 "), 6)

    def test_anything_else_is_refused_by_name(self):
        for value in ["auto", "0", "-2", "4.0"]:
            with self.subTest(value=value):
                with self.assertRaises(RuntimeError) as refused:
                    self.count(value)
                self.assertIn(f"{runner.SHARDS_ENV} must be a positive integer, not {value!r}",
                              str(refused.exception))


if __name__ == "__main__":
    unittest.main()
