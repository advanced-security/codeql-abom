package expand

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// The reasons a declaration goes unread rest on two things only the CLI decides: a local pack
// answers a reference to any version of itself, and a pack it has nowhere fails as one that
// "cannot be found". The fakes know both only because this pins them.
func TestTheCLIReadsADeclarationOnlyFromTheFileThatIsTheVersion(t *testing.T) {
	if os.Getenv("ABOM_ITEST_CODEQL") == "" {
		t.Skip("set ABOM_ITEST_CODEQL=1 to read declarations with the CodeQL CLI")
	}
	// The runner finds this repository's packs relative to the working directory.
	t.Chdir("../..")
	query, err := filepath.Abs("ql/actions/graph-extraction/src/AbomEdges.ql")
	if err != nil {
		t.Fatal(err)
	}
	pack, packVersion, _ := runner.PackOf(query)
	current := versionOf(t, query)
	unread := []store.CatalogQuery{
		{QueryID: "abom/edges", QueryVersion: current, Pack: pack, PackVersion: packVersion},
		{QueryID: "abom/edges", QueryVersion: "edited-since", Pack: pack, PackVersion: "0.0.1"},
		{QueryID: "acme/never", QueryVersion: "v1", Pack: "acme/no-such-pack", PackVersion: "9.9.9"},
	}
	s := declaredStore(t)

	unreadable, err := ReadDeclarations(context.Background(), s, &runner.Runner{}, unread)
	if err != nil {
		t.Fatal(err)
	}
	if id := declaredRows(t, s, unread[:1])["abom/edges\x00"+current].Metadata["id"]; id != "abom/edges" {
		t.Errorf("the version on disk declares id %q, want abom/edges", id)
	}
	why := unreadable["abom/edges\x00edited-since"]
	if !strings.Contains(why, pack+" 0.0.1 is not on disk, and no file of "+packVersion+" is this version") {
		t.Errorf("reason %q should say 0.0.1 is not on disk and name the %s that is", why, packVersion)
	}
	if why := unreadable["acme/never\x00v1"]; why != "Declared metadata cannot be read: acme/no-such-pack 9.9.9 is not on disk." {
		t.Errorf("a pack the CLI has nowhere reads %q, want it named as not on disk", why)
	}
}
