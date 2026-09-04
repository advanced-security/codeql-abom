package store

import "testing"

// The tree's central promise: every level is a grouping of the same reference set, so drilling
// from an account to a single `uses:` line never gains or loses a reference.
//
// Tested end to end rather than one level at a time, because the levels are computed by separate
// queries and could drift apart without either one looking wrong on its own.
func TestPinningTreeReconcilesFromOwnerToReference(t *testing.T) {
	s := subgraphFixture(t)

	for _, f := range []PinningFilter{
		{},
		{RefStyle: "tag"},
		{FlaggedOnly: true},
	} {
		owners, err := s.PinningLevel(PinningByOwner, "", f, 0)
		if err != nil {
			t.Fatalf("owner level: %v", err)
		}
		for _, owner := range owners {
			repos, err := s.PinningLevel(PinningByRepo, owner.ScopeKey, f, 0)
			if err != nil {
				t.Fatalf("repo level under %s: %v", owner.ScopeKey, err)
			}
			assertSum(t, f, owner, repos, "repositories under "+owner.ScopeKey)

			for _, repo := range repos {
				workflows, err := s.PinningLevel(PinningByWorkflow, repo.ScopeKey, f, 0)
				if err != nil {
					t.Fatalf("workflow level under %s: %v", repo.ScopeKey, err)
				}
				assertSum(t, f, repo, workflows, "workflows under "+repo.ScopeKey)

				for _, wf := range workflows {
					refs, err := s.PinningLevel(PinningByReference, wf.ScopeKey, f, 0)
					if err != nil {
						t.Fatalf("reference level under %s: %v", wf.ScopeKey, err)
					}
					assertSum(t, f, wf, refs, "references under "+wf.ScopeKey)
				}
			}
		}
	}
}

func assertSum(t *testing.T, f PinningFilter, parent PinningRow, children []PinningRow, what string) {
	t.Helper()
	if len(children) == 0 {
		t.Errorf("%s: parent holds %d references but has no children", what, parent.Pinnable)
		return
	}
	var pinnable, commit, floating, flagged int
	for _, c := range children {
		pinnable += c.Pinnable
		commit += c.Commit
		floating += c.Floating
		flagged += c.Flagged
	}
	if pinnable != parent.Pinnable {
		t.Errorf("%s (filter %+v): children hold %d references, parent claims %d",
			what, f, pinnable, parent.Pinnable)
	}
	if commit != parent.Commit || floating != parent.Floating {
		t.Errorf("%s: children split %d/%d, parent claims %d/%d",
			what, commit, floating, parent.Commit, parent.Floating)
	}
	if flagged != parent.Flagged {
		t.Errorf("%s: children carry %d flagged, parent claims %d", what, flagged, parent.Flagged)
	}
}

// A leaf is one target at one reference. Every use of it must be counted, or the tree would
// under-report exactly where a reader has drilled in far enough to act.
func TestPinningLeafCountsEveryUseOfTheSameReference(t *testing.T) {
	s := subgraphFixture(t)

	workflows, err := s.PinningLevel(PinningByWorkflow, "", PinningFilter{}, 0)
	if err != nil {
		t.Fatalf("workflow level: %v", err)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflows with pinnable references")
	}

	for _, wf := range workflows {
		refs, err := s.PinningLevel(PinningByReference, wf.ScopeKey, PinningFilter{}, 0)
		if err != nil {
			t.Fatalf("reference level: %v", err)
		}
		seen := map[string]bool{}
		for _, r := range refs {
			id := r.ScopeKey + "@" + r.Ref
			if seen[id] {
				t.Errorf("%s appears twice under %s; uses should be counted on one row",
					id, wf.ScopeKey)
			}
			seen[id] = true
			if r.HasChildren {
				t.Errorf("%s reports children; a reference is the leaf", id)
			}
			if r.Pinnable == 0 {
				t.Errorf("%s reports no uses", id)
			}
		}
	}
}

// Filtering narrows the reference set every level is computed from. Filtering the rows instead
// would leave a parent disagreeing with its own children the moment a filter was applied.
func TestPinningFilterNarrowsReferencesNotRows(t *testing.T) {
	s := subgraphFixture(t)

	all, err := s.PinningTotals(PinningFilter{})
	if err != nil {
		t.Fatalf("PinningTotals: %v", err)
	}
	commits, err := s.PinningTotals(PinningFilter{RefStyle: "commit"})
	if err != nil {
		t.Fatalf("PinningTotals(commit): %v", err)
	}
	tags, err := s.PinningTotals(PinningFilter{RefStyle: "tag"})
	if err != nil {
		t.Fatalf("PinningTotals(tag): %v", err)
	}

	if commits.Pinnable+tags.Pinnable != all.Pinnable {
		t.Errorf("commit %d + tag %d != all %d; the two styles must partition the set",
			commits.Pinnable, tags.Pinnable, all.Pinnable)
	}
	if commits.Floating != 0 {
		t.Errorf("filtering to commits left %d floating references", commits.Floating)
	}
	if tags.Commit != 0 {
		t.Errorf("filtering to tags left %d commit references", tags.Commit)
	}
}
