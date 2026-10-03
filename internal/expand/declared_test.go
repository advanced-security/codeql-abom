package expand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// fakeDeclarations answers as the CLI does over a set of packs laid out on disk: a reference it
// was not given is a pack that is not there, and a query declares the `@` pairs in its own text.
type fakeDeclarations struct {
	packs     map[string][]string
	noCodeQL  error
	resolved  []string
	declaring []string
}

func (f *fakeDeclarations) Version(context.Context) (string, error) {
	if f.noCodeQL != nil {
		return "", f.noCodeQL
	}
	return "2.27.1", nil
}

func (f *fakeDeclarations) ResolveQueries(_ context.Context, ref string) ([]string, error) {
	f.resolved = append(f.resolved, ref)
	paths, ok := f.packs[ref]
	if !ok {
		return nil, fmt.Errorf("codeql resolve queries %s: Query pack %s cannot be found", ref, ref)
	}
	return paths, nil
}

func (f *fakeDeclarations) ResolveMetadata(_ context.Context, path string) (runner.Metadata, error) {
	f.declaring = append(f.declaring, path)
	body, err := os.ReadFile(path)
	if err != nil {
		return runner.Metadata{}, err
	}
	pairs := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		_, tag, ok := strings.Cut(line, "@")
		if !ok {
			continue
		}
		name, value, _ := strings.Cut(tag, " ")
		pairs[name] = strings.TrimSpace(value)
	}
	raw, _ := json.Marshal(pairs)
	var md runner.Metadata
	return md, json.Unmarshal(raw, &md)
}

// declaredPack lays out a pack the way the package cache holds one, returning each query's path.
func declaredPack(t *testing.T, name, version string, queries map[string]string) map[string]string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), filepath.FromSlash(name), version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("name: %s\nversion: %s\n", name, version)
	if err := os.WriteFile(filepath.Join(dir, "qlpack.yml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for file, text := range queries {
		path := filepath.Join(dir, file)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		out[file] = path
	}
	return out
}

func declaredQuery(id, score string) string {
	return fmt.Sprintf("/**\n * @name %s\n * @id %s\n * @kind problem\n * @security-severity %s\n */\nselect 1\n",
		id, id, score)
}

func versionOf(t *testing.T, path string) string {
	t.Helper()
	v, err := runner.QueryVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func declaredStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	return s
}

// declaredRows lists each version in the catalog as a working copy would, which needs no ledger,
// and answers what the catalog then holds for each.
func declaredRows(t *testing.T, s *store.Store, rows []store.CatalogQuery) map[string]store.CatalogQuery {
	t.Helper()
	working := make([]store.WorkingQuery, 0, len(rows))
	for _, q := range rows {
		working = append(working, store.WorkingQuery{QueryID: q.QueryID, QueryVersion: q.QueryVersion, Pack: q.Pack})
	}
	catalog, err := s.QueryCatalog(working...)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.CatalogQuery{}
	for _, q := range catalog {
		out[q.QueryID+"\x00"+q.QueryVersion] = q
	}
	return out
}

