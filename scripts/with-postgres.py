#!/usr/bin/env python3
"""Provide a disposable PostgreSQL runtime on the caller's Docker daemon."""

import os
import ipaddress
import re
import secrets
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import uuid


IMAGE = "postgres@sha256:f3bd19c606e442c3d7bdfa8002e03fe260a1023351e0ea4598032022b68dd6e3"
READY_ATTEMPTS = 60
DSN_ENV = "ABOM_TEST_POSTGRES_DSN"
CONTAINER_ENV = "ABOM_TEST_POSTGRES_CONTAINER"
REPLICA_ENV = "ABOM_TEST_POSTGRES_REPLICA_DSN"
REPLICA_CONTAINER_ENV = "ABOM_TEST_POSTGRES_REPLICA_CONTAINER"
BACKEND_ENV = "ABOM_TEST_STORE_BACKEND"
# How many processes run the store suite at once; each test migrates its own schema, so they share
# one primary without seeing each other.
SHARDS_ENV = "ABOM_TEST_SHARDS"
PROBE = ["python3", "-c", """
import os, socket, struct, urllib.parse
url = urllib.parse.urlsplit(os.environ["ABOM_TEST_POSTGRES_DSN"])
with socket.create_connection((url.hostname, url.port), 5) as connection:
    connection.sendall(struct.pack("!II", 8, 80877103))
    assert connection.recv(1) in (b"S", b"N"), "not a PostgreSQL SSL response"
print("PostgreSQL answered its wire protocol; storage compatibility was not tested")
"""]


def shard_store(environment):
    """Run the store suite on PostgreSQL as one compile and several concurrent processes."""
    shards = max(1, int(os.environ.get(SHARDS_ENV) or min(4, os.cpu_count() or 1)))
    package = os.path.join(os.getcwd(), "internal", "store")
    scratch = tempfile.mkdtemp(prefix="abom-store-shards-")
    processes = []
    try:
        binary = os.path.join(scratch, "store.test")
        built = subprocess.run(["go", "test", "-c", "-race", "-o", binary, "./internal/store"],
                               env=environment, check=False)
        if built.returncode:
            return built.returncode
        listed = subprocess.run([binary, "-test.list", "."], cwd=package, env=environment,
                                check=True, capture_output=True, text=True, timeout=120)
        names = sorted(name for name in listed.stdout.split() if name.startswith("Test"))
        groups = [group for group in (names[i::shards] for i in range(shards)) if group]
        print(f"store tests: {len(names)} tests in {len(groups)} concurrent processes", flush=True)
        for index, group in enumerate(groups):
            log = open(os.path.join(scratch, f"shard-{index}.log"), "w+")
            process = subprocess.Popen(
                [binary, "-test.run", "^(" + "|".join(group) + ")$", "-test.count=1", "-test.timeout=20m"],
                cwd=package, env=environment, stdout=log, stderr=subprocess.STDOUT)
            processes.append((index, group, process, log))
        failed = 0
        for index, group, process, log in processes:
            code = process.wait()
            log.seek(0)
            print(f"--- store shard {index + 1} of {len(groups)}: {len(group)} tests, exit {code}", flush=True)
            print(log.read(), end="", flush=True)
            failed = failed or code
        return failed
    finally:
        # Interrupted: a shard still running would hold the container this run is about to remove.
        for _, _, process, log in processes:
            if process.poll() is None:
                process.kill()
                process.wait()
            log.close()
        shutil.rmtree(scratch, ignore_errors=True)


STORE = [
    # Replica routing against the physical standby, the one test outside the store that needs it.
    # First: after the shards, the standby is still replaying their schemas and the test times out.
    ("postgres", ["go", "test", "./internal/serve", "-run", "^TestPhysicalReplica",
                  "-count=1", "-race", "-timeout=20m"]),
    ("postgres", shard_store),
]
COMMANDS = {
    "probe": [(None, PROBE)],
    # PostgreSQL alone: the store suite in concurrent shards, then replica routing on the standby.
    "store": STORE,
    "test": STORE,
}


