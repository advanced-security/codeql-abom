// Package checks compares what ABOM claims with independent readings, writing only its own tables.
package checks

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Check is one of the comparisons plans/tools/graph-consistency-checker.md names.
type Check struct {
	Number  int    `json:"number"`
	Claim   string `json:"claim"`
	Reading string `json:"reading"`
	// BuiltBy names, by its title, the plan item that builds a check this build does not run. Never
	// by number: the plan's items are renumbered as they land.
	BuiltBy string `json:"builtBy,omitempty"`
}

const (
	external = "the external checks, 1 to 4"
	runLogs  = "measure what run logs record, then check 5"
)

// Catalog lists every check the plan names, so one that is not running says so.
var Catalog = []Check{
	{1, "What a commit declared", "The same commit's files, read by an independent parser",
		external},
	{2, "An owned repository's direct dependencies",
		"GitHub's dependency graph, at the default-branch head only", external},
	{3, "What a reference resolves to", "The reference now", external},
	{4, "Which commits of an owned repository are live", "Its default and protected branch heads now",
		external},
	{5, "A workflow's closure at a commit", "The actions that commit's run downloaded, with their SHAs",
		runLogs},
	{6, "An owned repository's reach", "A CodeQL walk over its closure database",
		"check 6 as a sample, once the reachability crosscheck reports"},
	{7, "Incrementally maintained reach, exposure and advisory routes", "The wholesale recompute", ""},
	{8, "Exposure, closure and reach", "Each other, on every owned repository", ""},
	{9, "Graph-wide invariants", "None: each invariant holds two readings ABOM makes to each other", ""},
	{10, "The freshness each pool's estimate shows", "The lag checks 3 and 4 measured",
		"nothing planned yet: it needs the checker's pool and the lag the external checks measure"},
	{11, "Work put back is done when claimed", "The ledger after the cycle that claimed it",
		"check 11, from the queue and the ledger alone"},
}

// class is assigned by rule: 7 to 9 compare ABOM with itself, so only derivation can be at fault.
var class = map[int]string{7: "derivation", 8: "derivation", 9: "derivation"}

// Run runs every built check once on copies of src made under dir, and files each run into src.
func Run(src *store.Store, build, dir string) ([]store.CheckRun, error) {
	if err := src.MigrateChecks(); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(dir, "abom-check-")
	if err != nil {
		return nil, fmt.Errorf("making a place for the copies: %w", err)
	}
	defer os.RemoveAll(tmp)

	started := time.Now()
	incremental, wholesale, err := copies(src, tmp)
	if err != nil {
		return nil, err
	}
	defer incremental.Close()
	defer wholesale.Close()

	var runs []store.CheckRun
	file := func(check int, at time.Time, parts []store.CheckPart, err error) error {
		if err != nil {
			parts = []store.CheckPart{{Part: fmt.Sprintf("check %d", check),
				Skipped: []store.CheckSkip{{Reason: "could not be read: " + err.Error(), Count: 1}}}}
		}
		run := store.CheckRun{Check: check, Build: build, Parts: settle(check, parts),
			StartedAt: at.UTC().Format(time.RFC3339), WallMillis: time.Since(at).Milliseconds()}
		id, err := src.RecordCheckRun(run)
		if err != nil {
			return err
		}
		run.ID = id
		runs = append(runs, run)
		return nil
	}

	parts, err := derivations(incremental, wholesale)
	if err := file(7, started, parts, err); err != nil {
		return runs, err
	}
	if err != nil {
		// 8 and 9 read the recompute, and a partial one would confound them.
		for _, check := range []int{8, 9} {
			if err := file(check, time.Now(), nil, err); err != nil {
				return runs, err
			}
		}
		return runs, nil
	}
	at := time.Now()
	parts, err = agreement(wholesale)
	if err := file(8, at, parts, err); err != nil {
		return runs, err
	}
	at = time.Now()
	parts, err = invariants(wholesale)
	if err := file(9, at, parts, err); err != nil {
		return runs, err
	}
	return runs, nil
}

// copies takes the second copy from the first, so both start from one state whatever src commits.
func copies(src *store.Store, dir string) (incremental, wholesale *store.Store, err error) {
	open := func(path string) (*store.Store, error) {
		s, err := store.Open(path, store.Options{})
		if err != nil {
			return nil, err
		}
		if err := s.Migrate(); err != nil {
			s.Close()
			return nil, err
		}
		return s, nil
	}
	a, b := filepath.Join(dir, "incremental.db"), filepath.Join(dir, "wholesale.db")
	if err := src.SnapshotInto(a); err != nil {
		return nil, nil, err
	}
	if incremental, err = open(a); err != nil {
		return nil, nil, err
	}
	if err := incremental.SnapshotInto(b); err != nil {
		incremental.Close()
		return nil, nil, err
	}
	if wholesale, err = open(b); err != nil {
		incremental.Close()
		return nil, nil, err
	}
	return incremental, wholesale, nil
}

// settle counts and classes disagreements as the store will; nothing compared leaves no count.
func settle(check int, parts []store.CheckPart) []store.CheckPart {
	for i := range parts {
		p := &parts[i]
		if p.Skipped == nil {
			p.Skipped = []store.CheckSkip{}
		}
		if p.Compared > 0 || len(p.Disagreements) > 0 {
			n := len(p.Disagreements)
			p.Disagreed = &n
		}
		for j := range p.Disagreements {
			p.Disagreements[j].Check, p.Disagreements[j].Part = check, p.Part
			p.Disagreements[j].Class = class[check]
		}
	}
	return parts
}

// derivations is check 7: production's incremental path on one copy, its rebuild on the other.
func derivations(incremental, wholesale *store.Store) ([]store.CheckPart, error) {
	_, refused := (&graph.Builder{Store: incremental}).Derive()
	if _, err := (&graph.Builder{Store: wholesale}).Rebuild(); err != nil {
		return nil, fmt.Errorf("the wholesale recompute failed: %w", err)
	}
	oracle, err := wholesale.DerivedRows()
	if err != nil {
		return nil, err
	}
	if refused != nil {
		parts := make([]store.CheckPart, 0, len(oracle))
		for _, table := range oracle {
			subjects := map[string]bool{}
			for _, r := range table.Rows {
				subjects[r.Subject] = true
			}
			parts = append(parts, store.CheckPart{Part: table.Name, Skipped: []store.CheckSkip{{
				Reason: "the incremental path refused: " + refused.Error(), Count: len(subjects)}}})
		}
		return parts, nil
	}
	derived, err := incremental.DerivedRows()
	if err != nil {
		return nil, err
	}
	return graph.CompareDerived(derived, oracle), nil
}

// agreement is check 8: reach against the exposure walk, and against each owned closure.
func agreement(s *store.Store) ([]store.CheckPart, error) {
	walk, err := (&graph.Builder{Store: s}).ExposureWalkAgreement()
	if err != nil {
		return nil, err
	}
	owned, err := s.OwnedRepositories()
	if err != nil {
		return nil, err
	}
	closure, err := graph.ClosureAgreement(s, owned)
	if err != nil {
		return nil, err
	}
	if len(owned) == 0 {
		for i := range closure {
			closure[i].Skipped = append(closure[i].Skipped, store.CheckSkip{
				Reason: "no repository is owned, so no closure was read", Count: 1})
		}
	}
	return append(walk, closure...), nil
}
