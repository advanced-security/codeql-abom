package checks

import (
	"fmt"
	"maps"
	"slices"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// invariants is check 9, one part per invariant, read on the rebuilt copy so all six see one state.
func invariants(s *store.Store) ([]store.CheckPart, error) {
	var out []store.CheckPart
	for _, read := range []func(*store.Store) (store.CheckPart, error){
		dependencyTotals, rollups, startingPointsOnly, kindAgreement, unobservedCommits,
		relationshipLines,
	} {
		p, err := read(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func disagree(p *store.CheckPart, subject, reading, claim, independent string) {
	p.Disagreements = append(p.Disagreements, store.Disagreement{Subject: subject, Reading: reading,
		Claim: claim, Independent: independent})
}

func skip(p *store.CheckPart, counts map[string]int) {
	for _, reason := range slices.Sorted(maps.Keys(counts)) {
		p.Skipped = append(p.Skipped, store.CheckSkip{Reason: reason, Count: counts[reason]})
	}
}

// dependencyTotals is 9a: the Dashboard's count links to the Actions unit, so both are one number.
func dependencyTotals(s *store.Store) (store.CheckPart, error) {
	p := store.CheckPart{Part: "9a the Dashboard's dependency count is the Actions unit's total"}
	summary, err := s.Summary()
	if err != nil {
		return p, err
	}
	page, err := s.ActionsInventoryPage(store.InventoryQuery{Limit: 1})
	if err != nil {
		return p, err
	}
	p.Compared = 1
	if summary.Dependencies != page.Total {
		disagree(&p, "dependencies", p.Part, fmt.Sprintf("the Dashboard counts %d", summary.Dependencies),
			fmt.Sprintf("the Actions unit lists %d", page.Total))
	}
	return p, nil
}

// rollups is 9b: each reach rollup against the breakdown a grouping recounts it from.
func rollups(s *store.Store) (store.CheckPart, error) {
	p := store.CheckPart{Part: "9b each reach rollup is the sum of its breakdown"}
	levels, err := s.RollupLevels()
	if err != nil {
		return p, err
	}
	unstored := map[string]int{}
	for _, l := range levels {
		reading := "9b " + l.Level
		switch {
		case !l.HasRow:
			p.Compared++
			disagree(&p, l.Node, reading, "no abom_blast_radius row",
				fmt.Sprintf("%d abom_node_reach rows", l.Breakdown.Int64))
		case !l.Rollup.Valid:
			unstored["the rollup does not store "+l.Level]++
		case !l.Breakdown.Valid:
			unstored["the breakdown does not store "+l.Level+" for every repository"]++
		default:
			p.Compared++
			if l.Rollup.Int64 != l.Breakdown.Int64 {
				disagree(&p, l.Node, reading, fmt.Sprintf("%s = %d", l.Level, l.Rollup.Int64),
					fmt.Sprintf("the breakdown sums to %d", l.Breakdown.Int64))
			}
		}
	}
	skip(&p, unstored)
	return p, nil
}

// startingPointsOnly is 9c: upstream repositories are not dependents.
func startingPointsOnly(s *store.Store) (store.CheckPart, error) {
	p := store.CheckPart{Part: "9c reach names only starting points"}
	total, rows, err := s.ReachOutsideStartingPoints()
	if err != nil {
		return p, err
	}
	p.Compared = total
	for _, r := range rows {
		origin := "origin " + r.Origin
		if r.Origin == "" {
			origin = "no repository has this id"
		}
		disagree(&p, r.Node+" -> "+r.Repository, "9c "+r.Table, "names "+r.Repository+" as a dependent",
			origin)
	}
	return p, nil
}

// kindAgreement is 9d: a `needs:` names a job and a `uses:` never does, so a declaration read with
// a kind its target contradicts was given another commit's kind, as every sighting was before they
// kept their own.
func kindAgreement(s *store.Store) (store.CheckPart, error) {
	p := store.CheckPart{Part: "9d every declaration's kind agrees with its target"}
	k, err := s.KindAgreement()
	if err != nil {
		return p, err
	}
	p.Compared = k.Declarations
	for _, c := range k.Contradictions {
		subject := c.Location
		if c.Commit != "" {
			subject += "@" + c.Commit
		}
		disagree(&p, subject, "9d declaration kind against its target",
			c.Kind+" in "+c.Repository, "names "+c.TargetKind+" "+c.Target)
	}
	return p, nil
}

// unobservedCommits is 9e: no live commit whose declarations are unknown goes unreported.
func unobservedCommits(s *store.Store) (store.CheckPart, error) {
	p := store.CheckPart{Part: "9e every live commit with unknown declarations is reported"}
	evidence, err := s.LiveCommitEvidence()
	if err != nil {
		return p, err
	}
	reported, err := s.UnobservedCommits()
	if err != nil {
		return p, err
	}
	count, err := s.UnobservedCommitCount()
	if err != nil {
		return p, err
	}
	unknown := 0
	live := map[string]bool{}
	for _, e := range evidence {
		live[e.Commit] = true
		p.Compared++
		isUnknown := e.Answers > 0 && !e.Content && e.Analyzed >= 2
		if isUnknown {
			unknown++
		}
		switch {
		case isUnknown && !reported[e.Commit]:
			disagree(&p, e.Commit, "9e unobserved", "the objective does not report it",
				"every edges run answering for it predates sighting content")
		case !isUnknown && reported[e.Commit]:
			disagree(&p, e.Commit, "9e unobserved", "the objective reports it as unobserved",
				fmt.Sprintf("%d runs answer for it, content kept: %t, its repository analyzed at %d",
					e.Answers, e.Content, e.Analyzed))
		case e.Answers == 0 && !e.Outstanding:
			disagree(&p, e.Commit, "9e no evidence", "the queue calls it done",
				"no edges run answers for it, at it or at a commit with identical content")
		}
	}
	for _, c := range slices.Sorted(maps.Keys(reported)) {
		if !live[c] {
			disagree(&p, c, "9e unobserved", "the objective reports it as unobserved",
				"no queue row resolves to it")
		}
	}
	if count != unknown {
		disagree(&p, "objective", "9e unobserved count", fmt.Sprintf("the objective reports %d", count),
			fmt.Sprintf("%d live commits have unknown declarations", unknown))
	}
	return p, nil
}

// relationshipLines is 9f: Relationships accounts for every line in scope, listed or undeclared.
func relationshipLines(s *store.Store) (store.CheckPart, error) {
	p := store.CheckPart{Part: "9f relationship lines and lines without a declaration are the lines in scope"}
	page, err := s.RelationshipsPage(store.RelationshipsQuery{Limit: 1})
	if err != nil {
		return p, err
	}
	lines, err := s.LinesInScope()
	if err != nil {
		return p, err
	}
	p.Compared = 1
	if page.Lines+page.WithoutDeclaration != lines {
		disagree(&p, "relationships", p.Part,
			fmt.Sprintf("%d lines listed + %d without a declaration = %d", page.Lines,
				page.WithoutDeclaration, page.Lines+page.WithoutDeclaration),
			fmt.Sprintf("%d lines in scope", lines))
	}
	return p, nil
}
