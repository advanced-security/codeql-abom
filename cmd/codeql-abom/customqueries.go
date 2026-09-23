package main

import (
	"context"
	"fmt"
	"io"

	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

// openCustomQueries makes the operator's own pack resolvable, creating it if it is not there.
//
// Named but absent is the ordinary first run. Not named at all is off: ABOM must not create a
// directory in somebody's working tree because a binary started. A failure is reported and not
// fatal, or a pack nobody has written a query into could take the crawl down.
func openCustomQueries(dir, codeqlPath string, stdout, stderr io.Writer) {
	if dir == "" {
		return
	}
	wrote, err := customql.Scaffold(dir, customPackVersion(codeqlPath))
	if err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return
	}
	runner.SetCustomPackRoot(dir)
	if wrote {
		fmt.Fprintf(stdout,
			"serve: created your query pack at %s; add a .ql under src/ and it will be cataloged\n",
			dir)
		return
	}
	fmt.Fprintf(stdout, "serve: your query pack at %s is on the search path\n", dir)
}

// customPackVersion pins the operator's pack to the CodeQL that will evaluate its queries.
//
// Best effort: a version that cannot be read leaves the manifest at its placeholder rather than
// failing the scaffold, because the pin is provenance and the pack is still usable without it.
func customPackVersion(codeqlPath string) string {
	r := &runner.Runner{CodeQLPath: codeqlPath}
	version, err := r.Version(context.Background())
	if err != nil {
		return ""
	}
	return version
}
