package store

import (
	"path/filepath"
	"testing"
)

// bomFixture is a corpus with one owned repository, a dependency reached only through another
// dependency, and a repository nobody owns reaches. Provenance is complete on one finding and
// absent on another, because a document that can only describe complete evidence is not a
// document about this corpus.
//
//	octo-org/app     ci.yml ------> actions/checkout@v4   direct, cross-owner, analyzed
//	                       \------> octo-org/shared@v1    direct, same-owner, analyzed
//	actions/checkout action.yml --> actions/setup-go@v5   INDIRECT, never analyzed
//	stranger/repo    ci.yml ------> stranger/thing        unreachable from the root
//
// `actions/checkout` is the intermediary rather than `octo-org/shared`, because everything under
// a declared account is itself a root and nothing reached through one would be indirect.
func bomFixture(t *testing.T) *Store {
	t.Helper()
	s, err := openTestStore(t, filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	repo := func(name string) int64 {
		t.Helper()
		id, err := s.UpsertRepository(name, "")
		if err != nil {
			t.Fatalf("UpsertRepository(%s): %v", name, err)
		}
		return id
	}
	app := repo("octo-org/app")
	checkout := repo("actions/checkout")
	stranger := repo("stranger/repo")

	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo-org", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	w := NewWriter(s, 0)
	add := adder(t, w)
	add(Node{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: app,
		Path: ".github/workflows/ci.yml"})
	add(Node{Key: "stranger/repo/.github/workflows/ci.yml", Kind: "workflow",
		RepositoryID: stranger, Path: ".github/workflows/ci.yml"})
	add(Node{Key: "actions/checkout", Kind: "composite_action", Owner: "actions", Repo: "checkout",
		RepositoryID: checkout, Path: "action.yml", IsExternal: true})
	add(Node{Key: "actions/checkout:step:1", Kind: "step",
		RepositoryID: checkout, Path: "action.yml", ContainerKey: "actions/checkout"})
	add(Node{Key: "octo-org/shared", Kind: "action", Owner: "octo-org", Repo: "shared",
		IsExternal: true})
	add(Node{Key: "actions/setup-go", Kind: "action", Owner: "actions", Repo: "setup-go",
		IsExternal: true})
	add(Node{Key: "stranger/thing", Kind: "action", Owner: "stranger", Repo: "thing",
		IsExternal: true})

	for _, e := range []Edge{
		{Key: "app-checkout", SrcKey: "octo-org/app/.github/workflows/ci.yml",
			DstKey: "actions/checkout", Kind: "step-uses-action", RepositoryID: app,
			RefRaw: "actions/checkout@v4", RefValue: "v4",
			FilePath: ".github/workflows/ci.yml", StartLine: 1},
		{Key: "app-shared", SrcKey: "octo-org/app/.github/workflows/ci.yml",
			DstKey: "octo-org/shared", Kind: "step-uses-action", RepositoryID: app,
			RefRaw: "octo-org/shared@v1", RefValue: "v1",
			FilePath: ".github/workflows/ci.yml", StartLine: 2},
		{Key: "checkout-setupgo", SrcKey: "actions/checkout:step:1",
			DstKey: "actions/setup-go", Kind: "composite-step-uses-action", RepositoryID: checkout,
			RefRaw: "actions/setup-go@v5", RefValue: "v5",
			FilePath: "action.yml", StartLine: 1},
		{Key: "stranger-thing", SrcKey: "stranger/repo/.github/workflows/ci.yml",
			DstKey: "stranger/thing", Kind: "step-uses-action", RepositoryID: stranger,
			RefRaw: "stranger/thing@v1", RefValue: "v1",
			FilePath: ".github/workflows/ci.yml", StartLine: 1},
	} {
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := s.ClassifyEdgeBoundaries(); err != nil {
		t.Fatalf("ClassifyEdgeBoundaries: %v", err)
	}

	// A run that recorded every provenance field.
	traced, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "abom/edges", QueryVersion: "1.0.0", Pack: "abom/actions-graph",
		PackVersion: "0.1.0", PackDigest: "sha256:pack", CLIVersion: "2.26.4",
		Repository: "octo-org/app", DBCommit: "cafe1234",
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if _, err := s.AddFindings([]Finding{
		{Provider: ProviderCodeQL, QueryRunID: traced, RuleID: "actions/unpinned-tag",
			RepositoryID: app,
			FilePath:     ".github/workflows/ci.yml", StartLine: 1, Severity: "warning",
			SecuritySeverity: score(5.5), Message: "unpinned", Fingerprint: "f1",
			ResultHash: "sha256:result"},

		// No run at all: analyzed but untraceable.
		{RuleID: "actions/unpinned-tag", RepositoryID: checkout,
			FilePath: "action.yml", StartLine: 1, Severity: "warning",
			Fingerprint: "f2"},
		// Somebody else's repository, so it must not appear in a document rooted at octo-org.
		{QueryRunID: traced, RuleID: "actions/unpinned-tag", RepositoryID: stranger,
			FilePath: ".github/workflows/ci.yml", StartLine: 1, Fingerprint: "f3"},
	}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}

	// Coverage: checkout@v4 and shared@v1 analyzed, setup-go@v5 queued and never done.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, resolved_sha,
		                           enqueued_at)
		VALUES ('actions/checkout', 'v4', 'actions', 'checkout', 1, 'done', 'cafe1234', ?),
		       ('octo-org/shared', 'v1', 'octo-org', 'shared', 1, 'done', 'beef5678', ?),
		       ('actions/setup-go', 'v5', 'actions', 'setup-go', 2, 'pending', NULL, ?)`,
		now(), now(), now()); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}
	return s
}
