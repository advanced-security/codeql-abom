package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Reading the graph tables directly bypasses the tombstone, so a pruned node stays drawn — and
// nothing fails, which is the worst shape a defect can take.
//
// This exists because the alternative was 110 hand edits every time what "live" means changes.
// `abom_node_live` and `abom_edge_live` are views, so the definition lives in one place and a
// read cannot opt out of it by accident.
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
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return err
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
