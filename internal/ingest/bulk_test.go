package ingest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func writeSarif(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestResolveGhMrvaFilename(t *testing.T) {
	r := NewRepoResolver([]string{"octo-org/app", "octo-org/service"})

	got, ok := r.Resolve("octo-org_app_21991.sarif")
	if !ok || got != "octo-org/app" {
		t.Errorf("Resolve = %q (ok=%v), want octo-org/app", got, ok)
	}
}

// sarif-sql writes a different convention; both must resolve against the same list.
func TestResolveSarifSqlFilename(t *testing.T) {
	r := NewRepoResolver([]string{"octo-org/app"})

	got, ok := r.Resolve("octo-org-app-github-controller-results.sarif")
	if !ok || got != "octo-org/app" {
		t.Errorf("Resolve = %q (ok=%v), want octo-org/app", got, ok)
	}
}

// Owners and repository names may both contain the separator, so the longest known match wins
// rather than splitting on a fixed position.
func TestResolvePrefersLongestMatch(t *testing.T) {
	r := NewRepoResolver([]string{"octo/app", "octo/app-service"})

	got, ok := r.Resolve("octo_app-service_42.sarif")
	if !ok || got != "octo/app-service" {
		t.Errorf("Resolve = %q (ok=%v), want octo/app-service", got, ok)
	}
}

// Two repositories can sanitise to the same filename. Guessing would silently attribute
// findings to the wrong repository, so genuine ambiguity is reported instead.
func TestResolveReportsAmbiguity(t *testing.T) {
	r := NewRepoResolver([]string{"foo_bar/baz", "foo/bar_baz"})

	if got, ok := r.Resolve("foo_bar_baz_7.sarif"); ok {
		t.Errorf("Resolve = %q, want ambiguity to be reported as unresolved", got)
	}
}

func TestResolveUnknownRepository(t *testing.T) {
	r := NewRepoResolver([]string{"octo-org/app"})

	if got, ok := r.Resolve("other-org_thing_1.sarif"); ok {
		t.Errorf("Resolve = %q, want no match", got)
	}
}

func TestResolveIsCaseInsensitive(t *testing.T) {
	r := NewRepoResolver([]string{"Octo-Org/App"})

	got, ok := r.Resolve("octo-org_app_5.sarif")
	if !ok || got != "Octo-Org/App" {
		t.Errorf("Resolve = %q (ok=%v), want the canonical casing", got, ok)
	}
}

func TestLoadRepoList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	body := `{"actions-top": ["octo-org/app", "octo-org/service"], "other": ["x/y"]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing list: %v", err)
	}

	repos, err := LoadRepoList(path, "actions-top")
	if err != nil {
		t.Fatalf("LoadRepoList: %v", err)
	}
	if len(repos) != 2 || repos[0] != "octo-org/app" {
		t.Errorf("got %v, want the actions-top list", repos)
	}
}

func TestLoadRepoListMissingName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	if err := os.WriteFile(path, []byte(`{"a":["x/y"]}`), 0o644); err != nil {
		t.Fatalf("writing list: %v", err)
	}

	if _, err := LoadRepoList(path, "missing"); err == nil {
		t.Error("expected an error for an unknown list name")
	}
}

// When no list name is given, every repository across all lists is usable.
func TestLoadRepoListAllLists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	if err := os.WriteFile(path,
		[]byte(`{"a":["x/y"],"b":["p/q"]}`), 0o644); err != nil {
		t.Fatalf("writing list: %v", err)
	}

	repos, err := LoadRepoList(path, "")
	if err != nil {
		t.Fatalf("LoadRepoList: %v", err)
	}
	if len(repos) != 2 {
		t.Errorf("got %v, want both lists merged", repos)
	}
}

func TestFindingsFromDirectory(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	dir := t.TempDir()
	writeSarif(t, dir, "octo-org_app_1.sarif", suiteSarif)
	writeSarif(t, dir, "octo-org_service_1.sarif", suiteSarif)
	// Non-SARIF files in a download directory must be ignored, not treated as failures.
	writeSarif(t, dir, "repos.json", `{"a":[]}`)

	resolver := NewRepoResolver([]string{"octo-org/app", "octo-org/service"})
	res, err := ing.FindingsFromDirectory(dir, resolver, Options{})
	if err != nil {
		t.Fatalf("FindingsFromDirectory: %v", err)
	}
	if res.Files != 2 {
		t.Errorf("processed %d files, want 2", res.Files)
	}
	if res.Findings != 4 {
		t.Errorf("stored %d findings, want 4 (2 per file)", res.Findings)
	}

	var repos int
	s.DB().QueryRow(`SELECT COUNT(*) FROM repository`).Scan(&repos)
	if repos != 2 {
		t.Errorf("recorded %d repositories, want 2", repos)
	}
}

// One unresolvable file must not abandon the other 999.
func TestFindingsFromDirectoryReportsUnresolved(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	dir := t.TempDir()
	writeSarif(t, dir, "octo-org_app_1.sarif", suiteSarif)
	writeSarif(t, dir, "unknown-org_mystery_1.sarif", suiteSarif)

	res, err := ing.FindingsFromDirectory(dir, NewRepoResolver([]string{"octo-org/app"}), Options{})
	if err != nil {
		t.Fatalf("FindingsFromDirectory: %v", err)
	}
	if res.Files != 1 {
		t.Errorf("processed %d files, want 1", res.Files)
	}
	if len(res.Unresolved) != 1 || res.Unresolved[0] != "unknown-org_mystery_1.sarif" {
		t.Errorf("unresolved = %v, want the mystery file", res.Unresolved)
	}
}

// A corrupt download must be reported and skipped, not fatal.
func TestFindingsFromDirectoryContinuesPastMalformedFile(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	dir := t.TempDir()
	writeSarif(t, dir, "octo-org_app_1.sarif", suiteSarif)
	writeSarif(t, dir, "octo-org_service_1.sarif", `{"runs":[{"results":[`)

	res, err := ing.FindingsFromDirectory(dir,
		NewRepoResolver([]string{"octo-org/app", "octo-org/service"}), Options{})
	if err != nil {
		t.Fatalf("FindingsFromDirectory: %v", err)
	}
	if res.Files != 1 {
		t.Errorf("processed %d files, want 1", res.Files)
	}
	if len(res.Failed) != 1 {
		t.Errorf("failed = %v, want one entry", res.Failed)
	}
	if res.Findings != 2 {
		t.Errorf("stored %d findings, want the 2 from the good file", res.Findings)
	}
}

func TestFindingsFromDirectoryEmptyIsNotAnError(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	res, err := ing.FindingsFromDirectory(t.TempDir(), NewRepoResolver(nil), Options{})
	if err != nil {
		t.Fatalf("FindingsFromDirectory: %v", err)
	}
	if res.Files != 0 || res.Findings != 0 {
		t.Errorf("got %+v, want an empty result", res)
	}
}

func TestFindingsFromDirectoryMissingDirectory(t *testing.T) {
	s := newTestStore(t)
	ing := New(s, store.NewWriter(s, 0))

	if _, err := ing.FindingsFromDirectory("/nope/missing", NewRepoResolver(nil), Options{}); err == nil {
		t.Error("expected an error for a missing directory")
	}
}
