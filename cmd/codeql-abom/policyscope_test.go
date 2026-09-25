package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A policy saved before its scope moved into its predicate is migrated when the server starts,
// with the grouping key the server was started with, and one it cannot migrate is named.
func TestServingWritesARetiredPolicyScopeIntoItsPredicate(t *testing.T) {
	db := filepath.Join(t.TempDir(), "abom.db")
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][3]string{
		{"unit-three", "unit", "3"},
		{"just-tool", "repository", "octo-org/tool"},
	} {
		if _, err := s.DB().Exec(`
INSERT INTO abom_policy (name, predicate, scope_kind, scope_value, state, created_at)
VALUES (?, 'rules=actions%2Funpinned-tag', ?, ?, 'draft', '2026-09-01T00:00:00Z')`,
			row[0], row[1], row[2]); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := &lockedWriter{}
	if err := runServe(ctx, []string{"--db", db, "--addr", "127.0.0.1:0",
		"--group-property", "business_id"}, out, out); err != nil {
		t.Fatalf("serve: %v (%s)", err, out.String())
	}

	reopened, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for name, want := range map[string]string{
		"unit-three": "properties=business_id%3A3",
		"just-tool":  "repositories=octo-org%2Ftool",
	} {
		p, ok, err := reopened.PolicyNamed(name)
		if err != nil || !ok {
			t.Fatalf("PolicyNamed(%s): %v", name, err)
		}
		if !strings.Contains(p.Predicate, want) {
			t.Errorf("%s predicate = %q, want %s written into it", name, p.Predicate, want)
		}
	}
	if !strings.Contains(out.String(), "2 policies") {
		t.Errorf("the migration was not reported: %s", out.String())
	}
}
