package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// installingCLI answers `codeql pack install <dir>` as the CLI does, by writing the lock, and logs
// every invocation one per line with its arguments joined by "|".
const installingCLI = `(IFS='|'; printf '%s\n' "$*") >> "$(dirname "$0")/calls.txt"
if [ "$1" = pack ] && [ "$2" = install ]; then
  printf 'lockVersion: 1.0.0\ndependencies: {}\ncompiled: false\n' > "$3/codeql-pack.lock.yml"
fi
`

// scaffoldedPack lays out the two manifests ABOM scaffolds, with no lock in either, as the
// operator's pack.
func scaffoldedPack(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "qlpack.yml"),
		"name: abom/custom-queries\nversion: 0.0.1\ndependencies:\n  codeql/actions-all: 0.6.2\n")
	write(t, filepath.Join(root, "test", "qlpack.yml"),
		"name: abom/custom-queries-tests\nversion: 0.0.1\nextractor: actions\n"+
			"dependencies:\n  abom/custom-queries: \"*\"\n  codeql/actions-all: 0.6.2\n")
	withCustomRoot(t, root)
	return root
}

// invocations reads what the stub CLI was asked to do, one argument list per call.
func invocations(t *testing.T, stub string) [][]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(filepath.Dir(stub), "calls.txt"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		out = append(out, strings.Split(line, "|"))
	}
	return out
}

// searches reports whether an invocation puts dir on the pack search path.
func searches(args []string, dir string) bool {
	i := slices.Index(args, "--additional-packs")
	return i >= 0 && i+1 < len(args) &&
		slices.Contains(strings.Split(args[i+1], string(os.PathListSeparator)), dir)
}

// The CLI refuses to compile a query or a test in a pack whose dependencies were never installed,
// even when every one of them is already downloaded, so ABOM installs the pack it scaffolded: the
// query pack first, then the test pack, which names the query pack and finds it only on the
// search path.
func TestAScaffoldedPackIsInstalledWhereItHasNoLock(t *testing.T) {
	root := scaffoldedPack(t)
	stub, _ := stubCodeQL(t, installingCLI)
	r := &Runner{CodeQLPath: stub, Lock: &Lock{}}

	if err := r.InstallPack(context.Background()); err != nil {
		t.Fatalf("InstallPack: %v", err)
	}

	calls := invocations(t, stub)
	want := []string{root, filepath.Join(root, "test")}
	if len(calls) != len(want) {
		t.Fatalf("the CLI was asked %v, want one install of each of %v", calls, want)
	}
	for i, dir := range want {
		if got := calls[i][:3]; !slices.Equal(got, []string{"pack", "install", dir}) {
			t.Errorf("call %d was %v, want pack install %s", i, calls[i], dir)
		}
		if !searches(calls[i], root) {
			t.Errorf("installing %s did not search %s, where the test pack's dependency lives: %v",
				dir, root, calls[i])
		}
		if _, err := os.Stat(filepath.Join(dir, "codeql-pack.lock.yml")); err != nil {
			t.Errorf("%s has no lock after its install: %v", dir, err)
		}
	}
}

// A lock is what makes the pack's dependencies the ones it was reviewed with, and replacing it is
// `codeql pack upgrade`, the operator's to run. So a lock that exists is never touched, and only a
// pack without one is installed.
func TestAPackThatHasALockIsLeftExactlyAsItWas(t *testing.T) {
	for _, tc := range []struct {
		name    string
		locked  []string
		install []string
	}{
		{name: "both locked", locked: []string{".", "test"}},
		{name: "query pack locked", locked: []string{"."}, install: []string{"test"}},
		{name: "test pack locked", locked: []string{"test"}, install: []string{"."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := scaffoldedPack(t)
			stub, _ := stubCodeQL(t, installingCLI)
			lock := "lockVersion: 1.0.0\ndependencies:\n  codeql/actions-all:\n    version: 0.6.2\n"
			for _, rel := range tc.locked {
				write(t, filepath.Join(root, rel, "codeql-pack.lock.yml"), lock)
			}

			if err := (&Runner{CodeQLPath: stub, Lock: &Lock{}}).InstallPack(context.Background()); err != nil {
				t.Fatalf("InstallPack: %v", err)
			}

			var installed []string
			for _, call := range invocations(t, stub) {
				if slices.Contains(call, "upgrade") {
					t.Errorf("the CLI was asked to upgrade: %v", call)
				}
				rel, err := filepath.Rel(root, call[2])
				if err != nil {
					t.Fatal(err)
				}
				installed = append(installed, rel)
			}
			if !slices.Equal(installed, tc.install) {
				t.Errorf("installed %v, want only the pack without a lock: %v", installed, tc.install)
			}
			for _, rel := range tc.locked {
				body, err := os.ReadFile(filepath.Join(root, rel, "codeql-pack.lock.yml"))
				if err != nil || string(body) != lock {
					t.Errorf("the lock in %s reads %q (%v), want it exactly as it was", rel, body, err)
				}
			}
		})
	}
}

