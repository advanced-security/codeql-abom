package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// systemClearedRoots are the shared temporary directories an operating system empties on its own
// schedule. The per-user directory behind TMPDIR is deliberately absent: a test's throwaway
// database belongs there, and warning about it would teach everyone to ignore the warning.
var systemClearedRoots = []string{"/tmp", "/private/tmp", "/var/tmp", "/private/var/tmp"}

// volatileDatabase reports why a database will not survive where it was put, or "" when it will.
//
// Nothing else notices this. The store opens the path it was given, creates what is missing and
// reports success, so a crawl written to a cleared directory comes back later as an empty corpus
// with no error, no event and nothing in the log to explain it.
func volatileDatabase(path string) string {
	if store.IsPostgresTarget(path) {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	for _, root := range systemClearedRoots {
		if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return fmt.Sprintf(
				"%s is under %s, which the system deletes on its own schedule; "+
					"put the database somewhere durable or it will empty itself with no error",
				abs, root)
		}
	}
	return ""
}
