package enumerate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// protections answers branch reads per repository and records who was asked, which is the cost
// these tests are about: one request per repository.
type protections struct {
	answers map[string][]string
	fail    map[string]error
	asked   []string
}

func (p *protections) read(_ context.Context, repository string) ([]string, error) {
	p.asked = append(p.asked, repository)
	if err := p.fail[repository]; err != nil {
		return nil, err
	}
	return p.answers[repository], nil
}

// byAccount lists each account from its own entry, so two declared accounts read differently.
func byAccount(listings map[string]catalog.Listing) Read {
	return func(_ context.Context, account string, _ catalog.Marks) (catalog.Listing, error) {
		return listings[account], nil
	}
}

func onMain(names ...string) []catalog.Repository {
	out := make([]catalog.Repository, 0, len(names))
	for _, name := range names {
		out = append(out, catalog.Repository{FullName: name, DefaultBranch: "main"})
	}
	return out
}

// branchRows names the references a repository is queued at as its own work, at depth 0.
func branchRows(t *testing.T, s *store.Store, repository string) []string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT ref FROM abom_frontier
		WHERE node_key = ? AND depth = 0 AND ref <> '' ORDER BY ref`, repository)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			t.Fatal(err)
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func checkedAt(t *testing.T, s *store.Store, repository string) string {
	t.Helper()
	var at string
	if err := s.DB().QueryRow(`SELECT COALESCE(branches_checked_at, '') FROM repository
		WHERE repository_full_name = ?`, repository).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// alreadyProtected is a repository a previous enumeration recorded as protecting main and hotfix.
func alreadyProtected(t *testing.T, s *store.Store, repository string) {
	t.Helper()
	if _, err := s.InsertOwnedRepository(repository); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefaultBranch(repository, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProtectedBranches(repository, []string{"main", "hotfix"}); err != nil {
		t.Fatal(err)
	}
}

// Protected branches are recorded where default branches are, so the live set reads them without
// anybody running a command: the manual one had never been run on the crawled organization.
func TestEnumerationRecordsAndQueuesWhatEachOwnedRepositoryProtects(t *testing.T) {
	s := newStore(t)
	p := &protections{answers: map[string][]string{"has-ghas/one": {"main", "release"}}}
	listings := map[string]catalog.Listing{"has-ghas": {
		Repositories: onMain("has-ghas/one"), Route: catalog.RouteGraphQL, Full: true}}

	report, err := Run(context.Background(), Options{
		Store: s, Read: byAccount(listings), Branches: p.read})
	if err != nil {
		t.Fatal(err)
	}

	branches, asked, err := s.ProtectedBranches("has-ghas/one")
	if err != nil {
		t.Fatal(err)
	}
	if !asked || !slices.Equal(branches, []string{"main", "release"}) {
		t.Errorf("recorded %v (asked=%v), want main and release", branches, asked)
	}
	if got := branchRows(t, s, "has-ghas/one"); !slices.Equal(got, []string{"release"}) {
		t.Errorf("queued at %v, want release beside the default branch and main not twice", got)
	}
	one := report.Accounts[0]
	if one.BranchesRead != 1 || one.ProtectedBranches != 2 || one.BranchesQueued != 1 {
		t.Errorf("report says %d read, %d branches, %d queued; want 1, 2, 1",
			one.BranchesRead, one.ProtectedBranches, one.BranchesQueued)
	}
}

// Not asked is not unprotected. A read that failed says nothing about protection, so the
// repository keeps the answer it had, and its branch rows stay in every walk.
func TestABranchReadThatFailsLeavesWhatWasRecorded(t *testing.T) {
	s := newStore(t)
	alreadyProtected(t, s, "has-ghas/two")
	before := checkedAt(t, s, "has-ghas/two")
	p := &protections{
		answers: map[string][]string{"has-ghas/one": {"main", "release"}},
		fail:    map[string]error{"has-ghas/two": errors.New("403 resource not accessible")},
	}
	listings := map[string]catalog.Listing{"has-ghas": {
		Repositories: onMain("has-ghas/one", "has-ghas/two"), Route: catalog.RouteGraphQL, Full: true}}

	report, err := Run(context.Background(), Options{
		Store: s, Read: byAccount(listings), Branches: p.read})
	if err != nil {
		t.Fatal(err)
	}

	if branches, _, _ := s.ProtectedBranches("has-ghas/two"); !slices.Equal(branches, []string{"hotfix", "main"}) {
		t.Errorf("has-ghas/two records %v after a failed read, want hotfix and main kept", branches)
	}
	if got := checkedAt(t, s, "has-ghas/two"); got != before {
		t.Errorf("the failed read moved the check from %q to %q, dating an answer nobody gave",
			before, got)
	}
	if got := branchRows(t, s, "has-ghas/two"); !slices.Equal(got, []string{"hotfix"}) {
		t.Errorf("has-ghas/two is queued at %v, want hotfix kept", got)
	}
	if got := branchRows(t, s, "has-ghas/one"); !slices.Equal(got, []string{"release"}) {
		t.Errorf("has-ghas/one is queued at %v, want release: one failure must not cost the rest", got)
	}
	one := report.Accounts[0]
	if one.BranchesRead != 1 || one.BranchesUnread != 1 || !strings.Contains(one.BranchesUnreadWhy, "403") {
		t.Errorf("report says %d read, %d unread (%q); want 1 read and the 403 named",
			one.BranchesRead, one.BranchesUnread, one.BranchesUnreadWhy)
	}
}

// A sweep names what moved, not everything. A repository it did not name was not asked, and keeps
// what an earlier read recorded.
func TestARepositoryTheListingDidNotNameIsNotAsked(t *testing.T) {
	s := newStore(t)
	alreadyProtected(t, s, "has-ghas/old")
	p := &protections{answers: map[string][]string{}}
	listings := map[string]catalog.Listing{"has-ghas": {
		Repositories: onMain("has-ghas/new"), Route: catalog.RouteGraphQL,
		Marks: catalog.Marks{HighPushed: time.Now()}}}

	if _, err := Run(context.Background(), Options{
		Store: s, Read: byAccount(listings), Branches: p.read}); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(p.asked, []string{"has-ghas/new"}) {
		t.Errorf("asked about %v, want only the repository the sweep named", p.asked)
	}
	if got := branchRows(t, s, "has-ghas/old"); !slices.Equal(got, []string{"hotfix"}) {
		t.Errorf("has-ghas/old is queued at %v, want hotfix kept", got)
	}
}

// A protected branch is where the operator's workflows run with secrets. A seed nobody declared, or
// an account `gh` answered against, is not the operator's, so it is never asked and gains no row
// that would make it the operator's own work.
func TestOnlyRepositoriesTheDeclarationOwnsAreAsked(t *testing.T) {
	s := newStore(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "has-ghas", Kind: store.AccountOrganization, Enabled: true},
		{Owner: "stranger", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceAccounts(store.Verification{
		Login: "operator", Host: "github.com", VerifiedAt: time.Now().UTC(),
		Scopes: []string{"repo", "read:org"},
		Accounts: []store.Account{
			{Owner: "has-ghas", Kind: store.AccountOrganization, Role: store.RoleAdmin}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertRepository("octo/seed", ""); err != nil {
		t.Fatal(err)
	}
	p := &protections{answers: map[string][]string{
		"has-ghas/one": {"main", "release"}, "stranger/app": {"main", "release"},
		"octo/seed": {"main", "release"}}}
	listings := map[string]catalog.Listing{
		"has-ghas": {Repositories: onMain("has-ghas/one"), Route: catalog.RouteGraphQL, Full: true},
		"stranger": {Repositories: onMain("stranger/app"), Route: catalog.RouteGraphQL, Full: true},
	}

	if _, err := Run(context.Background(), Options{
		Store: s, Read: byAccount(listings), Branches: p.read}); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(p.asked, []string{"has-ghas/one"}) {
		t.Errorf("asked about %v, want only has-ghas/one", p.asked)
	}
	if got := branchRows(t, s, "stranger/app"); len(got) != 0 {
		t.Errorf("stranger/app is queued at %v, want nothing", got)
	}
}
