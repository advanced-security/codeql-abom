package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Accepting makes a report the expected result, so it names exactly the tests a person approved,
// and accepting nothing is refused rather than read by the CLI as every test it can find.
func TestAcceptingNamesOnlyTheTestsItWasGiven(t *testing.T) {
	stub, _ := stubCodeQL(t, `printf '%s\n' "$@" > "$(dirname "$0")/argv.txt"`+"\n")
	r := &Runner{CodeQLPath: stub}

	if err := r.AcceptTests(context.Background(), []string{"/w/test/A/A.qlref"}); err != nil {
		t.Fatalf("AcceptTests: %v", err)
	}
	args, err := os.ReadFile(filepath.Join(filepath.Dir(stub), "argv.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSpace(string(args)), "\n"); strings.Join(got, "|") !=
		"test|accept|/w/test/A/A.qlref" {
		t.Errorf("args %q", got)
	}
	if err := r.AcceptTests(context.Background(), nil); err == nil {
		t.Error("accepting no test was handed to the CLI")
	}
}

func TestAFailedAcceptSaysWhatTheCLISaid(t *testing.T) {
	stub, _ := stubCodeQL(t, "echo 'A.actual does not exist' >&2\nexit 2\n")
	r := &Runner{CodeQLPath: stub}

	err := r.AcceptTests(context.Background(), []string{"/w/test/A/A.qlref"})
	if err == nil || !strings.Contains(err.Error(), "A.actual does not exist") {
		t.Errorf("err %v, want what the CLI said", err)
	}
}
