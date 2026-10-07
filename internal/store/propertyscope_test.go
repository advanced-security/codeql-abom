package store

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// propertyStore is an organization whose schema covers every way a property scope can be decided:
// properties only organization owners and admins can set (one of each value type), one repository
// administrators can set, and one GitHub never said anything about.
func propertyStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.ReplacePropertySchema("acme", []PropertyDefinition{
		{Name: "team", ValueType: "single_select", ValuesEditableBy: PropertyEditableByOrganization},
		{Name: "region", ValueType: "string", ValuesEditableBy: PropertyEditableByOrganization,
			HasDefault: true, DefaultValue: "emea"},
		{Name: "tags", ValueType: "multi_select", ValuesEditableBy: PropertyEditableByOrganization},
		{Name: "business_id", ValueType: "string", ValuesEditableBy: "org_and_repo_actors"},
		{Name: "legacy", ValueType: "string"},
	}); err != nil {
		t.Fatal(err)
	}
	values := map[string][]PropertyValue{
		"acme/alpha":   {{"team", "a"}, {"region", "emea"}},
		"acme/beta":    {{"team", "a"}, {"region", "amer"}},
		"acme/gamma":   {{"team", "b"}, {"region", "emea"}},
		"acme/delta":   {{"tags", "x"}, {"tags", "y"}},
		"acme/epsilon": nil,
		"acme/zeta":    {{"business_id", "7"}, {"legacy", "old"}},
	}
	for repo, vs := range values {
		if _, err := s.UpsertRepository(repo, ""); err != nil {
			t.Fatal(err)
		}
		if vs != nil {
			if err := s.ReplacePropertyValues(repo, vs); err != nil {
				t.Fatal(err)
			}
		}
	}
	return s
}

func restricted(includes []string, clauses ...[]PropertyTerm) DeclaredAccount {
	a := DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: true,
		IncludesOnly: true, Includes: includes}
	for _, terms := range clauses {
		a.Properties = append(a.Properties, PropertyClause{Terms: terms})
	}
	return a
}

func ownedNames(t *testing.T, s *Store) string {
	t.Helper()
	got, err := s.OwnedRepositories()
	if err != nil {
		t.Fatalf("OwnedRepositories: %v", err)
	}
	sort.Strings(got)
	return strings.Join(got, ",")
}

// The terms of one scope all hold: a repository carrying only some of them is not covered.
func TestAPropertyScopeOwnsTheRepositoriesCarryingEveryTerm(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted(nil, []PropertyTerm{{"team", "a"}, {"region", "emea"}}))

	if got := ownedNames(t, s); got != "acme/alpha" {
		t.Errorf("owned %q, want only acme/alpha", got)
	}
}

// Two scopes on one account each count, which is how an operator says "this or that".
func TestTwoPropertyScopesOnOneAccountEachCount(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted(nil, []PropertyTerm{{"team", "a"}}, []PropertyTerm{{"region", "emea"}}))

	if got := ownedNames(t, s); got != "acme/alpha,acme/beta,acme/gamma" {
		t.Errorf("owned %q, want alpha, beta and gamma", got)
	}
}

// A scope adds to the repositories named outright; neither narrows the other.
func TestAPropertyScopeAddsToTheNamedRepositories(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted([]string{"epsilon"}, []PropertyTerm{{"team", "b"}}))

	if got := ownedNames(t, s); got != "acme/epsilon,acme/gamma" {
		t.Errorf("owned %q, want epsilon and gamma", got)
	}
}

// A key repeated in one scope means the repository carries each value, which only a multi-select
// property can; the store refuses the repetition where the schema says it can never match.
func TestARepeatedKeyMatchesOnlyAMultiSelectCarryingEachValue(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted(nil, []PropertyTerm{{"tags", "x"}, {"tags", "y"}}))
	if got := ownedNames(t, s); got != "acme/delta" {
		t.Errorf("owned %q, want acme/delta", got)
	}

	err := s.SetOwnedScope([]DeclaredAccount{restricted(nil,
		[]PropertyTerm{{"team", "a"}, {"team", "b"}})}, DeclaredByCLI)
	if err == nil || !strings.Contains(err.Error(), "team") {
		t.Errorf("a single-select key repeated was accepted: %v", err)
	}
}

// A value a repository's own administrator can set would let them move it into or out of the
// operator's responsibility, so the store refuses it outright when the schema says so.
func TestAPropertyRepositoryAdministratorsCanEditIsRefused(t *testing.T) {
	s := propertyStore(t)
	err := s.SetOwnedScope([]DeclaredAccount{restricted(nil, []PropertyTerm{{"business_id", "7"}})},
		DeclaredByCLI)
	if err == nil || !strings.Contains(err.Error(), "repository administrators") {
		t.Errorf("a repository-editable property was accepted: %v", err)
	}
}

