package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCreateDatabaseInvokesCodeQL(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	db := filepath.Join(t.TempDir(), "db")
	if err := r.CreateDatabase(context.Background(), "/src", db, "actions"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	for _, want := range []string{
		"database", "create", "--language=actions", "--source-root=/src", db,
	} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

// A stale directory would make codeql refuse to write, so re-analyzing a drifted dependency has
// to start from a clean location.
func TestCreateDatabaseReplacesExistingDirectory(t *testing.T) {
	stub, _ := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	db := filepath.Join(t.TempDir(), "db")
	if err := os.MkdirAll(db, 0o755); err != nil {
		t.Fatalf("seeding directory: %v", err)
	}
	stale := filepath.Join(db, "stale.txt")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	if err := r.CreateDatabase(context.Background(), "/src", db, "actions"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale database contents survived")
	}
}

func TestCreateDatabasePassesResourceLimits(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub, Threads: 3, RAMMB: 2048}

	db := filepath.Join(t.TempDir(), "db")
	if err := r.CreateDatabase(context.Background(), "/src", db, "actions"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"--threads=3", "--ram=2048"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

// The config file decides which paths enter the database, so selfcheck and Code Scanning see the
// same source set. Losing or misspelling the option analyzes a different tree and still exits 0,
// which reads as a clean result rather than as a question nobody asked.
func TestCreateDatabasePassesTheCodeScanningConfig(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub, CodeScanningConfig: ".github/codeql/codeql-config.yml"}

	db := filepath.Join(t.TempDir(), "db")
	if err := r.CreateDatabase(context.Background(), "/src", db, "actions"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	args := string(mustRead(t, argsFile))
	if !strings.Contains(args, "--codescanning-config=.github/codeql/codeql-config.yml") {
		t.Errorf("args %q missing the Code Scanning config", args)
	}
}

// Unset means the CLI's own default. An empty value would name a config file of "", which it
// reports as missing, so every caller that never set one would stop building databases.
func TestAnUnsetCodeScanningConfigIsNotPassedAtAll(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	db := filepath.Join(t.TempDir(), "db")
	if err := r.CreateDatabase(context.Background(), "/src", db, "actions"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if args := string(mustRead(t, argsFile)); strings.Contains(args, "--codescanning-config") {
		t.Errorf("args %q names a config file that was never chosen", args)
	}
}

// The extractor indexes workflows only at the source root, so a staged reusable workflow is absent
// unless an include reaches it. Replacing the variable instead of appending would drop every
// `action.yml` the defaults include, and an inherited value is somebody else's configuration.
func TestAnIndexIncludeIsAppendedToTheFiltersTheExtractorReads(t *testing.T) {
	stub, _ := stubCodeQL(t, "printf '%s' \"$LGTM_INDEX_FILTERS\" > \"$(dirname \"$0\")/filters.txt\"\nexit 0\n")
	t.Setenv("LGTM_INDEX_FILTERS", "exclude:vendor/**")
	r := &Runner{CodeQLPath: stub}

	db := filepath.Join(t.TempDir(), "db")
	if err := r.CreateDatabaseIndexing(context.Background(), "/src", db, "actions",
		[]string{".github/workflows/external/**/*.yml", ".github/workflows/external/**/*.yaml"}); err != nil {
		t.Fatalf("CreateDatabaseIndexing: %v", err)
	}

	got := string(mustRead(t, filepath.Join(filepath.Dir(stub), "filters.txt")))
	want := "exclude:vendor/**\n" +
		"include:.github/workflows/external/**/*.yml\n" +
		"include:.github/workflows/external/**/*.yaml"
	if got != want {
		t.Errorf("LGTM_INDEX_FILTERS = %q, want %q", got, want)
	}
}

// A code scanning configuration rewrites the index filters itself, so the two together would let
// one silently replace the other and the staged workflows would vanish without an error.
func TestAnIndexIncludeRefusesACodeScanningConfig(t *testing.T) {
	stub, _ := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub, CodeScanningConfig: ".github/codeql/codeql-config.yml"}

	err := r.CreateDatabaseIndexing(context.Background(), "/src", filepath.Join(t.TempDir(), "db"),
		"actions", []string{".github/workflows/external/**/*.yml"})
	if err == nil {
		t.Fatal("an index include was combined with a code scanning configuration")
	}
}

// A repository with no workflows and no action manifest yields no database, and that is a
// permanent answer rather than a failure to retry.
func TestCreateDatabaseReportsNoSourceCode(t *testing.T) {
	stub, _ := stubCodeQL(t,
		"echo 'No source code was seen during the build' >&2\nexit 32\n")
	r := &Runner{CodeQLPath: stub}

	err := r.CreateDatabase(context.Background(), "/src", filepath.Join(t.TempDir(), "db"), "actions")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsNoSourceCode(err) {
		t.Errorf("IsNoSourceCode(%v) = false, want true", err)
	}
}

// Tracing a build relaunches it through a copy of a system shell, which an arm64 Mac without
// Rosetta cannot start ("Bad CPU type in executable"), so every extractor that can read source
// alone is asked to, and no database ABOM creates waits on a build it does not need.
func TestADatabaseIsCreatedFromSourceAloneWhereTheExtractorCanBe(t *testing.T) {
	for _, language := range []string{"actions", "csharp", "javascript", "python"} {
		t.Run(language, func(t *testing.T) {
			stub, argsFile := stubCodeQL(t, "exit 0\n")
			r := &Runner{CodeQLPath: stub}

			db := filepath.Join(t.TempDir(), "db")
			if err := r.CreateDatabase(context.Background(), "/src", db, language); err != nil {
				t.Fatalf("CreateDatabase: %v", err)
			}

			args := strings.Fields(string(mustRead(t, argsFile)))
			if !slices.Contains(args, "--build-mode=none") {
				t.Errorf("args %q build a %s database some other way than from source alone", args, language)
			}
		})
	}
}

// Go's extractor has no build mode none, but its autobuilder extracts the packages itself and the
// tracer is told to leave it alone, so it runs untraced and the database is the same.
func TestAGoDatabaseIsExtractedByItsAutobuilderWithoutTheTracer(t *testing.T) {
	stub, callsFile := stubCodeQLCalls(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub, Threads: 3, RAMMB: 2048, CodeScanningConfig: "cfg.yml"}

	db := filepath.Join(t.TempDir(), "db")
	if err := r.CreateDatabase(context.Background(), "/src", db, "go"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	want := []string{
		"database init --language=go --source-root=/src --build-mode=autobuild --overwrite " +
			"--codescanning-config=cfg.yml " + db,
		"database trace-command --no-tracing --use-build-mode --threads=3 --ram=2048 " + db,
		"database finalize --threads=3 --ram=2048 " + db,
	}
	if got := recordedCalls(t, callsFile); !slices.Equal(got, want) {
		t.Errorf("calls =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A Go tree with nothing to extract is refused when the database is finalized, and that is the
// same permanent answer a single `database create` gives.
func TestAGoTreeWithNothingToExtractIsReportedAsNoSourceCode(t *testing.T) {
	stub, _ := stubCodeQLCalls(t,
		"case \"$2\" in finalize) echo 'No source code was seen during the build' >&2; exit 32;; esac\nexit 0\n")
	r := &Runner{CodeQLPath: stub}

	err := r.CreateDatabase(context.Background(), "/src", filepath.Join(t.TempDir(), "db"), "go")
	if !IsNoSourceCode(err) {
		t.Errorf("IsNoSourceCode(%v) = false, want true", err)
	}
}

// A step that fails leaves nothing for the next to work on, so it stops there and says why.
func TestAGoDatabaseStopsAtTheStepThatFailed(t *testing.T) {
	stub, callsFile := stubCodeQLCalls(t,
		"case \"$2\" in init) echo 'disk full' >&2; exit 1;; esac\nexit 0\n")
	r := &Runner{CodeQLPath: stub}

	err := r.CreateDatabase(context.Background(), "/src", filepath.Join(t.TempDir(), "db"), "go")
	if err == nil || !strings.Contains(err.Error(), "disk full") || IsNoSourceCode(err) {
		t.Fatalf("CreateDatabase = %v, want the init failure and its message", err)
	}
	if got := recordedCalls(t, callsFile); len(got) != 1 || !strings.HasPrefix(got[0], "database init ") {
		t.Errorf("calls = %q, want only the init that failed", got)
	}
}

// stubCodeQLCalls records every invocation, one line each, where stubCodeQL keeps only the last.
func stubCodeQLCalls(t *testing.T, script string) (path, callsFile string) {
	t.Helper()
	path, _ = stubCodeQL(t, "echo \"$@\" >> \"$(dirname \"$0\")/calls.txt\"\n"+script)
	return path, filepath.Join(filepath.Dir(path), "calls.txt")
}

func recordedCalls(t *testing.T, callsFile string) []string {
	t.Helper()
	return strings.Split(strings.TrimSpace(string(mustRead(t, callsFile))), "\n")
}

func TestCreateDatabasePropagatesFailure(t *testing.T) {
	stub, _ := stubCodeQL(t, "echo 'disk full' >&2\nexit 1\n")
	r := &Runner{CodeQLPath: stub}

	err := r.CreateDatabase(context.Background(), "/src", filepath.Join(t.TempDir(), "db"), "actions")
	if err == nil {
		t.Fatal("expected an error")
	}
	if IsNoSourceCode(err) {
		t.Error("a generic failure must not be reported as empty source")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error %v does not carry the CLI message", err)
	}
}