def run(runs, replicas=False):
    if not runs:
        raise RuntimeError("a PostgreSQL test command is required")
    if os.environ.get(DSN_ENV):
        raise RuntimeError(f"{DSN_ENV} is already set; refusing to replace an existing database target")
    try:
        subprocess.run(["docker", "info"], check=True, capture_output=True, timeout=30)
    except (OSError, subprocess.SubprocessError) as error:
        raise RuntimeError("PostgreSQL tests need a running Docker daemon; no backend was tested") from error

    password = secrets.token_hex(24)
    environment = dict(os.environ, POSTGRES_PASSWORD=password)
    name = "abom-store-test-" + uuid.uuid4().hex
    launch = [
        "docker", "run", "--detach", "--name", name,
        "--label", "codeql-abom.test=store", "--memory", "2g",
        "--tmpfs", "/var/lib/postgresql/data",
        "--publish", "127.0.0.1::5432",
        "--env", "POSTGRES_USER=abom_test", "--env", "POSTGRES_DB=abom_test",
        "--env", "POSTGRES_PASSWORD", IMAGE,
    ]
    try:
        created = subprocess.run(
            launch, env=environment, check=True, capture_output=True, text=True, timeout=180)
    except (subprocess.TimeoutExpired, KeyboardInterrupt):
        remove(name)
        raise
    container = created.stdout.strip()
    if not re.fullmatch(r"[a-f0-9]+", container):
        remove(name)
        raise RuntimeError("Docker did not return a container ID for the PostgreSQL test instance")
    standby = None
    try:
        # The entrypoint's temporary Unix-socket server shuts down before the TCP service starts.
        for _ in range(READY_ATTEMPTS):
            ready = subprocess.run([
                "docker", "exec", container, "pg_isready",
                "--host", "127.0.0.1", "--port", "5432",
                "--username", "abom_test", "--dbname", "abom_test",
            ], capture_output=True, timeout=10)
            if ready.returncode == 0:
                break
            time.sleep(1)
        else:
            subprocess.run(["docker", "logs", container], check=False, timeout=10)
            raise RuntimeError("the PostgreSQL test container did not become ready")
        published = subprocess.run(
            ["docker", "port", container, "5432/tcp"],
            check=True, capture_output=True, text=True, timeout=10).stdout.strip()
        match = re.fullmatch(r"127\.0\.0\.1:([0-9]+)", published)
        if not match or not 0 < int(match[1]) < 65536:
            raise RuntimeError("Docker did not bind PostgreSQL to a single loopback port")
        dsn = f"postgresql://abom_test:{password}@{published}/abom_test?sslmode=disable"
        replica_dsn = ""
        if replicas:
            standby, replica_port = replica(container, password)
            replica_dsn = f"postgresql://abom_test:{password}@{replica_port}/abom_test?sslmode=disable"
        test_environment = dict(os.environ, **{
            DSN_ENV: dsn, CONTAINER_ENV: container, REPLICA_ENV: replica_dsn,
            REPLICA_CONTAINER_ENV: standby or "",
        })
        for backend, command in runs:
            environment = test_environment
            if backend:
                print(f"store tests: {backend}", flush=True)
                environment = dict(test_environment, **{BACKEND_ENV: backend})
            if callable(command):
                code = command(environment)
            else:
                code = subprocess.run(command, env=environment, check=False).returncode
            if code:
                return code
        return 0
    finally:
        try:
            if standby is not None:
                remove(standby)
        finally:
            remove(container)


def replica(primary, password):
    address = subprocess.run(
        ["docker", "inspect", "--format", "{{.NetworkSettings.Networks.bridge.IPAddress}}", primary],
        check=True, capture_output=True, text=True, timeout=10).stdout.strip()
    ipaddress.ip_address(address)
    subprocess.run(["docker", "exec", "-i", primary, "sh", "-c", 'cat >> "$PGDATA/pg_hba.conf"'],
                   input="\nhost replication abom_test 0.0.0.0/0 scram-sha-256\n",
                   text=True, check=True, timeout=10)
    subprocess.run(["docker", "exec", primary, "psql", "-U", "abom_test", "-d", "postgres",
                    "-c", "SELECT pg_reload_conf();"], check=True, stdout=subprocess.DEVNULL, timeout=10)
    name = "abom-replica-test-" + uuid.uuid4().hex
    environment = dict(os.environ, PGPASSWORD=password, PG_PRIMARY_HOST=address)
    try:
        created = subprocess.run([
            "docker", "run", "--detach", "--name", name, "--label", "codeql-abom.test=store",
            "--memory", "1g", "--tmpfs", "/var/lib/postgresql/data", "--publish", "127.0.0.1::5432",
            "--env", "PGPASSWORD", "--env", "PG_PRIMARY_HOST", IMAGE, "bash", "-c",
            'pg_basebackup -c fast -h "$PG_PRIMARY_HOST" -U abom_test -D /var/lib/postgresql/data -R -X stream'
            + ' && exec docker-entrypoint.sh postgres',
        ], env=environment, check=True, capture_output=True, text=True, timeout=180)
    except (subprocess.TimeoutExpired, KeyboardInterrupt):
        remove(name)
        raise
    container = created.stdout.strip()
    if not re.fullmatch(r"[a-f0-9]+", container):
        remove(name)
        raise RuntimeError("Docker did not identify the standby container")
    try:
        for _ in range(READY_ATTEMPTS):
            state = subprocess.run(["docker", "exec", container, "psql", "-U", "abom_test",
                                    "-d", "abom_test", "-Atc", "SELECT pg_is_in_recovery();"],
                                   capture_output=True, text=True, timeout=10)
            if state.returncode == 0 and state.stdout.strip() == "t":
                break
            time.sleep(1)
        else:
            raise RuntimeError("PostgreSQL standby did not become ready")
        published = subprocess.run(["docker", "port", container, "5432/tcp"],
                                   check=True, capture_output=True, text=True, timeout=10).stdout.strip()
        if not re.fullmatch(r"127\.0\.0\.1:[0-9]+", published):
            raise RuntimeError("PostgreSQL standby was not bound to loopback")
        return container, published
    except (OSError, RuntimeError, subprocess.SubprocessError, KeyboardInterrupt):
        remove(container)
        raise


def remove(container):
    try:
        subprocess.run(
            ["docker", "rm", "--force", "--volumes", container],
            check=True, capture_output=True, timeout=30)
    except (OSError, subprocess.SubprocessError) as error:
        raise RuntimeError(f"removing PostgreSQL test container {container} failed") from error


if __name__ == "__main__":
    def interrupt(_signum, _frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, interrupt)
    try:
        if len(sys.argv) != 2 or sys.argv[1] not in COMMANDS:
            raise RuntimeError("choose 'probe', 'store' or 'test' for PostgreSQL verification")
        sys.exit(run(COMMANDS[sys.argv[1]], replicas=sys.argv[1] != "probe"))
    except (OSError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"postgres-tests: {error}", file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        sys.exit(130)
