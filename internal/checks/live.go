package checks

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

const readingLive = "4 live commits"

// liveSide is one owned repository as both sides hold it: the depth-0 queue rows that make its
// commits live, and its branch heads now.
type liveSide struct {
	name, owner, repo string
	// defaultName is the default branch ABOM recorded; read says ABOM read its protected branches.
	defaultName string
	read        bool
	defaultRow  *store.QueueClaim
	protected   map[string]store.QueueClaim
	// protectedNow is what GitHub lists as protected now; listErr why it listed nothing.
	protectedNow []string
	listErr      error
}

// liveOutcome is what comparing one repository came to.
type liveOutcome struct {
	compared     bool
	disagreement *store.Disagreement
	skipped      map[string]int
}

// liveCommits is check 4: each owned repository's live commits — its default branch and its
// protected branches, as its depth-0 queue rows stand — against those branches' heads now.
func (c *Checker) liveCommits(ctx context.Context, s *store.Store) ([]store.CheckPart, error) {
	owned, err := s.OwnedRepositories()
	if err != nil {
		return nil, err
	}
	claims, err := s.QueueClaims()
	if err != nil {
		return nil, err
	}
	rowsOf := map[string][]store.QueueClaim{}
	for _, q := range claims {
		if q.Depth == 0 {
			rowsOf[strings.ToLower(q.NodeKey)] = append(rowsOf[strings.ToLower(q.NodeKey)], q)
		}
	}
	open, err := c.Store.OpenSubjects(4)
	if err != nil {
		return nil, err
	}
	cands := make([]candidate, len(owned))
	for i, name := range owned {
		changed := ""
		for _, q := range rowsOf[strings.ToLower(name)] {
			changed = max(changed, q.Changed)
		}
		cands[i] = candidate{key: name, stratum: stratumOwned, changed: changed}
	}
	claimed, gone := openAmong(open, cands)
	d := drawSample(cands, claimed, c.Recent, c.Sample, c.rng())

	sides := map[int]*liveSide{}
	var qs []catalog.RefQuestion
	for _, i := range d.order() {
		side, err := abomSide(s, owned[i], rowsOf[strings.ToLower(owned[i])])
		if err != nil {
			return nil, err
		}
		sides[i] = side
		if side.defaultRow == nil || side.defaultRow.Commit == "" {
			continue
		}
		qs = append(qs, catalog.RefQuestion{Owner: side.owner, Repo: side.repo})
		// Asked only where ABOM can say which branches it protects, so the answer can be compared.
		if !side.read || side.defaultName == "" {
			continue
		}
		side.protectedNow, side.listErr = c.GitHub.ProtectedBranches(ctx, side.name)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		for _, b := range side.protectedNow {
			if b != side.defaultName {
				qs = append(qs, catalog.RefQuestion{Owner: side.owner, Repo: side.repo, Ref: "refs/heads/" + b})
			}
		}
	}
	got, err := c.resolve(ctx, qs)
	if err != nil {
		return nil, err
	}
	outcomes := map[int]liveOutcome{}
	for i, side := range sides {
		outcomes[i] = compareLive(side, got)
	}

	compare := func(name string, idx []int, population int) store.CheckPart {
		p := store.CheckPart{Part: name, Population: &population}
		skips := map[string]int{}
		for _, i := range idx {
			o := outcomes[i]
			for reason, n := range o.skipped {
				skips[reason] += n
			}
			switch {
			case !o.compared:
			case o.disagreement != nil:
				p.Compared++
				p.Disagreements = append(p.Disagreements, *o.disagreement)
			default:
				p.Compared++
				p.Agreed = append(p.Agreed, store.CheckSubject{Subject: owned[i], Reading: readingLive})
			}
		}
		skip(&p, skips)
		return p
	}
	reread := compare("4a open disagreements, read again", d.reread, len(open))
	withdrawn(&reread, gone, "settled: it is no longer owned, so nothing claims it")
	random := compare("4c owned repositories, sampled at random", d.random[stratumOwned], d.sizes[stratumOwned])
	bounded(&random)
	return []store.CheckPart{reread,
		compare("4b owned repositories that changed most recently", d.recent, len(cands)), random}, nil
}

