package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/submit"
)

// unobservedClosure is octo/app pinning vendor/comp@v1, which was analyzed at two commits before
// sightings kept what each commit declared, so what v1's commit declares is not known.
func unobservedClosure(t *testing.T) *store.Store {
	t.Helper()
	s := newTestStore(t)
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	comp, err := s.InsertUpstreamRepository("vendor/comp")
	if err != nil {
		t.Fatal(err)
	}
	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: app},
		{Key: "app-step", Kind: "step", RepositoryID: app, ContainerKey: "octo/app/.github/workflows/ci.yml"},
		{Key: "vendor/comp", Kind: "action", Owner: "vendor", Repo: "comp", IsExternal: true},
		{Key: "comp-step", Kind: "step", RepositoryID: comp, ContainerKey: "vendor/comp"},
		{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.AddEdge(store.Edge{Key: "octo/app:.github/workflows/ci.yml:10:9", SrcKey: "app-step",
		DstKey: "vendor/comp", Kind: store.EdgeKindStepUsesAction, RefRaw: "vendor/comp@v1", RefValue: "v1",
		RepositoryID: app, FilePath: ".github/workflows/ci.yml", StartLine: 10, StartColumn: 9}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, commit := range []string{"c1", "c2"} {
		run, err := s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
			QueryID: "abom/edges", Repository: "vendor/comp", DBCommit: commit})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.AddEdge(store.Edge{Key: "vendor/comp:action.yml:9:9", SrcKey: "comp-step",
			DstKey: "actions/cache", Kind: store.EdgeKindCompositeStepUsesAction,
			RefRaw: "actions/cache@v4", RefValue: "v4", RepositoryID: comp, QueryRunID: run,
			FilePath: "action.yml", StartLine: 9, StartColumn: 9}); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`
			INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
			                           resolved_sha, analyzed_ref)
			VALUES ('vendor/comp', ?, 'vendor', 'comp', 1, 'done', '2026-09-27T00:00:00Z', ?, ?)`,
			strings.Replace(commit, "c", "v", 1), commit, strings.Replace(commit, "c", "v", 1)); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// A snapshot replaces what GitHub found, so one built while a commit it reads is not known could
// delete dependencies the repository has. The preview says so before anybody presses submit, and
// the submission is refused before a single request leaves, and recorded: the ledger is the only
// record of what was and was not written.
func TestASubmissionIsRefusedBeforeAnyRequestWhileItsClosureIsNotKnown(t *testing.T) {
	s := unobservedClosure(t)
	var requests atomic.Int32
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"result":"SUCCESS","message":"accepted"}`)
	}))
	t.Cleanup(github.Close)
	client := submit.NewClient("token", []string{"octo/app"})
	client.BaseURL, client.HTTP = github.URL, github.Client()
	wiring := snapshotWiring{s: s, client: client,
		headSHA: func(context.Context, string) (string, error) { return "head", nil }}

	preview, err := wiring.preview("octo/app")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"vendor/comp@c1"}; !slices.Equal(preview.Unobserved, want) {
		t.Errorf("the preview names %q as not known, want %q", preview.Unobserved, want)
	}

	_, err = wiring.submit("octo/app")
	var refused *store.UnobservedClosureError
	if !errors.As(err, &refused) {
		t.Fatalf("submitting answered %v, want the refusal naming what is not known", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d requests reached GitHub for a refused submission", n)
	}
	recorded, err := s.Submissions()
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0].Result != "REFUSED" {
		t.Errorf("the ledger holds %+v, want the one refused attempt", recorded)
	}
}
