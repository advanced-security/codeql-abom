package closure

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes files under a fresh directory, the shape a sparse checkout leaves on disk.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const (
	workflow   = "on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: vendor/comp@v1\n"
	composite  = "name: comp\nruns:\n  using: composite\n  steps:\n    - run: echo hi\n      shell: bash\n"
	javascript = "name: js\nruns:\n  using: node20\n  main: index.js\n"
	docker     = "name: img\nruns:\n  using: docker\n  image: Dockerfile\n"
	reusable   = "on:\n  workflow_call:\n    inputs:\n      x:\n        type: string\njobs:\n  run:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"
)

func ownedRoot(t *testing.T) Root {
	return Root{Repository: "octo/app", Commit: "aaaa", Tree: tree(t, map[string]string{
		".github/workflows/ci.yml": workflow,
		".git/HEAD":                "ref: refs/heads/main\n",
	})}
}

func stagedPaths(l Layout) map[string]Staged {
	out := map[string]Staged{}
	for _, s := range l.Staged {
		out[s.StagedPath] = s
	}
	return out
}

// codeql/actions-all resolves a `uses:` by string equality against the staged path with the
// external prefix and the manifest name removed, so the path has to be the uses: text byte for
// byte. A composite at a subdirectory keeps the subdirectory, and an action.yaml keeps its name.
func TestACompositeIsStagedWhereItsUsesLineResolves(t *testing.T) {
	layout, skipped, err := Plan(ownedRoot(t), []Member{
		{Repository: "vendor/comp", Uses: "vendor/comp", Commit: "c1",
			Tree: tree(t, map[string]string{"action.yml": composite})},
		{Repository: "vendor/kit", Uses: "vendor/kit/setup/node", Commit: "c2",
			Tree: tree(t, map[string]string{"setup/node/action.yaml": composite})},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped %v, want both composites staged", skipped)
	}
	got := stagedPaths(layout)
	for path, want := range map[string]Staged{
		".github/actions/external/vendor/comp/action.yml": {
			Repository: "vendor/comp", Path: "action.yml", Kind: Composite, Commit: "c1"},
		".github/actions/external/vendor/kit/setup/node/action.yaml": {
			Repository: "vendor/kit", Path: "setup/node/action.yaml", Kind: Composite, Commit: "c2"},
	} {
		s, ok := got[path]
		if !ok {
			t.Errorf("nothing staged at %s; staged %v", path, got)
			continue
		}
		if s.Repository != want.Repository || s.Path != want.Path || s.Kind != want.Kind ||
			s.Commit != want.Commit {
			t.Errorf("%s staged as %+v, want %+v", path, s, want)
		}
	}
}

// A reusable workflow is named by its repository and its own path, and resolves only under
// .github/workflows/external/; the reusable_workflows/ convention resolves nothing.
func TestAReusableWorkflowIsStagedUnderItsRepository(t *testing.T) {
	layout, skipped, err := Plan(ownedRoot(t), []Member{
		{Repository: "vendor/rw", Uses: "vendor/rw/.github/workflows/reuse.yml", Commit: "c3",
			Tree: tree(t, map[string]string{".github/workflows/reuse.yml": reusable})},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped %v", skipped)
	}
	s, ok := stagedPaths(layout)[".github/workflows/external/vendor/rw/.github/workflows/reuse.yml"]
	if !ok {
		t.Fatalf("the reusable workflow was not staged where it resolves: %+v", layout.Staged)
	}
	if s.Kind != ReusableWorkflow || s.Path != ".github/workflows/reuse.yml" {
		t.Errorf("staged as %+v", s)
	}
	for _, staged := range layout.Staged {
		if strings.Contains(staged.StagedPath, "reusable_workflows") {
			t.Errorf("%s uses the convention actions-all does not model", staged.StagedPath)
		}
	}
}

// Only composite actions and reusable workflows are callables. Anything else staged would
// contribute nothing to dataflow, and a workflow that is not callable would put the dependency's
// own CI in front of every query as though the consumer ran it.
func TestOnlyCallablesAreStaged(t *testing.T) {
	layout, skipped, err := Plan(ownedRoot(t), []Member{
		{Repository: "actions/checkout", Uses: "actions/checkout", Commit: "j1",
			Tree: tree(t, map[string]string{"action.yml": javascript})},
		{Repository: "vendor/img", Uses: "vendor/img", Commit: "d1",
			Tree: tree(t, map[string]string{"action.yml": docker})},
		{Repository: "vendor/ci", Uses: "vendor/ci/.github/workflows/ci.yml", Commit: "w1",
			Tree: tree(t, map[string]string{".github/workflows/ci.yml": workflow})},
		{Repository: "vendor/gone", Uses: "vendor/gone/sub", Commit: "g1",
			Tree: tree(t, map[string]string{"action.yml": composite})},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(layout.Staged) != 0 {
		t.Errorf("staged %+v, want nothing", layout.Staged)
	}
	if len(skipped) != 4 {
		t.Fatalf("skipped %d members, want all four named: %+v", len(skipped), skipped)
	}
	for _, s := range skipped {
		if s.Reason == "" {
			t.Errorf("%s was skipped without saying why", s.Uses)
		}
	}
}

// One staged slot holds one commit. Choosing between two by write order would analyze code one
// of the consumers does not run, so the database is refused and both commits are named.
func TestTwoCommitsOfOneStagedCalleeRefuseTheDatabase(t *testing.T) {
	_, _, err := Plan(ownedRoot(t), []Member{
		{Repository: "vendor/comp", Uses: "vendor/comp", Commit: "c1",
			Tree: tree(t, map[string]string{"action.yml": composite})},
		{Repository: "vendor/comp", Uses: "vendor/comp", Commit: "c9",
			Tree: tree(t, map[string]string{"action.yml": composite})},
	})
	if err == nil {
		t.Fatal("two commits of one staged composite were both accepted")
	}
	if !errors.Is(err, ErrCollision) {
		t.Errorf("err = %v, want ErrCollision", err)
	}
	for _, commit := range []string{"c1", "c9"} {
		if !strings.Contains(err.Error(), commit) {
			t.Errorf("the refusal %q does not name %s", err, commit)
		}
	}
}

// Collisions are counted over what is staged. A JavaScript action is never staged, so two majors
// of actions/checkout — the overwhelming majority of version disagreements — refuse nothing.
func TestTwoCommitsOfAnUnstagedActionDoNotCollide(t *testing.T) {
	_, skipped, err := Plan(ownedRoot(t), []Member{
		{Repository: "actions/checkout", Uses: "actions/checkout", Commit: "v3sha",
			Tree: tree(t, map[string]string{"action.yml": javascript})},
		{Repository: "actions/checkout", Uses: "actions/checkout", Commit: "v4sha",
			Tree: tree(t, map[string]string{"action.yml": javascript})},
	})
	if err != nil {
		t.Fatalf("two commits of a JavaScript action refused the database: %v", err)
	}
	if len(skipped) != 2 {
		t.Errorf("skipped %+v, want both named", skipped)
	}
}

// The same callee reached twice at one commit is one staged file, not a collision.
func TestTheSameCommitReachedTwiceIsStagedOnce(t *testing.T) {
	comp := tree(t, map[string]string{"action.yml": composite})
	layout, _, err := Plan(ownedRoot(t), []Member{
		{Repository: "vendor/comp", Uses: "vendor/comp", Commit: "c1", Tree: comp},
		{Repository: "vendor/comp", Uses: "vendor/comp", Commit: "C1", Tree: comp},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(layout.Staged) != 1 {
		t.Errorf("staged %d copies of one commit", len(layout.Staged))
	}
}

// A finding reported in a staged file belongs to the repository that holds the file, at the path
// it has there. Anything else attaches to no node: a staged path matches nothing on record.
func TestAttributionUnstagesToTheRepositoryThatHoldsTheFile(t *testing.T) {
	root := Root{Repository: "Octo/App", Commit: "aaaa", Tree: tree(t, map[string]string{
		"action.yml": composite,
	})}
	layout, _, err := Plan(root, []Member{
		{Repository: "Vendor/Comp", Uses: "vendor/comp", Commit: "c1",
			Tree: tree(t, map[string]string{"action.yml": composite})},
		{Repository: "vendor/rw", Uses: "vendor/rw/.github/workflows/reuse.yml", Commit: "c3",
			Tree: tree(t, map[string]string{".github/workflows/reuse.yml": reusable})},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for path, want := range map[string]Attribution{
		".github/actions/external/vendor/comp/action.yml": {
			Repository: "Vendor/Comp", Path: "action.yml"},
		".github/workflows/external/vendor/rw/.github/workflows/reuse.yml": {
			Repository: "vendor/rw", Path: ".github/workflows/reuse.yml"},
		"action.yml":               {Repository: "Octo/App", Path: "action.yml"},
		".github/workflows/ci.yml": {Repository: "Octo/App", Path: ".github/workflows/ci.yml"},
		AnchorPath:                 {Excluded: true},
	} {
		got, err := layout.Attribute(path)
		if err != nil {
			t.Errorf("Attribute(%s): %v", path, err)
			continue
		}
		if got != want {
			t.Errorf("Attribute(%s) = %+v, want %+v", path, got, want)
		}
	}
	// Under the external prefixes and staged by nobody: refused rather than guessed.
	if _, err := layout.Attribute(".github/actions/external/vendor/other/action.yml"); err == nil {
		t.Error("a path nothing staged was attributed")
	}
}

// codeql/actions-all needs a Workflow to compute a repository root, and without one every uses:
// is unresolved. A repository that is itself an action has none, so the closure synthesizes one,
// and the synthesized file is never a finding location.
func TestATreeWithNoWorkflowIsAnchored(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"an action":          {"action.yml": composite},
		"a workflow no jobs": {".github/workflows/x.yml": "on: push\n"},
	} {
		root := Root{Repository: "octo/action", Commit: "aaaa", Tree: tree(t, files)}
		layout, _, err := Plan(root, nil)
		if err != nil {
			t.Fatalf("%s: Plan: %v", name, err)
		}
		if !layout.Anchored {
			t.Errorf("%s: not anchored, so nothing in its closure would resolve", name)
			continue
		}
		dst := filepath.Join(t.TempDir(), "src")
		if err := layout.Write(dst); err != nil {
			t.Fatalf("%s: Write: %v", name, err)
		}
		body, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(AnchorPath)))
		if err != nil {
			t.Fatalf("%s: the anchor was not written: %v", name, err)
		}
		if !strings.Contains(string(body), "jobs:") {
			t.Errorf("%s: the anchor declares no job, so it is not a Workflow:\n%s", name, body)
		}
		if got, _ := layout.Attribute(AnchorPath); !got.Excluded {
			t.Errorf("%s: a finding in the synthesized anchor would be ingested", name)
		}
	}

	layout, _, err := Plan(ownedRoot(t), nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if layout.Anchored {
		t.Error("a repository with a workflow was anchored anyway")
	}
}

// A repository that already holds files under the external prefixes would mix its own with the
// staged ones, and actions-all would treat its files as somebody else's callees.
func TestARootThatAlreadyHoldsAnExternalPrefixIsRefused(t *testing.T) {
	root := Root{Repository: "octo/app", Commit: "aaaa", Tree: tree(t, map[string]string{
		".github/workflows/ci.yml":                workflow,
		".github/actions/external/x/y/action.yml": composite,
	})}
	if _, _, err := Plan(root, nil); err == nil {
		t.Fatal("a root holding .github/actions/external/ was staged into")
	}
}

// The identity of a database that is not one repository is what went into it. Any staged commit
// moving has to move it, and the order members arrive in must not.
func TestTheDigestMovesWithEveryInputAndNothingElse(t *testing.T) {
	comp := tree(t, map[string]string{"action.yml": composite})
	rw := tree(t, map[string]string{".github/workflows/reuse.yml": reusable})
	members := func(compCommit string, reversed bool) []Member {
		ms := []Member{
			{Repository: "vendor/comp", Uses: "vendor/comp", Commit: compCommit, Tree: comp},
			{Repository: "vendor/rw", Uses: "vendor/rw/.github/workflows/reuse.yml", Commit: "c3", Tree: rw},
		}
		if reversed {
			ms[0], ms[1] = ms[1], ms[0]
		}
		return ms
	}
	digest := func(root Root, ms []Member) string {
		t.Helper()
		layout, _, err := Plan(root, ms)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		return layout.Digest()
	}
	root := ownedRoot(t)
	base := digest(root, members("c1", false))
	if again := digest(root, members("c1", true)); again != base {
		t.Error("the order members arrived in changed the digest")
	}
	if moved := digest(root, members("c2", false)); moved == base {
		t.Error("a staged commit moved and the digest did not")
	}
	other := root
	other.Commit = "bbbb"
	if moved := digest(other, members("c1", false)); moved == base {
		t.Error("the root commit moved and the digest did not")
	}
	if base == "" || len(base) != 64 {
		t.Errorf("digest %q is not a sha256", base)
	}
}

// Only the root's own files and the staged manifests go into the tree. A member's own workflows
// are its CI, not something any consumer runs, and staging its whole checkout would hand
// actions-all a second repository root.
func TestWriteCopiesTheRootAndOnlyTheStagedManifests(t *testing.T) {
	member := tree(t, map[string]string{
		"action.yml":               composite,
		".github/workflows/ci.yml": workflow,
		"other/action.yml":         composite,
	})
	layout, _, err := Plan(ownedRoot(t), []Member{
		{Repository: "vendor/comp", Uses: "vendor/comp", Commit: "c1", Tree: member},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "src")
	if err := layout.Write(dst); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var files []string
	if err := filepath.WalkDir(dst, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dst, path)
		files = append(files, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		".github/workflows/ci.yml":                        true,
		".github/actions/external/vendor/comp/action.yml": true,
	}
	if len(files) != len(want) {
		t.Errorf("wrote %v, want exactly %v", files, want)
	}
	for _, f := range files {
		if !want[f] {
			t.Errorf("wrote %s, which is neither the root's nor a staged manifest", f)
		}
	}
	body, err := os.ReadFile(filepath.Join(dst, ".github/actions/external/vendor/comp/action.yml"))
	if err != nil || string(body) != composite {
		t.Errorf("the staged manifest is not the member's bytes: %q, %v", body, err)
	}
}

// A manifest is read inside the member's own checkout. A link pointing out of it would put a file
// from this machine into a database as though a dependency had published it.
func TestAManifestThatLeavesItsCheckoutIsNotStaged(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.yml")
	if err := os.WriteFile(outside, []byte(composite), 0o600); err != nil {
		t.Fatal(err)
	}
	member := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(member, "action.yml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	layout, skipped, err := Plan(ownedRoot(t), []Member{
		{Repository: "vendor/comp", Uses: "vendor/comp", Commit: "c1", Tree: member},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(layout.Staged) != 0 || len(skipped) != 1 {
		t.Errorf("staged %+v, skipped %+v; a manifest outside its checkout was read", layout.Staged, skipped)
	}
}
