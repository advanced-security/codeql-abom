package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// What `codeql version --format=json` actually prints, captured from the CLI. It has no
// semanticVersion field, which is the whole reason the fallback exists: a reader who assumed one
// would record an empty version and never say so.
const realVersionJSON = `{
  "productName" : "CodeQL",
  "vendor" : "GitHub",
  "version" : "2.26.4",
  "sha" : "6b1e4dee94adb20f90a671f3fc9e04be32eecf65",
  "branches" : [ "codeql-cli-2.26.4" ],
  "unpackedLocation" : "/Users/x/.local/share/gh/extensions/gh-codeql/dist/release/v2.26.4",
  "configFileFound" : false,
  "overlayVersion" : 4,
  "features" : { "buildModeOption" : true }
}`

func TestTheCliVersionIsReadFromWhatTheCliActuallyPrints(t *testing.T) {
	stub, _ := stubCodeQL(t, "cat <<'EOF'\n"+realVersionJSON+"\nEOF\n")
	r := &Runner{CodeQLPath: stub}

	got, err := r.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got != "2.26.4" {
		t.Errorf("version %q, want 2.26.4", got)
	}
}

// A version that cannot be read must stop the caller, not leave it recording runs whose identity
// silently omits the CodeQL that produced them.
func TestAnUnreadableVersionIsAnError(t *testing.T) {
	stub, _ := stubCodeQL(t, "echo 'not json'\n")
	r := &Runner{CodeQLPath: stub}

	if _, err := r.Version(context.Background()); err == nil {
		t.Fatal("unparseable output reported no error")
	}
}

// The crawl plans once per round and the version cannot change under a running process, so
// paying for a JVM start every time would be a needless several seconds per round.
func TestTheVersionIsAskedForOnce(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "calls")
	stub, _ := stubCodeQL(t,
		"printf x >> "+counter+"\ncat <<'EOF'\n"+realVersionJSON+"\nEOF\n")
	r := &Runner{CodeQLPath: stub}

	for range 3 {
		if _, err := r.Version(context.Background()); err != nil {
			t.Fatalf("Version: %v", err)
		}
	}

	calls, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("reading the call counter: %v", err)
	}
	if len(calls) != 1 {
		t.Errorf("the CLI was asked %d times, want 1", len(calls))
	}
}
