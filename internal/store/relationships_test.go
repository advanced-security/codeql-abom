package store

import (
	"strings"
	"testing"
)

// A relationship is a row a reader can be shown, and its identity travels with it. Without the key
// a listing is a picture: two `uses:` lines from one workflow to one action read identically, and
// selecting either of them selects neither.
func TestARelationshipListsWithItsIdentityAndItsFacts(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	page, err := s.RelationshipsPage(RelationshipsQuery{Limit: 50})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	if page.Total != 10 {
		t.Fatalf("Total = %d, want the ten relationships the fixture declares", page.Total)
	}
	seen := map[string]bool{}
	for _, r := range page.Items {
		if r.EdgeKey == "" {
			t.Errorf("%s -> %s lists without an identity", r.Source, r.Target)
		}
		if seen[r.EdgeKey] {
			t.Errorf("%q is listed twice", r.EdgeKey)
		}
		seen[r.EdgeKey] = true
		if r.Kind == "" {
			t.Errorf("%s -> %s lists without a kind", r.Source, r.Target)
		}
	}

	pinned := relationshipTo(t, page.Items, "actions/checkout")
	if pinned.RefStyle != EdgeRefCommit {
		t.Errorf("a commit reference lists as %q", pinned.RefStyle)
	}
	if pinned.FindingRoute == nil || *pinned.FindingRoute {
		t.Errorf("findingRoute = %v on a route to nothing found", pinned.FindingRoute)
	}
	if pinned.OwnedDepth == nil || *pinned.OwnedDepth != 1 {
		t.Errorf("ownedDepth = %v on a direct use", pinned.OwnedDepth)
	}
	if pinned.FilePath == "" || pinned.StartLine == 0 {
		t.Errorf("%q lists without the location that declares it", pinned.EdgeKey)
	}
}

// A fact nobody computed lists as absent rather than as false or zero. An unrecognized reference is
// the case that matters: `wip` is neither a commit, a tag nor a branch anyone could confirm, and
// calling it a branch would be inventing the answer.
func TestAnUncomputedFactListsAsAbsent(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	page, err := s.RelationshipsPage(RelationshipsQuery{Limit: 50})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	mystery := relationshipTo(t, page.Items, "mystery/action")
	if mystery.RefStyle != "" {
		t.Errorf("an unrecognized reference lists as %q", mystery.RefStyle)
	}

	// And before the walk has run at all, every route fact is absent rather than false: a corpus
	// nobody has derived facts for must not read as a corpus where nothing was found.
	unwalked, err := edgeKindFixture(t).RelationshipsPage(RelationshipsQuery{Limit: 50})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	for _, r := range unwalked.Items {
		if r.AdvisoryRoute != nil || r.FindingRoute != nil || r.OwnedDepth != nil {
			t.Errorf("%q reports %v/%v/%v before anything walked it",
				r.EdgeKey, r.AdvisoryRoute, r.FindingRoute, r.OwnedDepth)
		}
	}
}

// Ordering is not use. A reader asking which jobs wait for which is asking a different question
// from a reader asking what a workflow depends on, and the listing has to keep them apart — the
// aggregates already do, and a list that blurred them would contradict every total on the site.
func TestARelationshipListingSeparatesOrderingFromUse(t *testing.T) {
	s := edgeKindFixture(t)

	ordering, err := s.RelationshipsPage(RelationshipsQuery{
		EdgeKinds: []string{EdgeKindJobNeedsJob}, Limit: 50})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	if ordering.Total != 1 {
		t.Fatalf("Total = %d, want the one `needs:` the fixture declares", ordering.Total)
	}
	if ordering.Items[0].Kind != EdgeKindJobNeedsJob {
		t.Errorf("a %s edge answers a question about ordering", ordering.Items[0].Kind)
	}

	uses, err := s.RelationshipsPage(RelationshipsQuery{
		EdgeKinds: []string{EdgeKindStepUsesAction}, Limit: 50})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	if uses.Total != 1 || uses.Items[0].Kind != EdgeKindStepUsesAction {
		t.Errorf("filtering to uses returned %d rows: %+v", uses.Total, uses.Items)
	}
}

// Containment is drawn and never declared, so it has no location, no reference and no identity.
// Listing it would put a row on the grid that no `uses:` line produced, and every total counting
// relationships would then disagree with the grid that claims to show them.
func TestContainmentIsNotListedAsARelationship(t *testing.T) {
	s := edgeKindFixture(t)

	page, err := s.RelationshipsPage(RelationshipsQuery{Limit: 50})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	for _, r := range page.Items {
		if strings.HasSuffix(r.Kind, "-contains") {
			t.Errorf("%s -> %s lists as a relationship, and nothing declares it",
				r.Source, r.Target)
		}
	}
	if page.Total != 2 {
		t.Errorf("Total = %d, want the two declared relationships", page.Total)
	}
}

// The total is the size of the match, not of the page, or a grid reports the number of rows it
// happens to be showing as the number that exist. Two pages of a stable order repeat nothing and
// skip nothing.
func TestAPageOfRelationshipsIsCutFromTheWholeMatch(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	first, err := s.RelationshipsPage(RelationshipsQuery{Limit: 4})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	if first.Total != 10 || len(first.Items) != 4 {
		t.Fatalf("page of %d with Total %d, want 4 of 10", len(first.Items), first.Total)
	}
	second, err := s.RelationshipsPage(RelationshipsQuery{Limit: 4, Offset: 4})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	if second.Total != 10 {
		t.Errorf("Total = %d on the second page, want the same match", second.Total)
	}
	seen := map[string]bool{}
	for _, r := range append(append([]RelationshipSummary{}, first.Items...), second.Items...) {
		if seen[r.EdgeKey] {
			t.Errorf("%q appears on both pages", r.EdgeKey)
		}
		seen[r.EdgeKey] = true
	}
	if len(seen) != 8 {
		t.Errorf("two pages of four showed %d relationships", len(seen))
	}
}

