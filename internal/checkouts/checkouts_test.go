package checkouts

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// workDir builds a scratch directory holding the named entries as real directories.
func workDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(dir, n, "src"), 0o755); err != nil {
			t.Fatalf("creating %s: %v", n, err)
		}
	}
	return dir
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}

// A checkout is a working copy of a repository the corpus no longer reaches. It costs disk and
// nothing reads it, and the graph rows for it are already gone.
func TestACheckoutOfARepositoryOutsideTheClosureIsFound(t *testing.T) {
	dir := workDir(t, "octo_app", "stranger_thing")

	got, err := Stale(dir, []string{"stranger/thing"})
	if err != nil {
		t.Fatalf("Stale: %v", err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "stranger_thing" {
		t.Errorf("Stale = %v, want the one checkout outside the closure", got)
	}
}

// Every reference of a repository is a separate tree, so removing the repository removes all of
// them rather than whichever one happened to be named.
func TestEveryReferenceOfTheSameRepositoryGoesTogether(t *testing.T) {
	dir := workDir(t, "stranger_thing", "stranger_thing@v4", "stranger_thing@refs-heads-main")

	got, err := Stale(dir, []string{"stranger/thing"})
	if err != nil {
		t.Fatalf("Stale: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("Stale found %d of the repository's 3 trees: %v", len(got), got)
	}
}

// The directory name joins owner and repository with an underscore, and a repository name may
// itself contain one. Matching on a prefix alone would take `stranger/thing-extra`'s tree for
// `stranger/thing`'s and delete a working copy the closure still reaches.
func TestARepositoryWhoseNameExtendsAnotherIsNotTakenForIt(t *testing.T) {
	dir := workDir(t, "stranger_thing_extra", "stranger_thingy")

	got, err := Stale(dir, []string{"stranger/thing"})
	if err != nil {
		t.Fatalf("Stale: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Stale = %v, want nothing: neither directory is that repository's", got)
	}
}

// ABOM only ever creates directories here. Anything else was put there by somebody else, and a
// cleanup that removes what it did not create is a cleanup nobody can leave running.
func TestWhatABOMDidNotCreateIsLeftAlone(t *testing.T) {
	dir := workDir(t)
	if err := os.WriteFile(filepath.Join(dir, "stranger_thing"), []byte("not ours"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "stranger_thing@v4")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := Stale(dir, []string{"stranger/thing"})
	if err != nil {
		t.Fatalf("Stale: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Stale = %v, want nothing: a file and a symlink are not checkouts", got)
	}
}

// Removal reports what it did, and leaves everything it was not asked about.
func TestRemovingTakesOnlyWhatItWasGiven(t *testing.T) {
	dir := workDir(t, "octo_app", "stranger_thing", "stranger_thing@v4")

	stale, err := Stale(dir, []string{"stranger/thing"})
	if err != nil {
		t.Fatalf("Stale: %v", err)
	}
	removed, err := Remove(dir, stale)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed %d, want the repository's 2 trees", removed)
	}
	if left := names(t, dir); !slices.Equal(left, []string{"octo_app"}) {
		t.Errorf("left %v, want only the checkout still inside the closure", left)
	}
}

// The paths come from a caller, and a caller is where a mistake gets in. Nothing outside the work
// directory is removed however it is named, because the one thing this must never do is delete
// somebody's source tree.
func TestNothingOutsideTheWorkDirectoryIsRemoved(t *testing.T) {
	dir := workDir(t, "octo_app")
	outside := t.TempDir()
	victim := filepath.Join(outside, "precious")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatalf("creating the victim: %v", err)
	}

	removed, err := Remove(dir, []string{victim, filepath.Join(dir, "..", "escaped")})
	if err == nil {
		t.Error("removing a path outside the work directory was not refused")
	}
	if removed != 0 {
		t.Errorf("removed %d outside the work directory", removed)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("the directory outside the work directory is gone: %v", err)
	}
}

// Nothing declared reaches nothing, and removing every checkout on the strength of an empty list
// is the most destructive thing this could do. The graph prune refuses the same way.
func TestAnEmptyListRemovesNothing(t *testing.T) {
	dir := workDir(t, "octo_app", "stranger_thing")

	got, err := Stale(dir, nil)
	if err != nil {
		t.Fatalf("Stale: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Stale = %v, want nothing when no repository was named", got)
	}
}

// A work directory that was never created is not an error: it is a server that has not fetched
// anything yet, which is the state every install starts in.
func TestAWorkDirectoryThatDoesNotExistYetIsNotAFault(t *testing.T) {
	got, err := Stale(filepath.Join(t.TempDir(), "never-made"), []string{"stranger/thing"})
	if err != nil {
		t.Errorf("Stale: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Stale = %v, want nothing", got)
	}
}
