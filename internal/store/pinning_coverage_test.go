package store

import "testing"

func levelTotals(rows []PinningRow) (pinnable, flagged int) {
	for _, r := range rows {
		pinnable += r.Pinnable
		flagged += r.Flagged
	}
	return
}

// The identity that has to hold: every pinnable reference either names a commit or does not.
func TestPinningLevelCommitAndFloatingSumToPinnable(t *testing.T) {
	s := subgraphFixture(t)

	for _, scope := range []string{PinningByOwner, PinningByRepo, PinningByWorkflow} {
		rows, err := s.PinningLevel(scope, "", PinningFilter{}, 0)
		if err != nil {
			t.Fatalf("PinningLevel(%s): %v", scope, err)
		}
		if scope == PinningByOwner && len(rows) == 0 {
			t.Fatal("no owners; the fixture has external references")
		}
		for _, r := range rows {
			if r.Commit+r.Floating != r.Pinnable {
				t.Errorf("%s %s: commit %d + floating %d != pinnable %d",
					scope, r.ScopeKey, r.Commit, r.Floating, r.Pinnable)
			}
		}
	}
}

// A parent equal to the sum of its children is what makes the tree readable. It has to survive
// a filter, which is why every level groups the same filtered reference set.
func TestPinningChildrenReconcileToParentUnderAFilter(t *testing.T) {
	s := subgraphFixture(t)

	for _, f := range []PinningFilter{{}, {RefStyle: "tag"}, {FlaggedOnly: true}} {
		owners, err := s.PinningLevel(PinningByOwner, "", f, 0)
		if err != nil {
			t.Fatalf("PinningLevel(owner): %v", err)
		}
		for _, owner := range owners {
			repos, err := s.PinningLevel(PinningByRepo, owner.ScopeKey, f, 0)
			if err != nil {
				t.Fatalf("PinningLevel(repo): %v", err)
			}
			pinnable, flagged := levelTotals(repos)
			if pinnable != owner.Pinnable || flagged != owner.Flagged {
				t.Errorf("filter %+v: %s reports %d pinnable / %d flagged, its repositories "+
					"total %d / %d", f, owner.ScopeKey, owner.Pinnable, owner.Flagged,
					pinnable, flagged)
			}
		}
	}
}

// The leaf is the level a person can act on, so it has to account for its workflow exactly.
func TestPinningLeafReconcilesToItsWorkflow(t *testing.T) {
	s := subgraphFixture(t)

	workflows, err := s.PinningLevel(PinningByWorkflow, "", PinningFilter{}, 0)
	if err != nil {
		t.Fatalf("PinningLevel(workflow): %v", err)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflows with pinnable references")
	}
	for _, w := range workflows {
		refs, err := s.PinningLevel(PinningByReference, w.ScopeKey, PinningFilter{}, 0)
		if err != nil {
			t.Fatalf("PinningLevel(reference): %v", err)
		}
		pinnable, flagged := levelTotals(refs)
		if pinnable != w.Pinnable || flagged != w.Flagged {
			t.Errorf("%s reports %d pinnable / %d flagged, its references total %d / %d",
				w.ScopeKey, w.Pinnable, w.Flagged, pinnable, flagged)
		}
		for _, r := range refs {
			if r.HasChildren {
				t.Errorf("reference %s claims children", r.ScopeKey)
			}
			if r.Ref == "" {
				t.Errorf("reference %s carries no ref value", r.ScopeKey)
			}
		}
	}
}

// Upstream repositories were fetched only to resolve dependencies; their pinning is not the
// organization's to answer for, and including them would move every figure.
func TestPinningLevelExcludesUpstream(t *testing.T) {
	s := subgraphFixture(t)

	rows, err := s.PinningLevel(PinningByRepo, "", PinningFilter{}, 0)
	if err != nil {
		t.Fatalf("PinningLevel: %v", err)
	}
	for _, r := range rows {
		if r.ScopeKey == "octo-org/shared" {
			t.Errorf("upstream repository %s included in pinning coverage", r.ScopeKey)
		}
	}
}

// A reference the rule never objected to is not evidence of a pinned reference: the rule leaves
// GitHub-owned actions alone. The two measures stay separate, and the gap is reported.
func TestPinningKeepsRuleVerdictSeparateFromReferenceStyle(t *testing.T) {
	s := subgraphFixture(t)

	rows, err := s.PinningLevel(PinningByRepo, "", PinningFilter{}, 0)
	if err != nil {
		t.Fatalf("PinningLevel: %v", err)
	}
	for _, r := range rows {
		if r.Flagged > r.Floating {
			t.Errorf("%s: flagged %d exceeds floating %d, so the rule is being read as the "+
				"style measure", r.ScopeKey, r.Flagged, r.Floating)
		}
		if r.FloatingUnflagged > r.Floating {
			t.Errorf("%s: unflagged floating %d exceeds floating %d",
				r.ScopeKey, r.FloatingUnflagged, r.Floating)
		}
	}
}

// A filter that matches nothing must report nothing, not a hundred per cent of an empty set.
func TestPinningTotalsReportZeroWhenAFilterMatchesNoReference(t *testing.T) {
	s := subgraphFixture(t)

	total, err := s.PinningTotals(PinningFilter{Search: "no-such-action-anywhere"})
	if err != nil {
		t.Fatalf("PinningTotals: %v", err)
	}
	if total.Pinnable != 0 || total.Commit != 0 {
		t.Errorf("expected an empty measurement, got %+v", total)
	}
}

// Totals and the widest level are two paths to the same number; drift between them would show
// as a page whose headline disagrees with its own table.
func TestPinningTotalsMatchTheOwnerLevel(t *testing.T) {
	s := subgraphFixture(t)

	total, err := s.PinningTotals(PinningFilter{})
	if err != nil {
		t.Fatalf("PinningTotals: %v", err)
	}
	owners, err := s.PinningLevel(PinningByOwner, "", PinningFilter{}, 0)
	if err != nil {
		t.Fatalf("PinningLevel: %v", err)
	}
	pinnable, flagged := levelTotals(owners)
	if pinnable != total.Pinnable || flagged != total.Flagged {
		t.Errorf("totals report %d pinnable / %d flagged, owners total %d / %d",
			total.Pinnable, total.Flagged, pinnable, flagged)
	}
}
