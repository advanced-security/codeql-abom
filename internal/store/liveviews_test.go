package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Reading graph tables directly bypasses tombstones; live views keep that rule in one place.
func TestReadsGoThroughTheLiveViews(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving repository root: %v", err)
	}

	// The writer inserts, the prune marks, the classification fills a column, and the schema
	// declares. Those are the only places that may name the physical tables, and each is named
	// rather than pattern-matched so adding one is a decision.
	//
	// edgeboundary.go is here because a view is not writable: filling `boundary` has to name
	// `abom_edge` itself. Its endpoint lookup names `abom_node` for the same statement's sake.
	allowed := map[string]bool{
		"internal/store/edgeboundary.go": true,
		"internal/store/graph.go":        true,
		"internal/store/prune.go":        true,
		"internal/store/schema.go":       true,
	}
	// `_live` after the table name is the view; anything else is a direct read.
	direct := regexp.MustCompile(`(?:FROM|JOIN)\s+(abom_node|abom_edge)\b(?:_live)?`)

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skip := skipNonSource(root, path, d); skip != nil {
			return skip
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if allowed[filepath.ToSlash(rel)] {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range direct.FindAllString(string(body), -1) {
			if strings.HasSuffix(m, "_live") {
				continue
			}
			t.Errorf("%s reads %q directly; use abom_node_live or abom_edge_live", rel, m)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
}

// Ownership reads must use the honored view so contradicted declarations stay gated.
func TestOwnershipReadsGoThroughTheHonoredView(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving repository root: %v", err)
	}

	// ownedscope.go declares the view, writes the table, and reads it undivided for the page that
	// has to show a contradicted declaration so the operator can correct it. schema.go declares.
	allowed := map[string]bool{
		"internal/store/ownedscope.go": true,
		"internal/store/schema.go":     true,
	}
	direct := regexp.MustCompile(`(?:FROM|JOIN)\s+(abom_owned_scope)\b(?:_honored)?`)

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skip := skipNonSource(root, path, d); skip != nil {
			return skip
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if allowed[filepath.ToSlash(rel)] {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range direct.FindAllString(string(body), -1) {
			if strings.HasSuffix(m, "_honored") {
				continue
			}
			t.Errorf("%s reads %q directly; use abom_owned_scope_honored", rel, m)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
}