// A version is the hash of a query and what it imports, so the file hashing to it is the only one
// whose declaration is that version's. The same id at another version sits in the same pack
// version and may declare another severity; reading it instead would report a score under a
// version that never declared it.
func TestADeclarationIsReadOnlyFromTheFileWhoseContentIsThatVersion(t *testing.T) {
	paths := declaredPack(t, "acme/pack", "1.0.0", map[string]string{
		"Rule.ql":  declaredQuery("acme/rule", "8.1"),
		"Other.ql": declaredQuery("acme/other", "3.0"),
	})
	current := versionOf(t, paths["Rule.ql"])
	unread := []store.CatalogQuery{
		{QueryID: "acme/rule", QueryVersion: current, Pack: "acme/pack", PackVersion: "1.0.0"},
		{QueryID: "acme/rule", QueryVersion: "edited-since", Pack: "acme/pack", PackVersion: "1.0.0"},
	}
	fake := &fakeDeclarations{packs: map[string][]string{
		"acme/pack@1.0.0:.": {paths["Rule.ql"], paths["Other.ql"]},
		"acme/pack:.":       {paths["Rule.ql"], paths["Other.ql"]},
	}}
	s := declaredStore(t)

	unreadable, err := ReadDeclarations(context.Background(), s, fake, unread)
	if err != nil {
		t.Fatal(err)
	}

	got := declaredRows(t, s, unread)
	if score := got["acme/rule\x00"+current].Metadata["security-severity"]; score != "8.1" {
		t.Errorf("the version on disk declares security-severity %q, want 8.1", score)
	}
	if md := got["acme/rule\x00edited-since"].Metadata; md != nil {
		t.Errorf("a version whose content no file on disk has was given another version's declaration: %v", md)
	}
	if why := unreadable["acme/rule\x00edited-since"]; why != "Declared metadata cannot be read: no file of acme/pack 1.0.0 on disk is this version." {
		t.Errorf("a version with no file says %q, want it to say no file of the pack version it ran from is it", why)
	}
	// Every version asked about ends read or named, and never both.
	for _, q := range unread {
		key := q.QueryID + "\x00" + q.QueryVersion
		_, named := unreadable[key]
		if read := got[key].Metadata != nil; read == named {
			t.Errorf("%s: read %v, named %v; exactly one must hold", key, read, named)
		}
	}
}

// A pack version that is not installed is the one remedy a reader has, so it is the reason given
// rather than a general "not found".
func TestAPackVersionThatIsNotOnDiskIsNamedAsTheReason(t *testing.T) {
	paths := declaredPack(t, "acme/pack", "1.0.0", map[string]string{
		"Rule.ql": declaredQuery("acme/rule", "8.1"),
	})
	unread := []store.CatalogQuery{
		{QueryID: "acme/rule", QueryVersion: "ran-from-nine", Pack: "acme/pack", PackVersion: "9.9.9"},
	}
	fake := &fakeDeclarations{packs: map[string][]string{"acme/pack:.": {paths["Rule.ql"]}}}

	unreadable, err := ReadDeclarations(context.Background(), declaredStore(t), fake, unread)
	if err != nil {
		t.Fatal(err)
	}
	if why := unreadable["acme/rule\x00ran-from-nine"]; !strings.Contains(why, "acme/pack 9.9.9 is not on disk") {
		t.Errorf("reason %q does not name the pack version that is missing", why)
	}
}

// A local pack answers a reference to any version of itself with the version it is, so a pack
// version resolving is not evidence it is on disk: the reason names what the files say they are.
func TestAPackVersionIsOnlyCalledPresentWhenItsFilesSaySo(t *testing.T) {
	paths := declaredPack(t, "acme/local", "2.27.1", map[string]string{
		"Rule.ql": declaredQuery("acme/rule", "8.1"),
	})
	unread := []store.CatalogQuery{
		{QueryID: "acme/rule", QueryVersion: "ran-from-2.27.0", Pack: "acme/local", PackVersion: "2.27.0"},
	}
	fake := &fakeDeclarations{packs: map[string][]string{
		"acme/local@2.27.0:.": {paths["Rule.ql"]},
		"acme/local:.":        {paths["Rule.ql"]},
	}}

	unreadable, err := ReadDeclarations(context.Background(), declaredStore(t), fake, unread)
	if err != nil {
		t.Fatal(err)
	}
	why := unreadable["acme/rule\x00ran-from-2.27.0"]
	if why != "Declared metadata cannot be read: acme/local 2.27.0 is not on disk, and no file of 2.27.1 is this version." {
		t.Errorf("reason %q should say 2.27.0 is not on disk and name the 2.27.1 that is", why)
	}
}