// An install that fails is the answer: nothing in the pack can compile, and what the CLI said is
// the only account of why.
func TestAFailedInstallAnswersWithWhatTheCLISaid(t *testing.T) {
	root := scaffoldedPack(t)
	stub, _ := stubCodeQL(t, `(IFS='|'; printf '%s\n' "$*") >> "$(dirname "$0")/calls.txt"
echo "A fatal error occurred: Failed to download codeql/actions-all@0.6.2" >&2
exit 1
`)
	r := &Runner{CodeQLPath: stub, Lock: &Lock{}}

	err := r.InstallPack(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Failed to download codeql/actions-all@0.6.2") ||
		!strings.Contains(err.Error(), root) {
		t.Fatalf("a failed install answered %v, want the CLI's reason and the pack", err)
	}
	if calls := invocations(t, stub); len(calls) != 1 {
		t.Errorf("the CLI was asked %v, want the test pack left alone once its query pack failed", calls)
	}
	if _, err := os.Stat(filepath.Join(root, "codeql-pack.lock.yml")); err == nil {
		t.Error("a failed install left a lock")
	}
}

// Pack installs write the package cache every other CodeQL invocation reads, so one takes the lock
// they take.
func TestAnInstallWaitsForCodeQLToBeFree(t *testing.T) {
	scaffoldedPack(t)
	stub, _ := stubCodeQL(t, installingCLI)
	r := &Runner{CodeQLPath: stub, Lock: &Lock{}}
	release, err := r.Lock.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := r.InstallPack(ctx); err == nil {
		t.Error("an install ran while somebody else held CodeQL")
	}
	if calls := invocations(t, stub); len(calls) != 0 {
		t.Errorf("the CLI was asked %v while the lock was held", calls)
	}
}

// A Test or a run of a pack that is already installed must not queue behind a crawl to find that
// out, so a pack with nothing to install takes no lock.
func TestAnInstalledPackDoesNotWaitForCodeQL(t *testing.T) {
	root := scaffoldedPack(t)
	for _, rel := range []string{".", "test"} {
		write(t, filepath.Join(root, rel, "codeql-pack.lock.yml"), "lockVersion: 1.0.0\n")
	}
	stub, _ := stubCodeQL(t, installingCLI)
	r := &Runner{CodeQLPath: stub, Lock: &Lock{}}
	release, err := r.Lock.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := r.InstallPack(ctx); err != nil {
		t.Errorf("an installed pack waited on CodeQL: %v", err)
	}
}

// An authoring session holds the lock for its whole life, and its pack is installed before the
// agent starts; waiting for the lock there would wait on itself.
func TestAnInstallForTheHolderOfTheLockDoesNotWaitForIt(t *testing.T) {
	root := scaffoldedPack(t)
	stub, _ := stubCodeQL(t, installingCLI)
	r := &Runner{CodeQLPath: stub, Lock: &Lock{}}
	release, err := r.Lock.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.InstallPackHeld(ctx); err != nil {
		t.Fatalf("InstallPackHeld: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "test", "codeql-pack.lock.yml")); err != nil {
		t.Errorf("the pack was not installed for the holder of the lock: %v", err)
	}
}

// Without an operator's pack there is nothing ABOM scaffolded, so nothing to install.
func TestWithoutAnOperatorsPackNothingIsInstalled(t *testing.T) {
	withCustomRoot(t, "")
	stub, _ := stubCodeQL(t, installingCLI)
	if err := (&Runner{CodeQLPath: stub}).InstallPack(context.Background()); err != nil {
		t.Fatalf("InstallPack: %v", err)
	}
	if calls := invocations(t, stub); len(calls) != 0 {
		t.Errorf("the CLI was asked %v with no pack configured", calls)
	}
}

// An offline runner, such as a Demo's, must never reach a registry: installing contacts one even
// when every dependency is cached, so an unlocked pack is refused, by name, without running the CLI.
func TestAnOfflineRunnerRefusesAnUnlockedPackWithoutAskingTheCLI(t *testing.T) {
	root := scaffoldedPack(t)
	stub, _ := stubCodeQL(t, installingCLI)
	r := &Runner{CodeQLPath: stub, Lock: &Lock{}, Offline: true}

	err := r.InstallPack(context.Background())
	if err == nil || !strings.Contains(err.Error(), root) || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("InstallPack offline: %v, want a refusal naming %s and its lock", err, root)
	}
	if calls := invocations(t, stub); len(calls) != 0 {
		t.Fatalf("an offline runner asked the CLI %v", calls)
	}
	for _, dir := range []string{root, filepath.Join(root, "test")} {
		write(t, filepath.Join(dir, "codeql-pack.lock.yml"), "lockVersion: 1.0.0\ndependencies: {}\ncompiled: false\n")
	}
	if err := r.InstallPack(context.Background()); err != nil {
		t.Fatalf("an offline runner refused a locked pack: %v", err)
	}
}
