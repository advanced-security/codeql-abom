package serve

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/querypack"
	"github.com/advanced-security/codeql-abom/internal/store"
)

type componentsShown struct {
	querypack.Manifest
	NewestTest *store.QueryTest `json:"newestTest"`
}

// A reader sees everything that makes a query run, what is missing from it, and the newest test run
// on record with the CodeQL that ran it; never-tested is null, not a run that found nothing.
func TestAQuerysComponentsAreServedWithItsNewestTestRun(t *testing.T) {
	s, st := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A", "B"})

	rec := get(t, s, "/api/queries/components?query=acme/a")
	var shown componentsShown
	if err := json.Unmarshal(rec.Body.Bytes(), &shown); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("components answered %d: %s", rec.Code, rec.Body.String())
	}
	paths := map[string]string{}
	for _, c := range shown.Components {
		paths[c.Path] = c.Role
	}
	if paths["src/A.ql"] != querypack.RoleQuery || paths["test/A/A.qlref"] != querypack.RoleQLRef ||
		paths["src/B.ql"] != "" {
		t.Errorf("components = %v", paths)
	}
	if !slices.ContainsFunc(shown.Missing, func(m querypack.Missing) bool { return m.Path == "src/A.md" }) {
		t.Errorf("missing = %+v, want the help named", shown.Missing)
	}
	if shown.NewestTest != nil {
		t.Errorf("a query nobody tested has a newest run: %+v", shown.NewestTest)
	}

	if err := st.RecordQueryTest(store.QueryTest{QueryID: "acme/a", QueryVersion: shown.Version,
		CLIVersion: "2.27.1", Passed: true, Total: 1}); err != nil {
		t.Fatal(err)
	}
	rec = get(t, s, "/api/queries/components?query=acme/a")
	if err := json.Unmarshal(rec.Body.Bytes(), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.NewestTest == nil || shown.NewestTest.CLIVersion != "2.27.1" || shown.NewestTest.QueryVersion != shown.Version {
		t.Errorf("newest test = %+v, want the run just recorded", shown.NewestTest)
	}

	if rec := get(t, s, "/api/queries/components?query=acme/nobody"); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown query answered %d", rec.Code)
	}
	s.QueryDir = ""
	if rec := get(t, s, "/api/queries/components?query=acme/a"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a server with no query directory answered %d", rec.Code)
	}
}

// One component opens by its path, and only a path the query's components name: a reader asking for
// anything else of the directory, or outside it, is told it is not one of them.
func TestOnlyAComponentOfTheQueryOpens(t *testing.T) {
	s, _ := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A", "B"})

	rec := get(t, s, "/api/queries/component?query=acme/a&path=src/A.ql")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "@id acme/a") {
		t.Errorf("the query file answered %d: %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"src/B.ql", "src/README.md", "../../etc/passwd", "codeql-suites/abom-deployed.qls"} {
		if rec := get(t, s, "/api/queries/component?query=acme/a&path="+path); rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}

// The bundle downloads as an archive whose digest is the one the components named, so a reader can
// check what they took away against what they were shown: `gunzip -c bundle.tar.gz | sha256sum`.
func TestTheBundleDownloadsWithTheDigestItWasShownWith(t *testing.T) {
	s, _ := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A"})
	var shown componentsShown
	if err := json.Unmarshal(get(t, s, "/api/queries/components?query=acme/a").Body.Bytes(), &shown); err != nil {
		t.Fatal(err)
	}

	rec := get(t, s, "/api/queries/bundle?query=acme/a")
	if rec.Code != http.StatusOK {
		t.Fatalf("bundle answered %d: %s", rec.Code, rec.Body.String())
	}
	unzipped, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	archive, err := io.ReadAll(unzipped)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	if digest != shown.BundleSHA256 || rec.Header().Get("X-Abom-Bundle-Sha256") != digest {
		t.Errorf("bundle %s, header %s, shown %s", digest, rec.Header().Get("X-Abom-Bundle-Sha256"), shown.BundleSHA256)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment; filename=") || !strings.Contains(cd, ".tar.gz") {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

// A query whose own file cannot be read has no bundle to download, which is said rather than served.
// A symbolic link is listed as a query and refused as a component, since the bundle never follows one.
func TestAQueryWhoseFileCannotBeReadHasNoBundleToDownload(t *testing.T) {
	s, _ := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A"})
	query := filepath.Join(s.QueryDir, "src", "A.ql")
	body, err := os.ReadFile(query)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "A.ql")
	if err := os.WriteFile(elsewhere, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(query); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, query); err != nil {
		t.Fatal(err)
	}

	rec := get(t, s, "/api/queries/bundle?query=acme/a")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "src/A.ql") {
		t.Errorf("bundle of a query whose file is refused answered %d: %s", rec.Code, rec.Body.String())
	}
}