// abomSide reads what ABOM holds of one owned repository's live commits.
func abomSide(s *store.Store, name string, rows []store.QueueClaim) (*liveSide, error) {
	owner, repo, _ := strings.Cut(name, "/")
	side := &liveSide{name: name, owner: owner, repo: repo, protected: map[string]store.QueueClaim{}}
	var err error
	if side.defaultName, _, err = s.DefaultBranch(name); err != nil {
		return nil, err
	}
	if _, side.read, err = s.ProtectedBranches(name); err != nil {
		return nil, err
	}
	for _, q := range rows {
		if q.Ref == "" {
			side.defaultRow = &q
		} else {
			side.protected[q.Ref] = q
		}
	}
	return side, nil
}

// compareLive compares one repository's live commits with its branch heads now. Its protected
// branches are compared only where both sides can say which they are; otherwise the default branch
// is compared alone and the run says why.
func compareLive(side *liveSide, got map[catalog.RefQuestion]answer) liveOutcome {
	out := liveOutcome{skipped: map[string]int{}}
	if side.defaultRow == nil || side.defaultRow.Commit == "" {
		out.skipped["ABOM stands at no commit of its default branch yet"]++
		return out
	}
	head := got[catalog.RefQuestion{Owner: side.owner, Repo: side.repo}]
	if !head.read() {
		out.skipped[head.unread()]++
		return out
	}
	ours := map[string]*store.QueueClaim{"": side.defaultRow}
	theirs := map[string]string{"": strings.ToLower(head.Commit)}
	if why := protectedUnknown(side, got); why != "" {
		out.skipped["protected branches not compared: "+why]++
	} else {
		for ref, q := range side.protected {
			ours[ref] = &q
		}
		for _, b := range side.protectedNow {
			if b != side.defaultName {
				theirs[b] = strings.ToLower(got[catalog.RefQuestion{Owner: side.owner, Repo: side.repo,
					Ref: "refs/heads/" + b}].Commit)
			}
		}
	}
	out.compared = true
	differs, noticed := false, true
	for _, branch := range slices.Sorted(maps.Keys(union(ours, theirs))) {
		q, live := ours[branch]
		commit, isHead := theirs[branch]
		if live && isHead && q.Commit != "" && q.Commit == commit {
			continue
		}
		differs = true
		noticed = noticed && live && queued(q.Status)
	}
	if !differs {
		return out
	}
	class := "unnoticed"
	if noticed {
		class = "staleness"
	}
	out.disagreement = &store.Disagreement{Subject: side.name, Reading: readingLive, Class: class,
		Claim: "ABOM stands at " + describeOurs(ours), Independent: "now " + describeTheirs(theirs)}
	return out
}

// protectedUnknown says why the protected branches cannot be compared, or nothing where they can.
func protectedUnknown(side *liveSide, got map[catalog.RefQuestion]answer) string {
	switch {
	case !side.read:
		return "ABOM has not read which branches it protects"
	case side.defaultName == "":
		return "ABOM has no default branch on record, so it keeps no protected branch live"
	case side.listErr != nil:
		return "GitHub did not list them"
	}
	for _, b := range side.protectedNow {
		if b == side.defaultName {
			continue
		}
		if a := got[catalog.RefQuestion{Owner: side.owner, Repo: side.repo, Ref: "refs/heads/" + b}]; a.Commit == "" {
			return "GitHub did not say where one points"
		}
	}
	return ""
}

func union(ours map[string]*store.QueueClaim, theirs map[string]string) map[string]bool {
	out := map[string]bool{}
	for k := range ours {
		out[k] = true
	}
	for k := range theirs {
		out[k] = true
	}
	return out
}

func label(branch string) string {
	if branch == "" {
		return "default branch"
	}
	return branch
}

func describeOurs(ours map[string]*store.QueueClaim) string {
	var parts []string
	for _, b := range slices.Sorted(maps.Keys(ours)) {
		q := ours[b]
		if q.Commit == "" {
			parts = append(parts, label(b)+" queued, no commit yet")
			continue
		}
		parts = append(parts, label(b)+" at "+q.Commit+" ("+held(*q)+")")
	}
	return strings.Join(parts, "; ")
}

func describeTheirs(theirs map[string]string) string {
	var parts []string
	for _, b := range slices.Sorted(maps.Keys(theirs)) {
		if theirs[b] == "" {
			parts = append(parts, "no "+label(b))
			continue
		}
		parts = append(parts, label(b)+" at "+theirs[b])
	}
	return strings.Join(parts, "; ")
}
