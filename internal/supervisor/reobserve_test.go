package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// refusingFetcher fails every checkout, so a claimed dependency shows up as one attempt spent.
type refusingFetcher struct{}

func (refusingFetcher) Fetch(context.Context, string, string, string, string) (expand.Tree, error) {
	return expand.Tree{}, errors.New("offline")
}

// analyzedBeforeContent is vendor/comp as a crawl left it before sightings kept content: an edges
// run at each commit, none marked, and a finished queue row whose reference resolved to each.
func analyzedBeforeContent(t *testing.T, sup *Supervisor, s *store.Store, commits ...string) {
	t.Helper()
	repo, err := s.InsertUpstreamRepository("vendor/comp")
	if err != nil {
		t.Fatal(err)
	}
	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "vendor/comp", Kind: "composite_action", Owner: "vendor", Repo: "comp",
			RepositoryID: repo, Path: "action.yml"},
		{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for i, commit := range commits {
		run, err := s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
			QueryID: "abom/edges", Repository: "vendor/comp", DBCommit: commit})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.AddEdge(store.Edge{Key: "vendor/comp:action.yml:9:9", SrcKey: "vendor/comp",
			DstKey: "actions/cache", Kind: store.EdgeKindCompositeStepUsesAction,
			RefRaw: "actions/cache@v4", RefValue: "v4", RepositoryID: repo, QueryRunID: run,
			FilePath: "action.yml", StartLine: 9, StartColumn: 9}); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		ref := fmt.Sprintf("v%d", i+1)
		key := frontier.Key{NodeKey: "vendor/comp", Ref: ref}
		if _, err := sup.Queue.Enqueue([]frontier.Item{{NodeKey: key.NodeKey, Ref: ref,
			Owner: "vendor", Repo: "comp", Depth: 1}}); err != nil {
			t.Fatal(err)
		}
		if err := sup.Queue.Complete(key, frontier.Resolution{Ref: ref, SHA: commit}); err != nil {
			t.Fatal(err)
		}
	}
}

func attemptsByRef(t *testing.T, s *store.Store) map[string]string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT ref, status, attempts FROM abom_frontier
		WHERE node_key = 'vendor/comp'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var ref, status string
		var attempts int
		if err := rows.Scan(&ref, &status, &attempts); err != nil {
			t.Fatal(err)
		}
		out[ref] = fmt.Sprintf("%s/%d", status, attempts)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Nothing re-observes a commit analyzed before sightings kept content unless the crawl meets it
// again, and a server start must not put them all back: that was 2,456 rows, a re-crawl. A running
// cycle puts back one claim's worth before it claims, so it analyzes them itself, and the window
// stays occupied by work that failed rather than letting the backlog drain into retrying.
func TestACycleReobservesCommitsWhoseDeclarationsAreNotKnownAClaimAtATime(t *testing.T) {
	sup, s := newSupervisor(t)
	analyzedBeforeContent(t, sup, s, "c1", "c2", "c3")
	sup.Options.Limit = 2
	sup.Expander.Fetcher = refusingFetcher{}
	var log bytes.Buffer
	sup.Log = &log

	sup.cycle(context.Background())

	got := attemptsByRef(t, s)
	if got["v1"] != "pending/1" || got["v2"] != "pending/1" || got["v3"] != "done/0" {
		t.Errorf("after one cycle the rows are %v, want v1 and v2 claimed and tried, v3 left done", got)
	}
	if !strings.Contains(log.String(), "put back 2") || !strings.Contains(log.String(), "3 commits") {
		t.Errorf("the cycle did not say what it put back and what remains:\n%s", log.String())
	}

	sup.cycle(context.Background())

	if got := attemptsByRef(t, s); got["v3"] != "done/0" {
		t.Errorf("with the window held by two failed attempts the rows are %v, want v3 left done", got)
	}
}

// A commit only rows outside the owned closure stand at is not the crawl's to observe: putting
// one back queues work that is never claimed, and it would hold the window shut for good.
func TestACycleReobservesOnlyInsideTheOwnedClosure(t *testing.T) {
	sup, s := newSupervisor(t)
	analyzedBeforeContent(t, sup, s, "c1", "c2")
	sup.Options.Limit = 2
	sup.Expander.Fetcher = refusingFetcher{}
	sup.Queue.Closure = func() ([]string, error) { return []string{"octo/mine"}, nil }

	sup.cycle(context.Background())

	if got := attemptsByRef(t, s); got["v1"] != "done/0" || got["v2"] != "done/0" {
		t.Errorf("rows are %v, want both left done outside the closure", got)
	}
}
