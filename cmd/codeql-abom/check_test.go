package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// A summary that omitted a check that is not running, or why one skipped, would read as health.
func TestCheckReportsEachBuiltCheckAndNamesTheOnesNotRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	s.Close()

	var out bytes.Buffer
	if err := run(context.Background(), []string{"check", "--db", path}, &out, io.Discard); err != nil {
		t.Fatalf("check: %v\n%s", err, out.String())
	}
	for _, want := range []string{"check 7", "check 8", "check 9", "disagreed unread",
		"the incremental path refused", "not built: check 1 ", "not built: check 10 "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the summary does not say %q:\n%s", want, out.String())
		}
	}

	s, err = store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	latest, err := s.LatestCheckRuns()
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []int{7, 8, 9} {
		if run, ok := latest[check]; !ok || !strings.HasSuffix(run.Build, graph.ReachMarkVersion) {
			t.Errorf("check %d's run is %+v, want one filed under a build naming %s", check, run,
				graph.ReachMarkVersion)
		}
	}
}

func TestCheckRequiresADatabase(t *testing.T) {
	if err := run(context.Background(), []string{"check"}, io.Discard, io.Discard); err == nil {
		t.Error("check without --db succeeded")
	}
}
