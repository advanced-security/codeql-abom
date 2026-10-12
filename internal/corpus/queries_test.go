package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/store"
)

var corpusFixtures = filepath.Join("..", "..", "testdata", "corpus", "fixtures")

func readRecording(t *testing.T) recording {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(filepath.Dir(corpusFixtures), recordingFile))
	if err != nil {
		t.Fatalf("the query recording: %v", err)
	}
	var rec recording
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// builtWithQueries is the corpus with its query pack laid out, as a server given it would read it.
func builtWithQueries(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	path, queries := filepath.Join(dir, "abom.db"), filepath.Join(dir, "ql")
	if err := build(path, corpusFixtures, queries, nil); err != nil {
		t.Fatalf("build: %v", err)
	}
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, queries
}

// stepsOf is what the catalog would show for each query, under the CodeQL the recording names.
func stepsOf(t *testing.T, s *store.Store, root string) map[string]map[string]authoring.Step {
	t.Helper()
	queries, err := authoring.Workspace(root)
	if err != nil {
		t.Fatal(err)
	}
	printed, err := s.NewestASTs()
	if err != nil {
		t.Fatal(err)
	}
	cli := readRecording(t).CLIVersion
	out := map[string]map[string]authoring.Step{}
	for _, q := range queries {
		tests, err := s.QueryTestsOf(q.ID)
		if err != nil {
			t.Fatal(err)
		}
		steps, err := authoring.Progress(root, q, authoring.Evidence{CLI: cli, Tests: tests, Printed: printed[q.ID]})
		if err != nil {
			t.Fatal(err)
		}
		out[q.ID] = map[string]authoring.Step{}
		for _, step := range steps {
			out[q.ID][step.Step] = step
		}
	}
	return out
}

// The browser suite measures the marks, and a mark it never draws in some state is a state whose
// width and color nothing checks.
func TestTheCorpusQueryPackLeavesAMarkInEveryState(t *testing.T) {
	s, root := builtWithQueries(t)
	got := stepsOf(t, s, root)

	seen := map[authoring.State]bool{}
	for _, steps := range got {
		for _, step := range steps {
			seen[step.State] = true
		}
	}
	for _, state := range []authoring.State{authoring.Pending, authoring.Passing, authoring.Warning, authoring.Failed} {
		if !seen[state] {
			t.Errorf("no mark in the corpus query pack is %s", state)
		}
	}
	for _, c := range []struct {
		query, step string
		want        authoring.State
		says        string
	}{
		{"acme/checkout-tag-pin", "tests", authoring.Passing, "failed first"},
		{"acme/checkout-tag-pin", "ast", authoring.Passing, "printed at"},
		{"acme/write-all-permissions", "expected", authoring.Failed, "failed expectation"},
		{"acme/unpinned-setup-action", "expected", authoring.Warning, "empty"},
	} {
		if step := got[c.query][c.step]; step.State != c.want || !strings.Contains(step.Detail, c.says) {
			t.Errorf("%s %s reads %+v, want %s saying %q", c.query, c.step, step, c.want, c.says)
		}
	}
	for name, step := range got["acme/self-hosted-runner"] {
		if step.State != authoring.Pending {
			t.Errorf("acme/self-hosted-runner, which nobody has worked on, has %s %s", name, step.State)
		}
	}
}

// A session the suite can draw needs no agent, and every decision on its timeline is the gate's.
func TestTheSeededSessionFinishedWithWhatItWasRefusedAndTheTreeItPrinted(t *testing.T) {
	s, root := builtWithQueries(t)

	sessions, err := s.AuthoringSessions(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != store.SessionFinished || sessions[0].Detail != finished {
		t.Fatalf("request 1 has sessions %+v, want one that finished with its agent's words", sessions)
	}
	events, err := s.AuthoringEvents(sessions[0].RowID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	refused := false
	for _, e := range events {
		if e.Decision == "refused" && e.Tool == "bash" && strings.Contains(e.Reason, "not part of the authoring loop") {
			refused = true
		}
	}
	if !refused || sessions[0].Refused != 1 {
		t.Errorf("the timeline %+v does not keep the refused shell call with the gate's reason", events)
	}

	trees, err := s.AuthoringASTs(1)
	if err != nil {
		t.Fatal(err)
	}
	q, _, err := authoring.Find(root, "acme/checkout-tag-pin")
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := authoring.DigestFixtures(root, q.Tests)
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 1 || trees[0].SessionID != sessions[0].RowID || trees[0].TestDigest != fixtures ||
		trees[0].CLIVersion != readRecording(t).CLIVersion || trees[0].Size == 0 {
		t.Errorf("request 1 kept trees %+v, want the one its session printed from these fixtures", trees)
	}

	reviews, err := s.AuthoringReviews(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 1 || reviews[0].Verdict != store.ReviewApproved || reviews[0].ReviewedBy != seededBy {
		t.Errorf("request 1 has reviews %+v, want the operator's acceptance", reviews)
	}

	requests, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]int{}
	for _, r := range requests {
		sources[r.TargetSource]++
	}
	if sources[store.TargetOperator] == 0 || sources[store.TargetAgent] == 0 || sources[""] == 0 {
		t.Errorf("requests are targeted %v, want some by the operator, one by the agent and one by nobody",
			sources)
	}
}

// The pack is replaced wholesale on a rebuild, so pointed at an operator's own it must refuse
// rather than delete their queries.
func TestARebuildRefusesADirectoryTheCorpusDidNotWrite(t *testing.T) {
	dir := t.TempDir()
	theirs := filepath.Join(dir, "ql")
	mine := filepath.Join(theirs, "src", "Mine.ql")
	if err := put(mine, "select 1\n"); err != nil {
		t.Fatal(err)
	}

	err := build(filepath.Join(dir, "abom.db"), corpusFixtures, theirs, nil)
	if err == nil || !strings.Contains(err.Error(), "did not write") {
		t.Fatalf("build over somebody's pack returned %v, want a refusal", err)
	}
	if _, err := os.Stat(mine); err != nil {
		t.Errorf("their query is gone: %v", err)
	}
}

// A recording of other files than the corpus lays out would seed marks the files do not earn.
func TestARecordingOfOtherFilesIsRefused(t *testing.T) {
	rec := readRecording(t)
	rec.Runs[0].Fixtures = "fixtures nobody laid out"
	body, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := put(filepath.Join(dir, recordingFile), string(body)); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "abom.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}

	err = seedQueries(s, filepath.Join(dir, "fixtures"), filepath.Join(dir, "ql"))
	if err == nil || !strings.Contains(err.Error(), "regenerate-corpus-fixtures") {
		t.Errorf("seeding from a stale recording returned %v, want it refused", err)
	}
}
