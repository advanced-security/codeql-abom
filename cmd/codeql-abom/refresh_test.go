package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A release that adds a derived table leaves it empty on every existing corpus, and only a full
// crawl filled it before this existed.
func TestRefreshFillsTheAggregatesWithoutCrawling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
			IsExternal: true},
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: repoID,
			Path: ".github/workflows/ci.yml"},
		{Key: "step:app:0", Kind: "step", RepositoryID: repoID,
			Path: ".github/workflows/ci.yml", ContainerKey: "octo-org/app/.github/workflows/ci.yml"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	if err := w.AddEdge(store.Edge{Key: "e0", SrcKey: "step:app:0", DstKey: "actions/checkout",
		Kind: "step-uses-action", RefValue: "v4", RepositoryID: repoID,
		FilePath: ".github/workflows/ci.yml", StartLine: 1}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	s.Close()

	var out, errOut bytes.Buffer
	if err := run(context.Background(),
		[]string{"refresh", "--db", path}, &out, &errOut); err != nil {
		t.Fatalf("refresh: %v (%s)", err, errOut.String())
	}

	reopened, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	for _, table := range []string{"abom_blast_radius", "abom_node_reach", "abom_node_version_reach"} {
		var n int
		if err := reopened.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Errorf("%s is still empty after a refresh", table)
		}
		if !strings.Contains(out.String(), table) {
			t.Errorf("the report does not mention %s:\n%s", table, out.String())
		}
	}

	// The report names every table it touched, including one that did not move, so a reader can
	// tell "nothing to do" from "did not run".
	for _, table := range aggregateTables {
		if !strings.Contains(out.String(), table) {
			t.Errorf("the report does not mention %s:\n%s", table, out.String())
		}
	}
}

// A refresh without a database is a refusal, not a default path somebody discovers later.
func TestRefreshRequiresADatabase(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run(context.Background(), []string{"refresh"}, &out, &errOut); err == nil {
		t.Fatal("refresh ran without --db")
	}
}
