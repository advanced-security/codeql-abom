package checks

import (
	"context"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// fakeGitHub answers as GitHub would now: where each reference points, keyed owner/repo@ref, and
// which branches each repository protects. It records every question it is asked.
type fakeGitHub struct {
	refs      map[string]catalog.RefAnswer
	protected map[string][]string
	cost      int
	queries   [][]catalog.RefQuestion
	listed    []string
	// trees and files are commits' contents by owner/repo@commit; read and fileQueries what was asked.
	trees       map[string]catalog.Tree
	files       map[string]map[string]string
	read        []string
	fileQueries int
}

func (f *fakeGitHub) ResolveRefs(_ context.Context, qs []catalog.RefQuestion) (catalog.RefBatch, error) {
	f.queries = append(f.queries, qs)
	b := catalog.RefBatch{Answers: make([]catalog.RefAnswer, len(qs)), Requests: 1, Cost: f.cost,
		Priced: f.cost > 0}
	for i, q := range qs {
		a, ok := f.refs[q.Owner+"/"+q.Repo+"@"+q.Ref]
		if !ok {
			a = catalog.RefAnswer{Unread: "GitHub answered nothing for " + q.Owner + "/" + q.Repo}
		}
		b.Answers[i] = a
	}
	return b, nil
}

func (f *fakeGitHub) ProtectedBranches(_ context.Context, repository string) ([]string, error) {
	f.listed = append(f.listed, repository)
	branches, ok := f.protected[repository]
	if !ok {
		return nil, fmt.Errorf("GET /repos/%s/branches returned 404", repository)
	}
	return branches, nil
}

func (f *fakeGitHub) asked() []string {
	var out []string
	for _, q := range f.queries {
		for _, r := range q {
			out = append(out, r.Owner+"/"+r.Repo+"@"+r.Ref)
		}
	}
	return out
}

func orNil(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// enqueue puts a queue row where a crawl would have left it, resolved to commit at analyzedAt.
func enqueue(t *testing.T, s *store.Store, key, ref string, depth int, status, commit, analyzedAt string) {
	t.Helper()
	owner, rest, _ := strings.Cut(key, "/")
	repo, _, _ := strings.Cut(rest, "/")
	exec(t, s, `INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
		resolved_sha, analyzed_at, checked_at) VALUES (?, ?, ?, ?, ?, ?, '2026-10-01T00:00:00Z', ?, ?, ?)
		ON CONFLICT (node_key, ref) DO UPDATE SET depth = excluded.depth, status = excluded.status,
		  resolved_sha = excluded.resolved_sha, analyzed_at = excluded.analyzed_at,
		  checked_at = excluded.checked_at`,
		key, ref, owner, repo, depth, status, orNil(commit), orNil(analyzedAt), orNil(analyzedAt))
}

func checker(t *testing.T, s *store.Store, gh GitHub) *Checker {
	t.Helper()
	c, _ := checkerWithPools(t, s, gh)
	return c
}

func checkerWithPools(t *testing.T, s *store.Store, gh GitHub) (*Checker, *work.TokenPools) {
	t.Helper()
	pools := work.NewTokenPools()
	c := &Checker{Store: s, Build: "test-build", Dir: t.TempDir(), Recent: 0, Sample: 300,
		Capacity: pools.Pool(work.PoolConsistency, work.UnitWorkerSeconds),
		GraphQL:  pools.Pool(work.PoolConsistency, work.UnitGraphQL),
		Rand:     rand.New(rand.NewPCG(1, 2))}
	if gh != nil {
		c.GitHub = gh
	}
	return c, pools
}

func runChecker(t *testing.T, c *Checker) []store.CheckRun {
	t.Helper()
	runs, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return runs
}

func bySubject(ds []store.Disagreement) map[string]store.Disagreement {
	out := map[string]store.Disagreement{}
	for _, d := range ds {
		out[d.Subject] = d
	}
	return out
}

func skipped(p store.CheckPart) map[string]int {
	out := map[string]int{}
	for _, k := range p.Skipped {
		out[k.Reason] += k.Count
	}
	return out
}

// referencesFixture is fixture's queue, where vendor/act@v1 stands at c1, with references in every
// state check 3 has to tell apart.
func referencesFixture(t *testing.T) (*store.Store, *fakeGitHub) {
	t.Helper()
	s := fixture(t)
	enqueue(t, s, "actions/cache", "v4", 2, "pending", "e1", "2026-10-02T00:00:00Z")
	enqueue(t, s, "vendor/pinned", sha('f'), 1, "done", sha('f'), "2026-10-02T00:00:00Z")
	// Pinned to an annotated tag's own object, which git peels to the commit beneath it.
	enqueue(t, s, "vendor/tagpin", sha('a'), 1, "done", "b1", "2026-10-02T00:00:00Z")
	enqueue(t, s, "vendor/gone", "v2", 1, "done", "d1", "2026-10-02T00:00:00Z")
	enqueue(t, s, "vendor/private", "v1", 1, "done", "p1", "2026-10-02T00:00:00Z")
	enqueue(t, s, "vendor/fresh", "v3", 1, "pending", "", "")
	enqueue(t, s, "octo/lib", "v1", 1, "done", "l1", "2026-10-02T00:00:00Z")
	return s, &fakeGitHub{cost: 1, refs: map[string]catalog.RefAnswer{
		"vendor/act@v1":    {Commit: "c9"},
		"actions/cache@v4": {Commit: "e2"},
		"vendor/gone@v2":   {Unread: "vendor/gone has no branch or tag named v2", Missing: true},
		"octo/lib@v1":      {Commit: "l1"},
		"octo/app@":        {Commit: "a1"},
	}, protected: map[string][]string{"octo/app": {}}}
}

func sha(c byte) string { return strings.Repeat(string(c), 40) }

// Each reference the queue resolved is compared with where it points now: a move nothing noticed and
// one already queued are both disagreements, told apart by rule, and a reference GitHub could not
// read is unknown rather than agreement.
func TestCheck3ComparesEachResolvedReferenceWithWhereItPointsNow(t *testing.T) {
	s, gh := referencesFixture(t)
	runs := runChecker(t, checker(t, s, gh))

	external := partOf(t, runs, 3, "3d")
	if external.Population == nil || *external.Population != 4 || external.Compared != 3 {
		t.Errorf("the external stratum compared %d of %v, want 3 of the 4 references with a commit "+
			"that can move", external.Compared, external.Population)
	}
	got := bySubject(external.Disagreements)
	for subject, class := range map[string]string{"vendor/act@v1": "unnoticed",
		"actions/cache@v4": "staleness", "vendor/gone@v2": "unnoticed"} {
		d, ok := got[subject]
		if !ok {
			t.Errorf("no disagreement about %s among %+v", subject, external.Disagreements)
			continue
		}
		if d.Class != class {
			t.Errorf("%s is classed %q, want %q", subject, d.Class, class)
		}
	}
	if d := got["vendor/act@v1"]; !strings.Contains(d.Claim, "c1") || !strings.Contains(d.Independent, "c9") {
		t.Errorf("the disagreement about vendor/act@v1 reads %+v, want both commits", d)
	}
	if d := got["vendor/gone@v2"]; !strings.Contains(d.Independent, "no branch or tag") {
		t.Errorf("a reference GitHub no longer holds reads %+v, want its absence said", d)
	}
	if len(got) != 3 {
		t.Errorf("the external stratum disagrees about %v, want only the three that moved",
			slices.Sorted(maps.Keys(got)))
	}
	reasons := skipped(external)
	for _, want := range []string{"unread", "cannot move", "resolved no commit"} {
		found := false
		for reason := range reasons {
			found = found || strings.Contains(reason, want)
		}
		if !found {
			t.Errorf("the external stratum skipped %v, want a reason saying %q", reasons, want)
		}
	}
	owned := partOf(t, runs, 3, "3c")
	if owned.Population == nil || *owned.Population != 1 || owned.Compared != 1 || len(owned.Disagreements) != 0 {
		t.Errorf("the owned stratum reads %+v, want octo/lib@v1 compared and agreeing", owned)
	}
	for _, q := range gh.asked() {
		if strings.HasPrefix(q, "vendor/pinned@") || strings.HasPrefix(q, "vendor/fresh@") ||
			strings.HasPrefix(q, "vendor/tagpin@") {
			t.Errorf("asked GitHub about %s, which has no claim that can be compared", q)
		}
	}
	if n := reasons["excluded by rule: it names an object, which cannot move"]; n != 2 {
		t.Errorf("%d references naming an object were excluded, want the commit pin and the tag pin", n)
	}
}

// What changed most recently is read first, so a regression shows within a run of the build that
// caused it; the baseline is random, so it states a bound rather than a verdict.
func TestCheck3ReadsWhatChangedMostRecentlyFirstAndBoundsItsRandomBaseline(t *testing.T) {
	s := fixture(t)
	refs := map[string]catalog.RefAnswer{"vendor/act@v1": {Commit: "c1"}, "octo/app@": {Commit: "a1"}}
	for i := range 400 {
		key := fmt.Sprintf("vendor/tool%03d", i)
		enqueue(t, s, key, "v1", 1, "done", "c1", fmt.Sprintf("2026-09-%02dT%02d:00:00Z", 1+i/24%28, i%24))
		refs[key+"@v1"] = catalog.RefAnswer{Commit: "c1"}
	}
	enqueue(t, s, "vendor/newest", "v1", 1, "done", "n1", "2026-10-06T12:00:00Z")
	refs["vendor/newest@v1"] = catalog.RefAnswer{Commit: "n1"}
	gh := &fakeGitHub{cost: 1, refs: refs, protected: map[string][]string{"octo/app": {}}}
	c := checker(t, s, gh)
	c.Recent = 5

	runs := runChecker(t, c)
	recent := partOf(t, runs, 3, "3b")
	if recent.Compared != 5 || recent.Bound != nil {
		t.Errorf("the recent part compared %d with bound %v, want the 5 newest and no bound", recent.Compared,
			recent.Bound)
	}
	if first := gh.queries[0][0]; first.Owner+"/"+first.Repo != "vendor/newest" {
		t.Errorf("the first reference asked about is %+v, want the one that changed most recently", first)
	}
	random := partOf(t, runs, 3, "3d")
	if random.Population == nil || *random.Population != 402 || random.Compared != 300 {
		t.Errorf("the random baseline compared %d of %v, want 300 of 402", random.Compared, random.Population)
	}
	if random.Bound == nil || *random.Bound >= 0.01 || *random.Bound <= 0.009 {
		t.Errorf("zero disagreements in 300 bound the rate at %v, want just under 1%%", random.Bound)
	}
	for _, q := range gh.queries {
		if len(q) > 100 {
			t.Errorf("one query asked about %d references; GitHub answers a hundred at most", len(q))
		}
	}
}

// A sample that did not read a disagreement again says nothing about it, so every run reads the open
// ones first, and only a reading that agrees closes one.
func TestASampledDisagreementIsReadAgainFirstAndClosesOnlyWhenItAgrees(t *testing.T) {
	s, gh := referencesFixture(t)
	runChecker(t, checker(t, s, gh))
	open := func() []string {
		subjects, err := s.OpenSubjects(3)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, o := range subjects {
			out = append(out, o.Subject)
		}
		slices.Sort(out)
		return out
	}
	if got := open(); !slices.Equal(got, []string{"actions/cache@v4", "vendor/act@v1", "vendor/gone@v2"}) {
		t.Fatalf("check 3 left %v open, want the three references that moved", got)
	}

	gh.refs["vendor/act@v1"] = catalog.RefAnswer{Commit: "c1"}
	gh.refs["actions/cache@v4"] = catalog.RefAnswer{Unread: "GitHub answered nothing for actions/cache"}
	gh.queries = nil
	c := checker(t, s, gh)
	c.Sample = 0
	runs := runChecker(t, c)

	reread := partOf(t, runs, 3, "3a")
	if reread.Population == nil || *reread.Population != 3 || reread.Compared != 2 {
		t.Errorf("the open disagreements were read again as %+v, want 2 of the 3 compared", reread)
	}
	if got := open(); !slices.Equal(got, []string{"actions/cache@v4", "vendor/gone@v2"}) {
		t.Errorf("after reading them again %v are open, want the one that agrees closed and the unread "+
			"one still open", got)
	}
}

// liveFixture adds owned repositories whose live commits check 4 compares with their branch heads.
func liveFixture(t *testing.T) (*store.Store, *fakeGitHub) {
	t.Helper()
	s := fixture(t)
	if err := s.SetDefaultBranch("octo/app", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProtectedBranches("octo/app", []string{"main", "release", "legacy"}); err != nil {
		t.Fatal(err)
	}
	enqueue(t, s, "octo/app", "release", 0, "done", "r1", "2026-10-02T00:00:00Z")
	enqueue(t, s, "octo/app", "legacy", 0, "done", "g1", "2026-10-02T00:00:00Z")
	for _, name := range []string{"octo/web", "octo/new"} {
		if _, err := s.UpsertRepository(name, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.SetDefaultBranch(name, "main"); err != nil {
			t.Fatal(err)
		}
	}
	enqueue(t, s, "octo/web", "", 0, "done", "w1", "2026-10-03T00:00:00Z")
	return s, &fakeGitHub{cost: 1, refs: map[string]catalog.RefAnswer{
		"vendor/act@v1":                {Commit: "c1"},
		"octo/app@":                    {Commit: "a2"},
		"octo/app@refs/heads/release":  {Commit: "r1"},
		"octo/app@refs/heads/hotfix":   {Commit: "h1"},
		"octo/web@":                    {Commit: "w1"},
		"octo/new@":                    {Commit: "n1"},
		"octo/web@refs/heads/feature1": {Commit: "x1"},
	}, protected: map[string][]string{
		"octo/app": {"main", "release", "hotfix"},
		"octo/web": {"main", "feature1"},
		"octo/new": {"main"},
	}}
}

// An owned repository is live at its default branch and its protected branches, so a head that moved,
// a branch newly protected and one no longer protected each make its live commits disagree.
func TestCheck4ComparesEachOwnedRepositorysLiveCommitsWithItsBranchHeadsNow(t *testing.T) {
	s, gh := liveFixture(t)
	runs := runChecker(t, checker(t, s, gh))

	random := partOf(t, runs, 4, "4c")
	if random.Population == nil || *random.Population != 3 || random.Compared != 2 {
		t.Errorf("check 4 compared %d of %v owned repositories, want 2 of 3: octo/new stands at no commit",
			random.Compared, random.Population)
	}
	got := bySubject(random.Disagreements)
	if len(got) != 1 {
		t.Fatalf("check 4 disagrees about %+v, want octo/app alone", random.Disagreements)
	}
	app := got["octo/app"]
	for _, want := range []string{"a1", "r1", "legacy"} {
		if !strings.Contains(app.Claim, want) {
			t.Errorf("ABOM's side reads %q, want %s in it", app.Claim, want)
		}
	}
	for _, want := range []string{"a2", "r1", "hotfix"} {
		if !strings.Contains(app.Independent, want) {
			t.Errorf("GitHub's side reads %q, want %s in it", app.Independent, want)
		}
	}
	if strings.Contains(app.Independent, "legacy") || app.Class != "unnoticed" {
		t.Errorf("the disagreement reads %+v, want legacy absent from GitHub's side and the class unnoticed", app)
	}
	reasons := skipped(random)
	var notRead, noCommit bool
	for reason := range reasons {
		notRead = notRead || strings.Contains(reason, "not read which branches")
		noCommit = noCommit || strings.Contains(reason, "no commit of its default branch")
	}
	if !notRead || !noCommit {
		t.Errorf("check 4 skipped %v, want octo/web's protected branches and octo/new named", reasons)
	}
}

// The checker's work is metered, so each check it runs is charged to its own worker-seconds pool, and
// every question it puts to GitHub to its own GraphQL pool.
func TestTheCheckerChargesItsOwnPoolsForEveryCheckAndEveryQuestion(t *testing.T) {
	s, gh := liveFixture(t)
	c, pools := checkerWithPools(t, s, gh)
	runs := runChecker(t, c)

	spent := map[string]work.Spend{}
	for _, sp := range pools.Drain() {
		if sp.Pool != work.PoolConsistency {
			t.Errorf("the checker spent %d %s from %s, which is not its own pool", sp.Charged, sp.Unit, sp.Pool)
		}
		cur := spent[sp.Unit]
		cur.Requests += sp.Requests
		cur.Charged += sp.Charged
		spent[sp.Unit] = cur
	}
	if got := spent[work.UnitWorkerSeconds].Requests; got != len(runs) {
		t.Errorf("the worker-seconds pool was drawn %d times, want once for each of the %d checks run",
			got, len(runs))
	}
	if got := spent[work.UnitGraphQL]; got.Requests != len(gh.queries) || got.Charged != len(gh.queries) {
		t.Errorf("the GraphQL pool was charged %+v, want one request and one point for each of %d queries",
			got, len(gh.queries))
	}
}

// Without GitHub the checks that read it are off, and a run files nothing for them.
func TestTheChecksThatReadGitHubAreOffWithoutIt(t *testing.T) {
	s, _ := liveFixture(t)
	runs := runChecker(t, checker(t, s, nil))
	for _, r := range runs {
		if r.Check == 3 || r.Check == 4 {
			t.Errorf("check %d ran without GitHub: %+v", r.Check, r)
		}
	}
}

// The checker cannot change what it measures, and the checks that read GitHub are no exception.
func TestTheChecksThatReadGitHubWriteNothingOutsideTheCheckersTables(t *testing.T) {
	s, gh := liveFixture(t)
	before := contents(t, s)
	runChecker(t, checker(t, s, gh))
	after := contents(t, s)
	for name, rows := range before {
		if !slices.Equal(rows, after[name]) {
			t.Errorf("%s changed during a run:\nbefore %v\nafter  %v", name, rows, after[name])
		}
	}
	if len(gh.queries) == 0 {
		t.Fatal("nothing was asked of GitHub, so the comparison above is vacuous")
	}
}

// A one-sided Clopper-Pearson bound: zero in 300 is just under 1%, and more disagreement or fewer
// subjects can only raise it.
func TestTheBoundIsWhatTheSampleRulesOutAtNinetyFivePercent(t *testing.T) {
	if got := upperBound(0, 300); got >= 0.01 || got < 0.0099 {
		t.Errorf("zero in 300 bounds the rate at %v, want just under 1%%", got)
	}
	if got := upperBound(1, 100); math.Abs(got-0.0466) > 0.0005 {
		t.Errorf("one in 100 bounds the rate at %v, want about 4.66%%", got)
	}
	if got := upperBound(5, 5); got != 1 {
		t.Errorf("five in five bounds the rate at %v, want 1", got)
	}
	if upperBound(1, 300) <= upperBound(0, 300) || upperBound(0, 100) <= upperBound(0, 300) {
		t.Error("more disagreement, or fewer subjects, has to raise the bound")
	}
}
