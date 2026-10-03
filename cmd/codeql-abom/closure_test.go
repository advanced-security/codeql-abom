package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/closure"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func TestTheClosureCommandIsDispatched(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"closure"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--db is required") {
		t.Errorf("err = %v, want the closure command asking for a database", err)
	}
}

// A closure is rooted at a repository somebody can act on. One outside the declared scope is
// refused by name before anything is fetched or built, and the refusal is not a clean exit.
func TestTheClosureCommandRefusesARepositoryNobodyOwns(t *testing.T) {
	db := filepath.Join(t.TempDir(), "abom.db")
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnedScope([]store.DeclaredAccount{{
		Owner: "octo", Kind: store.AccountOrganization, Enabled: true,
	}}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertUpstreamRepository("vendor/comp"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	var stdout, stderr bytes.Buffer
	err = runClosure(context.Background(), []string{
		"--db", db, "--repo", "vendor/comp", "--work-dir", t.TempDir(), "--codeql", "/nonexistent/codeql",
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("a closure rooted outside the declared scope exited cleanly")
	}
	if !strings.Contains(stdout.String(), "vendor/comp refused") ||
		!strings.Contains(stdout.String(), "declared scope") {
		t.Errorf("the refusal does not say why:\n%s", stdout.String())
	}
}

// What a closure left out is part of the answer: a member nobody could stage is a coverage gap,
// and a report that only listed what was staged would read as complete.
func TestAClosureReportNamesWhatWasLeftOut(t *testing.T) {
	var out bytes.Buffer
	printClosureReport(&out, expand.ClosureReport{
		Root: "octo/app", Commit: strings.Repeat("a", 40), Digest: strings.Repeat("d", 64),
		Staged: []closure.Staged{{Uses: "vendor/comp", Commit: strings.Repeat("c", 40),
			Kind: closure.Composite, StagedPath: ".github/actions/external/vendor/comp/action.yml"}},
		Skipped: []closure.Skipped{
			{Reason: "a node action, which is never a callable"},
			{Reason: "a node action, which is never a callable"},
		},
		Unstaged: []expand.ClosureGap{{Uses: "vendor/gone", Reason: "not a repository the corpus holds"}},
		Extract:  1500 * time.Millisecond, Analyze: 3 * time.Second,
		Tally: store.ClosureTally{Findings: 9, Shared: 6, Unattached: 3},
	})
	for _, want := range []string{
		"closure: octo/app@aaaaaaaaaaaa dddddddddddd",
		"staged 1 (1 composite, 0 reusable workflow), skipped 2, unstaged 1",
		"staged vendor/comp@cccccccccccc as .github/actions/external/vendor/comp/action.yml",
		"2 skipped: a node action, which is never a callable",
		"1 unstaged: not a repository the corpus holds",
		"built in 1.5s, analyzed in 3.0s",
		"findings: 9 (6 already reported regularly, 3 only through the closure), 3 attached to no node",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the report does not say %q:\n%s", want, out.String())
		}
	}
}
