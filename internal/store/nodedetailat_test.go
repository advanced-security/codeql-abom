package store

import (
	"slices"
	"testing"
)

// nodeAtFixture is a composite action analyzed at two commits. One line of it names actions/cache
// at both, by another reference at each; a second line exists only at the second commit; and its
// own code carries a different finding at each. v9 resolves to nothing.
func nodeAtFixture(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	repo, err := s.InsertUpstreamRepository("vendor/tool")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, 0)
	add := adder(t, w)
	add(Node{Key: "vendor/tool", Kind: "composite_action", Owner: "vendor", Repo: "tool", RepositoryID: repo})
	for _, a := range []string{"actions/cache", "actions/setup-node"} {
		add(Node{Key: a, Kind: "action", Owner: a[:7], Repo: a[8:], IsExternal: true})
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	add(Node{Key: "tool-step-1", Kind: "step", RepositoryID: repo, ContainerKey: "vendor/tool"})
	add(Node{Key: "tool-step-2", Kind: "step", RepositoryID: repo, ContainerKey: "vendor/tool"})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	const v1, v2 = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	first, second := recordRun(t, s, "vendor/tool", v1), recordRun(t, s, "vendor/tool", v2)
	uses := func(run int64, step, line, dst, ref string) {
		t.Helper()
		if err := w.AddEdge(Edge{Key: "vendor/tool:action.yml:" + line + ":9", QueryRunID: run,
			SrcKey: step, DstKey: dst, Kind: EdgeKindCompositeStepUsesAction, RefRaw: dst + "@" + ref,
			RefValue: ref, RepositoryID: repo, FilePath: "action.yml", StartLine: len(line)}); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	uses(first, "tool-step-1", "10", "actions/cache", "v3")
	uses(second, "tool-step-1", "10", "actions/cache", "v4")
	uses(second, "tool-step-2", "20", "actions/setup-node", "v4")

	for run, rule := range map[int64]string{first: "acme/at-v1", second: "acme/at-v2"} {
		if _, err := s.AddFindings([]Finding{{QueryRunID: run, RuleID: rule, RepositoryID: repo,
			FilePath: "action.yml", StartLine: 3, Message: rule, Fingerprint: rule}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`INSERT INTO abom_finding_node (finding_row_id, node_row_id)
			SELECT f.row_id, n.row_id FROM abom_finding f, abom_node n
			 WHERE f.rule_id = ? AND n.node_key = 'vendor/tool'`, rule); err != nil {
			t.Fatal(err)
		}
	}
	for ref, sha := range map[string]string{"v1": v1, "v2": v2, "v9": ""} {
		if _, err := s.DB().Exec(`INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status,
			enqueued_at, resolved_sha) VALUES ('vendor/tool', ?, 'vendor', 'tool', 1, 'done', '', ?)`,
			ref, sha); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func ruleIDs(d NodeDetail) []string {
	out := []string{}
	for _, r := range d.Rules {
		out = append(out, r.RuleID)
	}
	slices.Sort(out)
	return out
}

func usedAt(d NodeDetail) map[string][]string {
	out := map[string][]string{}
	for _, n := range d.Uses {
		out[n.Key] = n.Refs
	}
	return out
}

// What an action holds and what it uses are read at the version a reader narrowed to: under v1
// nothing borrowed from v2's commit, and a reference nothing resolved is not analyzed rather than
// clean. An action's uses: lines are its steps', so they are read through what it contains.
func TestADependencysDetailsAreReadAtTheVersionAskedFor(t *testing.T) {
	s := nodeAtFixture(t)

	every, err := s.NodeDetailOf("vendor/tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := ruleIDs(every); !slices.Equal(got, []string{"acme/at-v1", "acme/at-v2"}) {
		t.Errorf("every version: rules %v, want both commits'", got)
	}
	if got := usedAt(every); len(got) != 2 {
		t.Errorf("every version: uses %v, want what both of its lines name", got)
	}

	v1, err := s.NodeDetailOf("vendor/tool", []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ruleIDs(v1); !slices.Equal(got, []string{"acme/at-v1"}) {
		t.Errorf("at v1: rules %v, want only v1's commit's", got)
	}
	if got := usedAt(v1); len(got) != 1 || !slices.Equal(got["actions/cache"], []string{"v3"}) {
		t.Errorf("at v1: uses %v, want actions/cache at v3 and nothing only v2 declares", got)
	}

	v2, err := s.NodeDetailOf("vendor/tool", []string{"v2"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ruleIDs(v2); !slices.Equal(got, []string{"acme/at-v2"}) {
		t.Errorf("at v2: rules %v, want only v2's commit's", got)
	}
	if got := usedAt(v2); !slices.Equal(got["actions/cache"], []string{"v4"}) || got["actions/setup-node"] == nil {
		t.Errorf("at v2: uses %v, want actions/cache at v4 and actions/setup-node", got)
	}

	v9, err := s.NodeDetailOf("vendor/tool", []string{"v9"})
	if err != nil {
		t.Fatal(err)
	}
	if len(v9.Rules) != 0 || len(v9.Uses) != 0 ||
		!slices.Equal(v9.RulesUnread, []string{"v9"}) || !slices.Equal(v9.UsesUnread, []string{"v9"}) {
		t.Errorf("at v9: rules %v, uses %v, unread %v / %v; want nothing, and v9 named as not read",
			v9.Rules, v9.Uses, v9.RulesUnread, v9.UsesUnread)
	}
}

// Read at a version, `needs:` is still ordering and not use: the node never lists itself.
func TestNeedsIsNotUseAtAVersionEither(t *testing.T) {
	s := nodeAtFixture(t)
	var second int64
	if err := s.DB().QueryRow(`SELECT row_id FROM abom_query_run
		WHERE db_commit = '2222222222222222222222222222222222222222'`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	var repo int64
	if err := s.DB().QueryRow(`SELECT row_id FROM repository WHERE repository_full_name = 'vendor/tool'`).
		Scan(&repo); err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, 0)
	add := adder(t, w)
	add(Node{Key: "vendor/tool", Kind: "composite_action", Owner: "vendor", Repo: "tool", RepositoryID: repo})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	add(Node{Key: "tool-step-1", Kind: "step", RepositoryID: repo, ContainerKey: "vendor/tool"})
	add(Node{Key: "tool-step-2", Kind: "step", RepositoryID: repo, ContainerKey: "vendor/tool"})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := w.AddEdge(Edge{Key: "vendor/tool:action.yml:30:9", QueryRunID: second, SrcKey: "tool-step-2",
		DstKey: "tool-step-1", Kind: EdgeKindJobNeedsJob, RepositoryID: repo, FilePath: "action.yml",
		StartLine: 30}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	v2, err := s.NodeDetailOf("vendor/tool", []string{"v2"})
	if err != nil {
		t.Fatal(err)
	}
	if got := usedAt(v2); got["vendor/tool"] != nil {
		t.Errorf("at v2: uses %v, which counts needs: as a use of itself", got)
	}
}
