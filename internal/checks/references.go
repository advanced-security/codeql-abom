package checks

import (
	"context"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

const readingReference = "3 where the reference points"

// references is check 3: each reference the queue resolved, against where it points now. An owned
// repository's own branches are check 4's, which compares them as one set of live commits.
func (c *Checker) references(ctx context.Context, s store.Backend) ([]store.CheckPart, error) {
	claims, err := s.QueueClaims()
	if err != nil {
		return nil, err
	}
	open, err := c.Store.OpenSubjects(3)
	if err != nil {
		return nil, err
	}
	var rows []store.QueueClaim
	var cands []candidate
	excluded := map[string]map[string]int{stratumOwned: {}, stratumExternal: {}}
	for _, q := range claims {
		if q.Depth == 0 && q.Owned {
			continue
		}
		stratum := stratumExternal
		if q.Owned {
			stratum = stratumOwned
		}
		switch {
		case q.Commit == "":
			excluded[stratum]["ABOM has resolved no commit for it yet"]++
		// The recheck sweep's rule: an object id cannot move, whether it names a commit or an
		// annotated tag peeled to one, and GitHub resolves neither as a branch or tag name.
		case namesAnObject(q.Ref):
			excluded[stratum]["excluded by rule: it names an object, which cannot move"]++
		default:
			rows = append(rows, q)
			cands = append(cands, candidate{key: referenceSubject(q), stratum: stratum, changed: q.Changed})
		}
	}
	claimed, gone := openAmong(open, cands)
	d := drawSample(cands, claimed, c.Recent, c.Sample, c.rng())
	var qs []catalog.RefQuestion
	for _, i := range d.order() {
		qs = append(qs, referenceQuestion(rows[i]))
	}
	got, err := c.resolve(ctx, qs)
	if err != nil {
		return nil, err
	}

	compare := func(name string, idx []int, population int) store.CheckPart {
		p := store.CheckPart{Part: name, Population: &population}
		unread := map[string]int{}
		for _, i := range idx {
			q, a := rows[i], got[referenceQuestion(rows[i])]
			switch {
			case !a.read():
				unread[a.unread()]++
			case strings.EqualFold(a.Commit, q.Commit):
				p.Compared++
				p.Agreed = append(p.Agreed, store.CheckSubject{Subject: cands[i].key, Reading: readingReference})
			default:
				p.Compared++
				p.Disagreements = append(p.Disagreements, referenceDisagreement(q, a))
			}
		}
		skip(&p, unread)
		return p
	}
	reread := compare("3a open disagreements, read again", d.reread, len(open))
	withdrawn(&reread, gone, "settled: nothing this check compares claims it any more")
	parts := []store.CheckPart{reread, compare("3b references that changed most recently", d.recent, len(cands))}
	for _, stratum := range []struct{ key, name string }{
		{stratumOwned, "3c owned references, sampled at random"},
		{stratumExternal, "3d external references, sampled at random"},
	} {
		p := compare(stratum.name, d.random[stratum.key], d.sizes[stratum.key])
		bounded(&p)
		skip(&p, excluded[stratum.key])
		parts = append(parts, p)
	}
	return parts, nil
}

// referenceSubject keys a queue row as its reference is written, the row alone where it names none.
func referenceSubject(q store.QueueClaim) string {
	if q.Ref == "" {
		return q.NodeKey
	}
	return q.NodeKey + "@" + q.Ref
}

// namesAnObject is a full object id, in either case, as the recheck sweep reads one.
func namesAnObject(ref string) bool {
	return len(ref) == 40 && strings.Trim(ref, "0123456789abcdefABCDEF") == ""
}

func referenceQuestion(q store.QueueClaim) catalog.RefQuestion {
	return catalog.RefQuestion{Owner: q.Owner, Repo: q.Repo, Ref: q.Ref}
}

func referenceDisagreement(q store.QueueClaim, a answer) store.Disagreement {
	now := "it points at " + strings.ToLower(a.Commit) + " now"
	switch {
	case a.Commit != "":
	case q.Ref == "":
		now = "it has no default branch now"
	default:
		now = "GitHub holds no branch or tag named " + q.Ref + " now"
	}
	return store.Disagreement{Subject: referenceSubject(q), Reading: readingReference, Class: classOf(q.Status),
		Claim: "ABOM stands at " + q.Commit + ", " + held(q), Independent: now}
}
