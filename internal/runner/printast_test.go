package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// printASTStub answers as the CLI does for the three calls a print makes, and keeps what it was
// handed: the fixtures named in the data extension, and each call's arguments.
const printASTStub = `
dir="$(dirname "$0")"
printf '%s\n' "$@" >> "$dir/calls.txt"
echo "--" >> "$dir/calls.txt"
case "$1 $2" in
"resolve queries") echo '["/packs/ql-mcp-actions-tools-src/2.27.1/PrintAST/PrintAST.ql"]' ;;
"query run")
  for a in "$@"; do
    case "$a" in
    --output=*) : > "${a#--output=}" ;;
    --additional-packs=*) cp "${a#--additional-packs=}/ext/sources.model.yml" "$dir/sources.yml" ;;
    esac
  done ;;
"bqrs interpret")
  for a in "$@"; do
    case "$a" in
    --output=*) mkdir -p "${a#--output=}/actions/tools"
      printf '#    1| [WorkflowImpl] name: ci\n' > "${a#--output=}/actions/tools/print-ast.txt" ;;
    esac
  done ;;
esac
`

// A tree ABOM prints is the one a session would have read: the same pinned query, told which
// fixtures to print through the data extension ql-mcp writes, and interpreted as the same graph text.
func TestPrintingATreeRunsThePinnedQueryOverTheNamedFixtures(t *testing.T) {
	stub, _ := stubCodeQL(t, printASTStub)
	r := &Runner{CodeQLPath: stub}

	got, err := r.PrintAST(context.Background(), "/w/test/A/A.testproj",
		[]string{".github/workflows/ci.yml", "action.yml"})
	if err != nil {
		t.Fatalf("PrintAST: %v", err)
	}
	if !strings.Contains(got.Body, "[WorkflowImpl] name: ci") || got.Truncated {
		t.Errorf("printed %+v", got)
	}
	dir := filepath.Dir(stub)
	sources, err := os.ReadFile(filepath.Join(dir, "sources.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"advanced-security/ql-mcp-actions-tools-src", "selectedSourceFiles",
		`".github/workflows/ci.yml"`, `"action.yml"`} {
		if !strings.Contains(string(sources), want) {
			t.Errorf("the data extension does not name %s:\n%s", want, sources)
		}
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{ToolsPack + ":PrintAST/PrintAST.ql", "--database=/w/test/A/A.testproj",
		"--model-packs=abom/print-ast-sources@0.0.0",
		"/packs/ql-mcp-actions-tools-src/2.27.1/PrintAST/PrintAST.ql", "--format=graphtext",
		"-t=id=actions/tools/print-ast"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("no call carried %s:\n%s", want, calls)
		}
	}
}

// Without a fixture the query prints nothing and fails on the missing extension, so nothing to
// print is refused before the CLI is asked.
func TestPrintingATreeOfNothingIsRefused(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}
	if _, err := r.PrintAST(context.Background(), "/w/test/A/A.testproj", nil); err == nil {
		t.Error("printing no fixture was handed to the CLI")
	}
	if _, err := r.PrintAST(context.Background(), "", []string{"ci.yml"}); err == nil {
		t.Error("printing with no database was handed to the CLI")
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Error("the CLI was invoked with nothing to print")
	}
}
