package lockfile

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A reading is trusted for as long as its reader matches, so a reader that forgot to change when
// the parser did would keep every recorded answer from before the upgrade. The parser half of the
// name is checked against the release go.mod actually builds with.
func TestTheReaderNamesTheParserReleaseTheBuildUses(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	required := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(parserModule) + `\s+(\S+)`).
		FindSubmatch(body)
	if required == nil {
		t.Fatalf("go.mod does not require %s", parserModule)
	}
	if want := parserModule + "@" + string(required[1]); !strings.HasPrefix(Reader, want+" ") {
		t.Errorf("Reader = %q, want it to begin with %q", Reader, want)
	}
}
