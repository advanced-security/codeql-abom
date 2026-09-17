package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The command exists so a document can be produced without a browser, and so CI can diff one.
func TestTheCommandWritesANativeDocumentToStandardOutput(t *testing.T) {
	db := bomCommandDB(t)
	var stdout, stderr bytes.Buffer

	if err := runBOM(context.Background(), []string{"--db", db}, &stdout, &stderr); err != nil {
		t.Fatalf("runBOM: %v (%s)", err, stderr.String())
	}
	var doc struct {
		Roots      []string `json:"roots"`
		Components []any    `json:"components"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("the command wrote something that is not JSON: %v", err)
	}
	if len(doc.Components) == 0 {
		t.Error("the document has no components")
	}
}

func TestTheCommandWritesTheOtherFormats(t *testing.T) {
	db := bomCommandDB(t)
	for _, c := range []struct{ format, marker string }{
		{"cyclonedx", `"bomFormat"`},
		{"spdx", `"spdxVersion"`},
	} {
		var stdout, stderr bytes.Buffer
		err := runBOM(context.Background(), []string{"--db", db, "--format", c.format},
			&stdout, &stderr)
		if err != nil {
			t.Fatalf("%s: %v (%s)", c.format, err, stderr.String())
		}
		if !strings.Contains(stdout.String(), c.marker) {
			t.Errorf("%s: output does not look like %s", c.format, c.format)
		}
	}
}

// The note about what was written goes to stderr, or `--out` plus a redirect mixes it into the
// document.
func TestTheCommandKeepsItsNoteOutOfTheDocument(t *testing.T) {
	db := bomCommandDB(t)
	out := filepath.Join(t.TempDir(), "bom.json")
	var stdout, stderr bytes.Buffer

	if err := runBOM(context.Background(), []string{"--db", db, "--out", out},
		&stdout, &stderr); err != nil {
		t.Fatalf("runBOM: %v (%s)", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("standard output = %q, want nothing when writing to a file", stdout.String())
	}
	if !strings.Contains(stderr.String(), "analyzed") {
		t.Error("the command did not say what coverage the document has")
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the written document: %v", err)
	}
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Errorf("the written file is not valid JSON: %v", err)
	}
}

func TestTheCommandRefusesAnUnknownFormat(t *testing.T) {
	db := bomCommandDB(t)
	var stdout, stderr bytes.Buffer

	if err := runBOM(context.Background(), []string{"--db", db, "--format", "spdx3"},
		&stdout, &stderr); err == nil {
		t.Error("an unknown format was accepted")
	}
}

// Without a database there is nothing to describe, and guessing a path would be worse than
// refusing.
func TestTheCommandRequiresADatabase(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runBOM(context.Background(), nil, &stdout, &stderr); err == nil {
		t.Error("the command ran without --db")
	}
}

func bomCommandDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := store.Open(path, store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "octo-org", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: repoID,
			Path: ".github/workflows/ci.yml"},
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
			IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(store.Edge{
		Key: "e1", SrcKey: "octo-org/app/.github/workflows/ci.yml", DstKey: "actions/checkout",
		Kind: "step-uses-action", RefRaw: "actions/checkout@v4", RefValue: "v4",
		RepositoryID: repoID, FilePath: ".github/workflows/ci.yml", StartLine: 1,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := s.ClassifyEdgeBoundaries(); err != nil {
		t.Fatalf("ClassifyEdgeBoundaries: %v", err)
	}
	return path
}
