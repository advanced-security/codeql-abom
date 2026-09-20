package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// A database under a shared temporary directory is removed by the system on a schedule nobody
// using ABOM is watching, and it reads afterwards as the corpus having emptied itself. Eight of
// them were found in /tmp this way, the largest holding 56,830 nodes and 46,264 edges, while the
// canonical database sat empty.
func TestADatabaseTheSystemWillDeleteIsNamedAsOne(t *testing.T) {
	for _, path := range []string{
		"/tmp/abom.db",
		"/tmp/nested/abom.db",
		"/private/tmp/abom.db",
		"/var/tmp/abom.db",
	} {
		if volatileDatabase(path) == "" {
			t.Errorf("%s is on a filesystem the system clears, and nothing said so", path)
		}
	}
}

// Warning about a path that persists would teach whoever sees it to ignore the warning that
// matters. The per-user directory behind TMPDIR is where a test's throwaway database belongs.
func TestADurableDatabaseIsNotWarnedAbout(t *testing.T) {
	for _, path := range []string{
		".abom/db/abom.db",
		"/Users/someone/work/abom.db",
		"/var/folders/q3/T/abom.db",
		filepath.Join(t.TempDir(), "abom.db"),
	} {
		if reason := volatileDatabase(path); reason != "" {
			t.Errorf("%s persists, but was reported as volatile: %s", path, reason)
		}
	}
}

// The reason has to name the directory and the consequence, because "volatile" on its own leaves
// the reader to guess whether anything is actually at stake.
func TestTheReasonSaysWhereAndWhatHappens(t *testing.T) {
	reason := volatileDatabase("/tmp/abom.db")
	if !strings.Contains(reason, "/tmp") {
		t.Errorf("the reason does not name the directory: %q", reason)
	}
	if !strings.Contains(reason, "delete") && !strings.Contains(reason, "clear") {
		t.Errorf("the reason does not say what happens to it: %q", reason)
	}
}
