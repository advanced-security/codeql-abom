package store

import "testing"

// A run reached by two references must record both.
//
// The run key hashes the commit, so two analyses of the same repository at the same commit
// collapse into one ledger row — that is deliberate, and it is why the ref cannot live in a
// column on that row. Measured on the seeded corpus 2026-09-10: 392 repository/commit pairs are
// reached by more than one ref, so a column would name one and silently drop the rest.
// Content equivalence is not analysis. A round skipped because some other commit had identical
// content did not analyze this one, and attaching this ref to that run would claim a revision was
// examined that never was.
func TestARefIsNotAttachedToARunAtAnotherCommit(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "abom/edges", Repository: "octo/app", DBCommit: "sha-one",
	}); err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	found, err := s.AttachQueryRunRef(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "abom/edges", Repository: "octo/app", DBCommit: "sha-two",
		Ref: "v5", AnalyzedRef: "v5",
	})
	if err != nil {
		t.Fatalf("AttachQueryRunRef: %v", err)
	}
	if found {
		t.Error("a ref was attached to a run recorded at a different commit")
	}
}

func TestARunReachedByTwoRefsRecordsBoth(t *testing.T) {
	s := newTestStore(t)

	run := QueryRun{AnalysisMode: AnalysisModeRegular, QueryID: "abom/edges", Repository: "octo/app", DBCommit: "sha-one"}

	first := run
	first.Ref, first.AnalyzedRef = "v4", "v4"
	if _, err := s.RecordQueryRun(first); err != nil {
		t.Fatalf("RecordQueryRun(v4): %v", err)
	}
	second := run
	second.Ref, second.AnalyzedRef = "v5", "v5"
	id, err := s.RecordQueryRun(second)
	if err != nil {
		t.Fatalf("RecordQueryRun(v5): %v", err)
	}

	refs, err := s.QueryRunRefs(id)
	if err != nil {
		t.Fatalf("QueryRunRefs: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("refs = %v, want both v4 and v5", refs)
	}
}

// The ref must not join the run's identity, or `QueryRunExistsForContent` stops skipping: it
// deliberately matches on content rather than commit, because CodeQL is deterministic and
// byte-identical sources cannot answer differently. Two refs at one commit are one evaluation.
func TestTheRefIsNotPartOfARunsIdentity(t *testing.T) {
	s := newTestStore(t)

	run := QueryRun{AnalysisMode: AnalysisModeRegular, QueryID: "abom/edges", Repository: "octo/app", DBCommit: "sha-one"}

	first := run
	first.Ref, first.AnalyzedRef = "v4", "v4"
	a, err := s.RecordQueryRun(first)
	if err != nil {
		t.Fatalf("RecordQueryRun(v4): %v", err)
	}
	second := run
	second.Ref, second.AnalyzedRef = "v5", "v5"
	b, err := s.RecordQueryRun(second)
	if err != nil {
		t.Fatalf("RecordQueryRun(v5): %v", err)
	}

	if a != b {
		t.Errorf("two refs at one commit produced runs %d and %d, want one evaluation", a, b)
	}
}

// Nobody recorded a ref is a different statement from the default branch, which the frontier
// already spells as the empty ref. Inventing a row here would turn an absent measurement into a
// specific claim about which branch was analyzed.
func TestARunWhoseRefNobodyKnowsRecordsNone(t *testing.T) {
	s := newTestStore(t)

	id, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
		QueryID: "abom/edges", Repository: "octo/app", DBCommit: "sha-one",
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	refs, err := s.QueryRunRefs(id)
	if err != nil {
		t.Fatalf("QueryRunRefs: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("refs = %v, want none recorded for a run nobody gave a ref", refs)
	}
}
