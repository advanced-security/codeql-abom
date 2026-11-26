package publish

import (
	"errors"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/querypack"
)

// A pull request says what is known of the query's tests: an unread record is not a run that never
// happened, and neither is a passing run.
func TestAnExportSaysWhatIsKnownOfItsTests(t *testing.T) {
	m := querypack.Manifest{Query: "acme/a", Version: "1.0.0", BundleSHA256: strings.Repeat("b", 64)}
	files := []querypack.File{{Component: querypack.Component{Path: "src/A.ql", Role: "query", SHA256: strings.Repeat("c", 64)}}}
	e := EvidenceOf(m, Target{Owner: "acme", Repo: "queries"}, files)
	for want, tests := range map[string]Tests{
		"could not be read (store closed)":  {Unread: errors.New("store closed")},
		"3 of 4 passed under CodeQL 2.27.6": {Ran: true, Passed: 3, Total: 4, CLIVersion: "2.27.6"},
		"not run at this version":           {},
	} {
		export, err := Compose(m, e, files, "sha256:d", "octocat", tests)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(export.Body, "- Tests: ") || !strings.Contains(export.Body, want) {
			t.Errorf("tests %+v: the body does not say %q:\n%s", tests, want, export.Body)
		}
	}
}