// What GitHub has not described cannot be judged: the scope is kept, counts nothing, and says why.
func TestAPropertyScopeNobodyCouldJudgeIsHeldNotRefused(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted(nil, []PropertyTerm{{"legacy", "old"}}, []PropertyTerm{{"unheard", "x"}}))

	if got := ownedNames(t, s); got != "" {
		t.Errorf("owned %q through scopes nothing could judge", got)
	}
	clauses := readClauses(t, s)
	if len(clauses) != 2 {
		t.Fatalf("read back %d scopes, want both kept", len(clauses))
	}
	for _, c := range clauses {
		if !c.Held || c.HeldReason == "" {
			t.Errorf("%v is not held, or says nothing about why: %+v", c.Terms, c)
		}
	}
}

// A property the organization later opens to repository administrators stops deciding anything,
// without anyone editing the declaration.
func TestAPropertyThatBecomesRepositoryEditableHoldsItsScope(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted(nil, []PropertyTerm{{"team", "a"}}))
	if got := ownedNames(t, s); got != "acme/alpha,acme/beta" {
		t.Fatalf("owned %q before the change, want alpha and beta", got)
	}

	if err := s.ReplacePropertySchema("acme", []PropertyDefinition{
		{Name: "team", ValueType: "single_select", ValuesEditableBy: "org_and_repo_actors"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := ownedNames(t, s); got != "" {
		t.Errorf("owned %q after team became repository-editable", got)
	}
	clauses := readClauses(t, s)
	if len(clauses) != 1 || !clauses[0].Held || !strings.Contains(clauses[0].HeldReason, "repository administrators") {
		t.Errorf("the scope reads back as %+v, want it held for being repository-editable", clauses)
	}
}

func TestAPropertyScopeIsRefusedWhereItCannotMeanAnything(t *testing.T) {
	s := propertyStore(t)
	cases := map[string]DeclaredAccount{
		"on a user account": {Owner: "someone", Kind: AccountUser, Enabled: true, IncludesOnly: true,
			Properties: []PropertyClause{{Terms: []PropertyTerm{{"team", "a"}}}}},
		"on a whole account": {Owner: "acme", Kind: AccountOrganization, Enabled: true,
			Properties: []PropertyClause{{Terms: []PropertyTerm{{"team", "a"}}}}},
		"with no terms": restricted(nil, []PropertyTerm{}),
		"with no value": restricted(nil, []PropertyTerm{{"team", ""}}),
		"with no name":  restricted(nil, []PropertyTerm{{"", "a"}}),
	}
	for name, a := range cases {
		if err := s.SetOwnedScope([]DeclaredAccount{a}, DeclaredByCLI); err == nil {
			t.Errorf("a property scope %s was accepted", name)
		}
	}
}

// The declaration is read back whole, so an editor that saves what it read cannot drop a scope.
func TestPropertyScopesAreReadBackWithTheirProvenance(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted([]string{"epsilon"}, []PropertyTerm{{"team", "a"}, {"region", "emea"}}))

	clauses := readClauses(t, s)
	if len(clauses) != 1 {
		t.Fatalf("read back %d scopes, want 1", len(clauses))
	}
	c := clauses[0]
	if len(c.Terms) != 2 || c.DeclaredBy != DeclaredByCLI || c.DeclaredAt == "" || c.Held {
		t.Errorf("read back %+v", c)
	}
	declared, err := s.OwnedScope()
	if err != nil || len(declared) != 1 || len(declared[0].Includes) != 1 {
		t.Errorf("the account's own includes did not survive: %+v, %v", declared, err)
	}
}

// Every spelling of the ownership predicate answers the same: the queue, the per-repository check
// and the tier read the frontier row's own columns, not the repository table.
func TestEverySpellingOfOwnershipHonorsAPropertyScope(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted(nil, []PropertyTerm{{"team", "a"}, {"region", "emea"}}))
	for _, repo := range []string{"alpha", "beta"} {
		if _, err := s.DB().Exec(`
			INSERT INTO abom_frontier (node_key, owner, repo, depth, status, score, enqueued_at,
				analysis_count, resolved_sha)
			VALUES (?, 'acme', ?, 2, 'done', 1, '2026-01-01T00:00:00Z', 1, 'sha')`,
			"acme/"+repo, repo); err != nil {
			t.Fatal(err)
		}
	}

	var queued []string
	rows, err := s.DB().Query(`SELECT node_key FROM abom_frontier WHERE ` + ownedFrontierRow)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		queued = append(queued, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if strings.Join(queued, ",") != "acme/alpha" {
		t.Errorf("the queue's spelling owns %v, want acme/alpha", queued)
	}
	for repo, want := range map[string]bool{"acme/alpha": true, "acme/beta": false} {
		if got, err := s.RepositoryIsOwned(repo); err != nil || got != want {
			t.Errorf("RepositoryIsOwned(%s) = %v, %v; want %v", repo, got, err, want)
		}
	}
}

// The Go form decides coverage for a declaration that has not been stored, from values GitHub just
// answered with, and must agree with the stored predicate on every case, including the held ones.
func TestPropertyCoverageAgreesWithTheStoredPredicate(t *testing.T) {
	s := propertyStore(t)
	scope := []DeclaredAccount{restricted([]string{"epsilon"},
		[]PropertyTerm{{"team", "a"}, {"region", "emea"}},
		[]PropertyTerm{{"tags", "x"}, {"tags", "y"}},
		[]PropertyTerm{{"legacy", "old"}})}
	declare(t, s, scope...)
	stored, err := s.OwnedScope()
	if err != nil {
		t.Fatal(err)
	}

	owned := map[string]bool{}
	names, err := s.OwnedRepositories()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		owned[n] = true
	}
	for _, full := range []string{"acme/alpha", "acme/beta", "acme/gamma", "acme/delta",
		"acme/epsilon", "acme/zeta"} {
		owner, repo, _ := splitRepository(full)
		values, err := s.PropertyValues(full)
		if err != nil {
			t.Fatal(err)
		}
		if got := CoveredBy(stored, owner, repo, values); got != owned[full] {
			t.Errorf("%s: CoveredBy = %v, the stored predicate says %v", full, got, owned[full])
		}
	}
}

