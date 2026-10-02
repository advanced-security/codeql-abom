package expand

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/bqrs"
	"github.com/advanced-security/codeql-abom/internal/closure"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

// What codeql/actions-all resolves in a database the stager laid out, built by `database create`,
// which alone applies index filters: `codeql test run` extracts every YAML file and cannot show
// either of the two things this pins as load-bearing.
func TestAStagedClosureResolvesOnlyWithItsIncludeAndItsAnchor(t *testing.T) {
	if os.Getenv("ABOM_ITEST_CODEQL") == "" {
		t.Skip("set ABOM_ITEST_CODEQL=1 to build closure databases with the CodeQL CLI")
	}
	files := closureFiles(t)
	members := func(t *testing.T) []closure.Member {
		var out []closure.Member
		for _, m := range []struct{ repository, uses string }{
			{"vendor/comp", "vendor/comp"},
			{"vendor/inner", "vendor/inner"},
			{"vendor/rw", "vendor/rw/.github/workflows/reuse.yml"},
		} {
			out = append(out, closure.Member{Repository: m.repository, Uses: m.uses,
				Commit: closureCommits[m.repository], Tree: closureCheckout(t, files[m.repository])})
		}
		return out
	}
	r := &runner.Runner{}
	query, err := filepath.Abs("../../ql/actions/graph-extraction/src/AbomResolvedCallees.ql")
	if err != nil {
		t.Fatal(err)
	}
	resolved := func(t *testing.T, root closure.Root, includes []string, anchor bool) map[string]string {
		t.Helper()
		layout, _, err := closure.Plan(root, members(t))
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		layout.Anchored = layout.Anchored && anchor
		dir := t.TempDir()
		src := filepath.Join(dir, "src")
		if err := layout.Write(src); err != nil {
			t.Fatalf("Write: %v", err)
		}
		db := filepath.Join(dir, "db")
		if err := r.CreateDatabaseIndexing(context.Background(), src, db, "actions", includes); err != nil {
			t.Fatalf("CreateDatabaseIndexing: %v", err)
		}
		out := filepath.Join(dir, "calls.bqrs")
		if err := r.RunQuery(context.Background(), db, query, out); err != nil {
			t.Fatalf("RunQuery: %v", err)
		}
		decoded := filepath.Join(dir, "calls.json")
		if err := r.Decode(context.Background(), out, decoded); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		f, err := os.Open(decoded)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		calls := map[string]string{}
		if _, err := bqrs.StreamSelect(f, func(row bqrs.Row) error {
			name, _ := row.String("callName")
			callee, _ := row.String("calleePath")
			calls[name] = callee
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return calls
	}
	owned := closure.Root{Repository: "octo/app", Commit: closureCommits["octo/app"],
		Tree: closureCheckout(t, files["octo/app"])}

	calls := resolved(t, owned, closure.IndexIncludes, true)
	for name, want := range map[string]string{
		"vendor/comp":                           ".github/actions/external/vendor/comp/action.yml",
		"vendor/inner":                          ".github/actions/external/vendor/inner/action.yml",
		"vendor/rw/.github/workflows/reuse.yml": ".github/workflows/external/vendor/rw/.github/workflows/reuse.yml",
		"actions/checkout":                      "",
	} {
		if calls[name] != want {
			t.Errorf("%s resolved to %q, want %q", name, calls[name], want)
		}
	}
	if without := resolved(t, owned, nil, true); without["vendor/rw/.github/workflows/reuse.yml"] != "" {
		t.Error("the staged reusable workflow resolved without the include, so the include " +
			"is no longer what makes it visible")
	}

	// A repository that is itself an action holds no workflow, so there is no repository root.
	action := closure.Root{Repository: "octo/action", Commit: closureCommits["octo/app"],
		Tree: closureCheckout(t, map[string]string{"action.yml": "name: wrapper\ninputs:\n  title: {}\n" +
			"runs:\n  using: composite\n  steps:\n    - uses: vendor/comp@v1\n      with:\n" +
			"        title: ${{ inputs.title }}\n"})}
	if anchored := resolved(t, action, closure.IndexIncludes, true); anchored["vendor/comp"] == "" {
		t.Error("an anchored action repository still resolves nothing")
	}
	if bare := resolved(t, action, closure.IndexIncludes, false); bare["vendor/comp"] != "" {
		t.Error("an action repository resolved without an anchor, so the anchor is no longer " +
			"load-bearing and the synthesized file is noise")
	}
}

func closureCheckout(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
