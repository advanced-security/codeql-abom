package store

import (
	"reflect"
	"testing"
)

func lockCoverageAt(t *testing.T, s *Store, repository, commit string) []LockCoverage {
	t.Helper()
	rows, err := s.DB().Query(`
		SELECT workflow_path, coverage, schema_version, lock_digest, reason FROM abom_lock_coverage
		 WHERE repository = ? AND commit_sha = ? ORDER BY workflow_path`, repository, commit)
	if err != nil {
		t.Fatalf("reading coverage: %v", err)
	}
	defer rows.Close()
	out := []LockCoverage{}
	for rows.Next() {
		var c LockCoverage
		if err := rows.Scan(&c.WorkflowPath, &c.Coverage, &c.Schema, &c.Digest, &c.Reason); err != nil {
			t.Fatalf("reading coverage: %v", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading coverage: %v", err)
	}
	return out
}

// Two commits with identical content hold identical locks, so a reading made at one answers for
// the other: coverage, entries, their DAG and every workflow's pins, or discovery reads a lock
// that selects nothing.
func TestALockReadingCarriesToACommitWithIdenticalContent(t *testing.T) {
	s := newTestStore(t)
	recordContent(t, s, "octo/app", "c1", contentA)
	if err := s.RecordLockReading("octo/app", "c1", testReader, coveredRows(), pagesGraph()); err != nil {
		t.Fatalf("RecordLockReading: %v", err)
	}

	carried, err := s.CarryLockReading("octo/app", "c2", contentA, testReader)
	if err != nil {
		t.Fatalf("CarryLockReading: %v", err)
	}
	if !carried {
		t.Fatal("nothing was carried from a commit with identical content and a current reading")
	}
	if got, err := s.HasLockCoverage("octo/app", "c2", testReader); err != nil || !got {
		t.Errorf("HasLockCoverage after carrying = %v, %v; want true", got, err)
	}
	if want, got := lockCoverageAt(t, s, "octo/app", "c1"), lockCoverageAt(t, s, "octo/app", "c2"); !reflect.DeepEqual(got, want) {
		t.Errorf("carried coverage %+v, want %+v", got, want)
	}
	want, err := s.LockGraphAt("octo/app", "c1")
	if err != nil {
		t.Fatalf("LockGraphAt: %v", err)
	}
	got, err := s.LockGraphAt("octo/app", "c2")
	if err != nil {
		t.Fatalf("LockGraphAt: %v", err)
	}
	if len(want.Dependencies) == 0 || !reflect.DeepEqual(got, want) {
		t.Errorf("carried graph %+v, want %+v", got, want)
	}
}

// Nothing carries where nothing vouches for it: other content, another repository's identical
// content, or a reading this reader would make again.
func TestALockReadingCarriesOnlyWhatThisRepositoryReadOfTheSameContent(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, s *Store){
		"different content": func(t *testing.T, s *Store) {
			recordContent(t, s, "octo/app", "c1", contentB)
			if err := s.RecordLockReading("octo/app", "c1", testReader, coveredRows(), pagesGraph()); err != nil {
				t.Fatal(err)
			}
		},
		"a fork with the same content": func(t *testing.T, s *Store) {
			recordContent(t, s, "fork/app", "c1", contentA)
			if err := s.RecordLockReading("fork/app", "c1", testReader, coveredRows(), pagesGraph()); err != nil {
				t.Fatal(err)
			}
		},
		"another reader's reading": func(t *testing.T, s *Store) {
			recordContent(t, s, "octo/app", "c1", contentA)
			if err := s.RecordLockReading("octo/app", "c1", "old-parser", coveredRows(), pagesGraph()); err != nil {
				t.Fatal(err)
			}
		},
		"no reading at all": func(t *testing.T, s *Store) {
			recordContent(t, s, "octo/app", "c1", contentA)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			setup(t, s)
			carried, err := s.CarryLockReading("octo/app", "c2", contentA, testReader)
			if err != nil {
				t.Fatalf("CarryLockReading: %v", err)
			}
			if carried {
				t.Error("a reading was carried with nothing vouching for it")
			}
			if rows := lockCoverageAt(t, s, "octo/app", "c2"); len(rows) != 0 {
				t.Errorf("c2 holds coverage %+v", rows)
			}
		})
	}
}

// Only a digest in the scheme a caller can compute is worth reading a tree for, only in the
// repository asked about, and only on a commit other than the one being asked about: a commit's
// own record is already the answer a listing would give.
func TestCommitContentIsFoundByItsLabelOnAnotherCommit(t *testing.T) {
	s := newTestStore(t)
	recordContent(t, s, "octo/app", "c1", "filter-a:tree1:0123")
	recordContent(t, s, "octo/old", "c1", "filter-a:0123")

	for _, c := range []struct {
		repository, label, except string
		want                      bool
	}{
		{"octo/app", "filter-a:tree1:", "c2", true},
		{"octo/app", "filter-a:tree1:", "c1", false},
		{"octo/app", "filter-b:tree1:", "c2", false},
		{"octo/old", "filter-a:tree1:", "c2", false},
		{"octo/none", "filter-a:tree1:", "c2", false},
	} {
		got, err := s.HasCommitContentLabeled(c.repository, c.label, c.except)
		if err != nil {
			t.Fatalf("HasCommitContentLabeled: %v", err)
		}
		if got != c.want {
			t.Errorf("HasCommitContentLabeled(%s, %s, except %s) = %v, want %v",
				c.repository, c.label, c.except, got, c.want)
		}
	}
}