// A working copy, or a version whose pack version moved on, is found where the search path puts
// its pack now, provided the content there is still that version.
func TestAVersionIsFoundWhereverItsPackIsOnTheSearchPath(t *testing.T) {
	paths := declaredPack(t, "acme/custom", "0.0.2", map[string]string{
		"Mine.ql": declaredQuery("acme/mine", "5.5"),
		"Kept.ql": declaredQuery("acme/kept", "6.5"),
	})
	mine, kept := versionOf(t, paths["Mine.ql"]), versionOf(t, paths["Kept.ql"])
	unread := []store.CatalogQuery{
		{QueryID: "acme/mine", QueryVersion: mine, Pack: "acme/custom"},
		{QueryID: "acme/kept", QueryVersion: kept, Pack: "acme/custom", PackVersion: "0.0.1"},
	}
	fake := &fakeDeclarations{packs: map[string][]string{
		"acme/custom:.": {paths["Mine.ql"], paths["Kept.ql"]},
	}}
	s := declaredStore(t)

	unreadable, err := ReadDeclarations(context.Background(), s, fake, unread)
	if err != nil {
		t.Fatal(err)
	}
	if len(unreadable) != 0 {
		t.Errorf("versions whose content is on the search path went unread: %v", unreadable)
	}
	got := declaredRows(t, s, unread)
	if score := got["acme/mine\x00"+mine].Metadata["security-severity"]; score != "5.5" {
		t.Errorf("the working copy declares security-severity %q, want 5.5", score)
	}
	if score := got["acme/kept\x00"+kept].Metadata["security-severity"]; score != "6.5" {
		t.Errorf("the version the pack carried forward declares security-severity %q, want 6.5", score)
	}
}

// Reading is once per pack reference, and the CLI is asked what a query declares only for files
// that are a version somebody listed: a pack holds tens of queries and a sweep lists a handful.
func TestReadingDeclarationsResolvesEachPackOnceAndReadsOnlyTheFilesItNeeds(t *testing.T) {
	paths := declaredPack(t, "acme/pack", "1.0.0", map[string]string{
		"A.ql": declaredQuery("acme/a", "1.0"),
		"B.ql": declaredQuery("acme/b", "2.0"),
		"C.ql": declaredQuery("acme/c", "3.0"),
	})
	all := []string{paths["A.ql"], paths["B.ql"], paths["C.ql"]}
	unread := []store.CatalogQuery{
		{QueryID: "acme/a", QueryVersion: versionOf(t, paths["A.ql"]), Pack: "acme/pack", PackVersion: "1.0.0"},
		{QueryID: "acme/b", QueryVersion: versionOf(t, paths["B.ql"]), Pack: "acme/pack", PackVersion: "1.0.0"},
	}
	fake := &fakeDeclarations{packs: map[string][]string{"acme/pack@1.0.0:.": all, "acme/pack:.": all}}

	if _, err := ReadDeclarations(context.Background(), declaredStore(t), fake, unread); err != nil {
		t.Fatal(err)
	}
	if want := []string{"acme/pack@1.0.0:."}; !slices.Equal(fake.resolved, want) {
		t.Errorf("resolved %v, want %v: every version was found at the pack version it ran from", fake.resolved, want)
	}
	slices.Sort(fake.declaring)
	if want := []string{paths["A.ql"], paths["B.ql"]}; !slices.Equal(fake.declaring, want) {
		t.Errorf("read the declarations of %v, want only %v", fake.declaring, want)
	}
}

// Without a CLI every pack reads as missing, and saying "not on disk" about all of them would be
// a statement about the disk that is really about the CLI.
func TestDeclarationsAreNotReadWithoutACodeQLToReadThem(t *testing.T) {
	fake := &fakeDeclarations{noCodeQL: errors.New("codeql: executable file not found in $PATH")}
	unread := []store.CatalogQuery{{QueryID: "acme/a", QueryVersion: "v1", Pack: "acme/pack", PackVersion: "1.0.0"}}

	unreadable, err := ReadDeclarations(context.Background(), declaredStore(t), fake, unread)
	if err == nil {
		t.Fatalf("read declarations with no CodeQL and named %v", unreadable)
	}
	if len(fake.resolved) != 0 {
		t.Errorf("asked a CLI that cannot run to resolve %v", fake.resolved)
	}
}
