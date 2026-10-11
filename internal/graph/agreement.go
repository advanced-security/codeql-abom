package graph

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Names of the comparisons, which are the parts a check run reports.
const (
	walkReachPart       = "abom_node_reach / exposure walk"
	walkVersionPart     = "abom_node_version_reach / exposure walk"
	closureReachPart    = "reach / submission closure"
	closureExposurePart = "exposure / submission closure"
)

// protectedBranchOnly names a difference by design, so it reads as neither agreement nor a defect.
const protectedBranchOnly = "excluded by rule: only a protected branch declares it, and a " +
	"snapshot is filed under the default branch"

// CompareDerived holds one derivation's rows to another's, listing per subject what only each holds.
func CompareDerived(derived, oracle []store.DerivedTable) []store.CheckPart {
	out := make([]store.CheckPart, 0, len(oracle))
	for i, table := range oracle {
		var mine []store.DerivedRow
		if i < len(derived) && derived[i].Name == table.Name {
			mine = derived[i].Rows
		}
		got, want := bySubject(mine), bySubject(table.Rows)
		part := store.CheckPart{Part: table.Name, Skipped: []store.CheckSkip{}}
		subjects := slices.Sorted(maps.Keys(want))
		for subject := range got {
			if _, ok := want[subject]; !ok {
				subjects = append(subjects, subject)
			}
		}
		for _, subject := range subjects {
			part.Compared++
			only, onlyOracle := onlyIn(got[subject], want[subject]), onlyIn(want[subject], got[subject])
			if len(only)+len(onlyOracle) == 0 {
				continue
			}
			part.Disagreements = append(part.Disagreements, store.Disagreement{
				Subject: subject, Reading: table.Name,
				Claim: rowsOrNone(only), Independent: rowsOrNone(onlyOracle)})
		}
		out = append(out, part)
	}
	return out
}

func bySubject(rows []store.DerivedRow) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, r := range rows {
		if out[r.Subject] == nil {
			out[r.Subject] = map[string]bool{}
		}
		out[r.Subject][r.Row] = true
	}
	return out
}

func onlyIn(a, b map[string]bool) []string {
	var out []string
	for row := range a {
		if !b[row] {
			out = append(out, row)
		}
	}
	slices.Sort(out)
	return out
}

func rowsOrNone(rows []string) string {
	if len(rows) == 0 {
		return "no row"
	}
	return strings.Join(rows, "; ")
}

// ExposureWalkAgreement holds stored reach and its per-version union to the walk exposure takes.
func (b *Builder) ExposureWalkAgreement() ([]store.CheckPart, error) {
	m, err := b.load()
	if err != nil {
		return nil, err
	}
	stored, versions, err := b.Store.StoredReach()
	if err != nil {
		return nil, err
	}
	names, err := b.Store.RepositoryNames()
	if err != nil {
		return nil, err
	}
	walked := map[int64]map[int64]int{}
	for _, node := range m.referenceable {
		walked[node] = map[int64]int{}
		for repo, p := range m.exposureOf(node, b.MaxDepth) {
			walked[node][repo] = p.hops
		}
	}
	compare := func(name string, claims map[int64]map[int64]int) store.CheckPart {
		part := store.CheckPart{Part: name, Skipped: []store.CheckSkip{}}
		nodes := slices.Collect(maps.Keys(walked))
		for node := range claims {
			if _, ok := walked[node]; !ok {
				nodes = append(nodes, node)
			}
		}
		slices.Sort(nodes)
		for _, node := range nodes {
			got, want := claims[node], walked[node]
			if len(got)+len(want) == 0 {
				continue
			}
			part.Compared++
			if maps.Equal(got, want) {
				continue
			}
			key := m.keyOf[node]
			if key == "" {
				key = fmt.Sprintf("#%d", node)
			}
			part.Disagreements = append(part.Disagreements, store.Disagreement{
				Subject: key, Reading: name,
				Claim: hopsOf(got, names), Independent: hopsOf(want, names)})
		}
		return part
	}
	return []store.CheckPart{compare(walkReachPart, stored), compare(walkVersionPart, versions)}, nil
}

// hopsOf renders repositories and hops as `owner/name:hops`, sorted.
func hopsOf(repos map[int64]int, names map[int64]string) string {
	if len(repos) == 0 {
		return "reaches nobody"
	}
	out := make([]string, 0, len(repos))
	for repo, hops := range repos {
		name := names[repo]
		if name == "" {
			name = fmt.Sprintf("#%d", repo)
		}
		out = append(out, fmt.Sprintf("%s:%d", name, hops))
	}
	slices.Sort(out)
	return strings.Join(out, ", ")
}

// ClosureAgreement holds each repository's reach and exposure to its closure, computed in SQL.
func ClosureAgreement(s store.Backend, repositories []string) ([]store.CheckPart, error) {
	reached, exposed, err := s.RepositoryReach()
	if err != nil {
		return nil, err
	}
	reach := store.CheckPart{Part: closureReachPart, Skipped: []store.CheckSkip{}}
	exposure := store.CheckPart{Part: closureExposurePart, Skipped: []store.CheckSkip{}}
	excluded := 0
	for _, repository := range repositories {
		submitted, err := s.SubmissionClosure(repository)
		if err != nil {
			return nil, err
		}
		starting, err := s.StartingPointClosure(repository)
		if err != nil {
			return nil, err
		}
		snapshot, everywhere := closureKeys(submitted), closureKeys(starting)
		r := folded(reached[repository])
		nodes := map[string]bool{}
		for _, set := range []map[string]bool{r, snapshot, everywhere} {
			maps.Copy(nodes, set)
		}
		for _, node := range slices.Sorted(maps.Keys(nodes)) {
			inReach, inSnapshot, inEverywhere := r[node], snapshot[node], everywhere[node]
			switch {
			case inReach && inSnapshot && inEverywhere:
				reach.Compared++
			case inReach && !inSnapshot && inEverywhere:
				excluded++
			default:
				reach.Compared++
				reach.Disagreements = append(reach.Disagreements, store.Disagreement{
					Subject: repository + " -> " + node, Reading: closureReachPart,
					Claim: carried("reach", inReach),
					Independent: carried("the submission closure", inSnapshot) + "; " +
						carried("the closure at every starting commit", inEverywhere)})
			}
		}
		for _, node := range slices.Sorted(maps.Keys(folded(exposed[repository]))) {
			exposure.Compared++
			if !everywhere[node] {
				exposure.Disagreements = append(exposure.Disagreements, store.Disagreement{
					Subject: repository + " -> " + node, Reading: closureExposurePart,
					Claim:       "exposure names it",
					Independent: carried("the closure at every starting commit", false)})
			}
		}
	}
	if excluded > 0 {
		reach.Skipped = append(reach.Skipped, store.CheckSkip{Reason: protectedBranchOnly, Count: excluded})
	}
	return []store.CheckPart{reach, exposure}, nil
}

func carried(by string, yes bool) string {
	if yes {
		return by + " carries it"
	}
	return by + " does not carry it"
}

// closureKeys keys entries as nodes are keyed, folding the case an entry's owner was written in.
func closureKeys(entries []store.ClosureEntry) map[string]bool {
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		key := e.Owner + "/" + e.Repo
		if e.Subpath != "" {
			key += "/" + e.Subpath
		}
		out[strings.ToLower(key)] = true
	}
	return out
}

func folded(keys map[string]bool) map[string]bool {
	out := make(map[string]bool, len(keys))
	for key := range keys {
		out[strings.ToLower(key)] = true
	}
	return out
}