// Whether a scope is held is decided in SQL for every reader and explained in Go for the screen,
// so the two must agree on which scopes are held.
func TestAHeldScopeIsHeldInSQLExactlyWhenItHasAReason(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted(nil,
		[]PropertyTerm{{"team", "a"}},
		[]PropertyTerm{{"tags", "x"}, {"tags", "y"}},
		[]PropertyTerm{{"legacy", "old"}},
		[]PropertyTerm{{"unheard", "x"}},
		[]PropertyTerm{{"team", "a"}, {"region", "emea"}}))

	for _, c := range readClauses(t, s) {
		if c.Held != (c.HeldReason != "") {
			t.Errorf("%v: held %v with reason %q", c.Terms, c.Held, c.HeldReason)
		}
	}
	if got := readClauses(t, s); got[0].Held || got[1].Held || !got[2].Held || !got[3].Held || got[4].Held {
		t.Errorf("held flags %v, want only legacy and unheard held", got)
	}
}

func readClauses(t *testing.T, s *Store) []PropertyClause {
	t.Helper()
	declared, err := s.OwnedScope()
	if err != nil {
		t.Fatalf("OwnedScope: %v", err)
	}
	var out []PropertyClause
	for _, a := range declared {
		out = append(out, a.Properties...)
	}
	return out
}

// The predicate's property test reads abom_repository_property, so a bare column passed in binds to
// that table's own column and every repository matches itself. Only a qualified column is safe.
func TestTheOwnershipPredicateIsGivenAQualifiedColumn(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	call := regexp.MustCompile(`(?:owned|declared)FullName\("([^"]*)"\)`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range call.FindAllStringSubmatch(string(src), -1) {
			if !strings.Contains(m[1], ".") {
				t.Errorf("%s passes %q unqualified", f, m[1])
			}
		}
	}
}

// A declaration replaces wholesale, scopes included: a scope left behind by an earlier save would
// keep owning repositories nobody can see declared.
func TestRedeclaringReplacesThePropertyScopes(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted(nil, []PropertyTerm{{"team", "a"}}))
	declare(t, s, restricted(nil, []PropertyTerm{{"team", "b"}}))

	if got := ownedNames(t, s); got != "acme/gamma" {
		t.Errorf("owned %q after redeclaring, want only acme/gamma", got)
	}
	if n := len(readClauses(t, s)); n != 1 {
		t.Errorf("read back %d scopes after redeclaring one, want 1", n)
	}
}