// Narrowing means narrowing. A term this listing cannot order by is refused by name rather than
// ignored: a grid that silently kept its own order under a column header a reader clicked is
// reporting a different answer from the one it was asked for.
func TestARelationshipListingRefusesASortItCannotDo(t *testing.T) {
	s := edgeFactsFixture(t)

	_, err := s.RelationshipsPage(RelationshipsQuery{Sort: "reach", Limit: 50})
	if err == nil {
		t.Fatal("sorting relationships by a column they do not have was accepted")
	}
	if !strings.Contains(err.Error(), "reach") {
		t.Errorf("the refusal does not name what was asked for: %v", err)
	}
}

// Reference style is a fact about the relationship rather than about either end, which is what
// makes pinning a filter instead of a page of its own.
func TestARelationshipListingNarrowsByReferenceStyle(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	// The term says `sha` and the column says `commit`: the reader's word and the stored word are
	// different words for one thing, and the listing is where they have to meet.
	page, err := s.RelationshipsPage(RelationshipsQuery{RefStyle: RefStyleSHA, Limit: 50})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("Total = %d, want the one commit-pinned reference", page.Total)
	}
	if page.Items[0].Target != "actions/checkout" {
		t.Errorf("the pinned reference is to %q", page.Items[0].Target)
	}
}

// A page is cut by the database, and both ends of a relationship are looked up by key rather than
// found by reading the node table once per edge. On a fixture every shape answers instantly, so
// this asserts the plan: a scan per row is what stops working somewhere between a corpus and an
// enterprise instead of failing outright, and it is measured before any index is considered.
func TestAPageOfRelationshipsIsCutByTheDatabase(t *testing.T) {
	s := edgeFactsFixture(t)
	derive(t, s)

	query, args, err := s.relationshipsPageQuery(RelationshipsQuery{Limit: 50})
	if err != nil {
		t.Fatalf("building the page statement: %v", err)
	}
	if !strings.Contains(query, "LIMIT") {
		t.Errorf("the page is sliced in Go rather than by the database:\n%s", query)
	}
	plan := explain(t, s, query, args...)
	if strings.Contains(plan, "SCAN abom_node") {
		t.Errorf("an endpoint is found by scanning the node table:\n%s", plan)
	}
}

// relationshipTo finds the one relationship pointing at a node.
func relationshipTo(t *testing.T, items []RelationshipSummary, target string) RelationshipSummary {
	t.Helper()
	for _, r := range items {
		if r.Target == target {
			return r
		}
	}
	t.Fatalf("no relationship to %s was listed", target)
	return RelationshipSummary{}
}

// explain reads a query's plan, which is how cost is asserted here: on a fixture every shape is
// fast, so a stopwatch would pass the one that does not survive scale.
func explain(t *testing.T, s *Store, query string, args ...any) string {
	t.Helper()
	rows, err := s.DB().Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explaining %q: %v", query, err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scanning the plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the plan: %v", err)
	}
	return strings.Join(plan, "\n")
}

// repoID reads a seeded repository's row, so a test can attach evidence the way ingestion does.
func repoID(t *testing.T, s *Store, fullName string) int64 {
	t.Helper()
	var id int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = ?`, fullName).Scan(&id); err != nil {
		t.Fatalf("reading the repository row for %s: %v", fullName, err)
	}
	return id
}

// Every `actions/unpinned-tag` finding sits on a `uses:` line, and a `uses:` line is a step. No
// unit lists steps, so until the relationship carried them these findings were reachable only
// through a node that no grid shows.
func TestARelationshipCarriesTheFindingsOnTheLineThatDeclaresIt(t *testing.T) {
	s := edgeFactsFixture(t)
	findingOn(t, s, repoID(t, s, "octo-org/app"), "step:octo-org/app:ci.yml::1")

	page, err := s.RelationshipsPage(RelationshipsQuery{Limit: 50})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	flagged := relationshipTo(t, page.Items, "actions/setup-node")
	if flagged.Findings != 1 {
		t.Errorf("Findings = %d on the line a finding sits on, want 1", flagged.Findings)
	}
	if len(flagged.Rules) != 1 || flagged.Rules[0] != "actions/unpinned-tag" {
		t.Errorf("Rules = %v, want the rule recorded against the line", flagged.Rules)
	}

	clean := relationshipTo(t, page.Items, "actions/checkout")
	if clean.Findings != 0 || len(clean.Rules) != 0 {
		t.Errorf("a line carrying nothing reports %d findings %v", clean.Findings, clean.Rules)
	}
}

// The payoff of carrying them: "where are my unpinned tags" asked as what it actually is, a
// question about relationships, rather than answered by a grid of nodes that holds none of them.
func TestNarrowingRelationshipsByRuleReadsTheDeclaringLine(t *testing.T) {
	s := edgeFactsFixture(t)
	findingOn(t, s, repoID(t, s, "octo-org/app"), "step:octo-org/app:ci.yml::1")

	page, err := s.RelationshipsPage(RelationshipsQuery{
		Rules: []string{"actions/unpinned-tag"}, Limit: 50,
	})
	if err != nil {
		t.Fatalf("RelationshipsPage: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("Total = %d narrowing to one rule, want the single line it fired on", page.Total)
	}
	if page.Items[0].Target != "actions/setup-node" {
		t.Errorf("narrowing by rule returned %s", page.Items[0].Target)
	}
}