// The property test runs inside every ownership read, so it must look values and terms up by key.
// A scan here costs a pass over every value on record for every repository a read considers.
func TestThePropertyTestSearchesByKeyRatherThanScanning(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted(nil, []PropertyTerm{{"team", "a"}, {"region", "emea"}}))

	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT r.repository_full_name FROM repository r WHERE ` +
		ownedFullName("r.repository_full_name"))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// SQLite flattens the held-scope view, so its clause table is planned under the view's alias c.
	for _, step := range plan {
		for _, table := range []string{"scope_value", "scope_term", "c"} {
			if step == "SCAN "+table || strings.HasPrefix(step, "SCAN "+table+" ") {
				t.Errorf("the property test scans %s: %q\nplan: %v", table, step, plan)
			}
		}
	}
}

// Values kept for a repository nobody owns describe nothing a reader may group or filter by.
func TestAKeyOnlyAnUnownedRepositoryCarriesIsOfferedNowhere(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted([]string{"alpha"}))
	if err := s.ReplacePropertyValues("acme/beta", []PropertyValue{{"secret", "x"}}); err != nil {
		t.Fatal(err)
	}

	keys, err := s.PropertyKeys()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k == "secret" || k == "tags" || k == "business_id" {
			t.Errorf("offered %q, which only repositories nobody owns carry", k)
		}
	}
	counts, err := s.FilterValueCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["properties"] != 2 {
		t.Errorf("counted %d keys, want alpha's team and region", counts["properties"])
	}
}

// A scope naming the value a repository carries for choosing nothing grows by itself, so it says
// so wherever it is shown: a single default and one of a multi-select's alike.
func TestAScopeNamingTheDefaultSaysSo(t *testing.T) {
	s := propertyStore(t)
	if err := s.ReplacePropertySchema("acme", []PropertyDefinition{
		{Name: "team", ValueType: "single_select", ValuesEditableBy: PropertyEditableByOrganization},
		{Name: "region", ValueType: "string", ValuesEditableBy: PropertyEditableByOrganization,
			HasDefault: true, DefaultValue: "emea"},
		{Name: "tags", ValueType: "multi_select", ValuesEditableBy: PropertyEditableByOrganization,
			DefaultValues: []string{"x"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnedScope([]DeclaredAccount{restricted(nil,
		[]PropertyTerm{{"region", "emea"}, {"team", "a"}},
		[]PropertyTerm{{"region", "amer"}},
		[]PropertyTerm{{"tags", "x"}, {"tags", "y"}},
	)}, DeclaredByCLI); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, c := range readClauses(t, s) {
		got[c.Terms[0].Name+"="+c.Terms[0].Value] = strings.Join(c.Defaults, ",")
	}
	want := map[string]string{"region=emea": "region", "region=amer": "", "tags=x": "tags"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("the scope starting %s names the default of %q, want %q", k, got[k], v)
		}
	}
}

// A schema recorded before its read time was kept lacks who may set each property, because the
// reader that wrote it never asked; the hold says it was not read rather than that GitHub never said.
func TestASchemaRecordedWithoutAReadTimeHoldsAsUnread(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO abom_property_schema (owner, property_name, default_value)
		VALUES ('acme', 'team', NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnedScope([]DeclaredAccount{restricted(nil, []PropertyTerm{{"team", "a"}})},
		DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	clauses := readClauses(t, s)
	if len(clauses) != 1 || !clauses[0].Held || !strings.Contains(clauses[0].HeldReason, "not been read") {
		t.Errorf("held %v for %q, want held as not read", clauses[0].Held, clauses[0].HeldReason)
	}
}

// A scope stored before its property became editable by repository administrators is held, not
// deleted, and saving the declaration again keeps it; only a scope that is new is refused, in the
// reader's words rather than the package's.
func TestAHeldScopeIsKeptWhenItsDeclarationIsSavedAgain(t *testing.T) {
	s := propertyStore(t)
	declare(t, s, restricted(nil, []PropertyTerm{{"team", "a"}}))
	if err := s.ReplacePropertySchema("acme", []PropertyDefinition{
		{Name: "team", ValueType: "single_select", ValuesEditableBy: "org_and_repo_actors"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetOwnedScope([]DeclaredAccount{restricted([]string{"acme/zeta"},
		[]PropertyTerm{{"team", "a"}})}, DeclaredByInterface); err != nil {
		t.Fatalf("saving again with the held scope unchanged: %v", err)
	}
	if clauses := readClauses(t, s); len(clauses) != 1 || !clauses[0].Held {
		t.Errorf("clauses %+v, want the one scope kept and held", clauses)
	}

	err := s.SetOwnedScope([]DeclaredAccount{restricted(nil, []PropertyTerm{{"team", "a"}},
		[]PropertyTerm{{"team", "b"}})}, DeclaredByInterface)
	var refused *DeclarationRefused
	if !errors.As(err, &refused) || strings.HasPrefix(refused.Reason, "store") ||
		!strings.Contains(refused.Reason, "repository administrators") {
		t.Errorf("err = %v, want a new scope on team refused with the reason alone", err)
	}
}
