package store

import (
	"encoding/json"
	"testing"
)

// The closure is what makes this a bill of materials rather than a neighborhood: everything the
// root reaches, however deep, and nothing it does not.
func TestTheDocumentContainsWhatTheRootReachesAtAnyDepth(t *testing.T) {
	doc := build(t, bomFixture(t), BOMOptions{})

	want := []string{"actions/checkout", "octo-org/shared", "actions/setup-go"}
	for _, key := range want {
		if componentOf(doc, key) == nil {
			t.Errorf("%s is reachable from the owned scope and is not in the document", key)
		}
	}
	if c := componentOf(doc, "stranger/thing"); c != nil {
		t.Error("stranger/thing is nobody's dependency here and must not be in the document")
	}
}

// Direct and indirect are relative to this document's root, which is why they cannot be stored on
// the edge. Two documents over one graph disagree, correctly, because they start in different
// places.
func TestDirectAndIndirectAreRelativeToTheRoot(t *testing.T) {
	s := bomFixture(t)

	whole := build(t, s, BOMOptions{})
	if c := componentOf(whole, "actions/checkout"); c == nil || !c.Direct {
		t.Error("actions/checkout is named by the root's own workflow, so it is direct")
	}
	if c := componentOf(whole, "actions/setup-go"); c == nil || c.Direct {
		t.Error("actions/setup-go is reached through actions/checkout, so it is indirect")
	}

	// Rooted at the intermediary instead, the same component is now direct.
	inner := build(t, s, BOMOptions{Roots: []string{"actions/checkout"}})
	if c := componentOf(inner, "actions/setup-go"); c == nil || !c.Direct {
		t.Error("rooted at actions/checkout, setup-go is named directly and must say so")
	}
}

// The reference as written is evidence about what the organization runs. Normalizing it away
// discards the most useful thing the document has.
func TestAComponentCarriesTheReferenceVerbatim(t *testing.T) {
	doc := build(t, bomFixture(t), BOMOptions{})

	c := componentOf(doc, "actions/checkout")
	if c == nil {
		t.Fatal("actions/checkout is missing")
	}
	if len(c.References) != 1 {
		t.Fatalf("References = %d, want 1", len(c.References))
	}
	r := c.References[0]
	if r.Raw != "actions/checkout@v4" {
		t.Errorf("Raw = %q, want the reference exactly as it appears in the workflow", r.Raw)
	}
	if r.Value != "v4" {
		t.Errorf("Value = %q, want v4", r.Value)
	}
	if r.ResolvedSHA != "cafe1234" {
		t.Errorf("ResolvedSHA = %q, want the commit the reference resolved to", r.ResolvedSHA)
	}
}

// A component ABOM has not analyzed is *in* the document, marked. Leaving it out would make the
// document look complete; including it silently would make an unexamined component read as a
// clean one. Both are an absent measurement drawn as a measurement.
func TestAnUnanalyzedComponentIsPresentAndMarked(t *testing.T) {
	doc := build(t, bomFixture(t), BOMOptions{})

	c := componentOf(doc, "actions/setup-go")
	if c == nil {
		t.Fatal("an unanalyzed component was omitted, which makes the document look complete")
	}
	if c.Analyzed {
		t.Error("Analyzed = true for a component whose only reference is still queued")
	}
	if c.RefsInUse != 1 || c.RefsAnalyzed != 0 {
		t.Errorf("coverage = %d/%d, want 0 of 1", c.RefsAnalyzed, c.RefsInUse)
	}
}

// Zero of zero is not full coverage. A component nothing ever queued is a different state from
// one that was queued and examined, and the document must not collapse them.
func TestAComponentNothingQueuedIsNotReportedAsCovered(t *testing.T) {
	doc := build(t, bomFixture(t), BOMOptions{})

	if doc.Coverage.ComponentsInUse == 0 {
		t.Fatal("the document reports no components at all")
	}
	if doc.Coverage.ComponentsAnalyzed > doc.Coverage.ComponentsInUse {
		t.Error("more components analyzed than exist")
	}
	if doc.Coverage.ComponentsAnalyzed == doc.Coverage.ComponentsInUse {
		t.Error("this corpus has an unanalyzed component, so coverage cannot be complete")
	}
}

// The coverage statement is inside the document, not beside it. A reader with only the file has
// to be able to tell what was examined to produce it.
func TestCoverageTravelsInsideTheDocument(t *testing.T) {
	doc := build(t, bomFixture(t), BOMOptions{})

	if doc.Coverage.ReferencesInUse != 3 {
		t.Errorf("ReferencesInUse = %d, want the three references the closure uses",
			doc.Coverage.ReferencesInUse)
	}
	if doc.Coverage.ReferencesAnalyzed != 2 {
		t.Errorf("ReferencesAnalyzed = %d, want 2", doc.Coverage.ReferencesAnalyzed)
	}
	if len(doc.Roots) == 0 {
		t.Error("a document that does not name its own roots cannot be checked by its reader")
	}
	if doc.GeneratedAt == "" {
		t.Error("a document with no generation time cannot be compared with a later one")
	}
}

// Claiming provenance that is not held is the same failure as claiming coverage that was not
// measured.
//
// A finding belongs to the component that *holds* the reference, not to the one referenced:
// `LinkFindingsToNodes` attributes by source location, so an unpinned `uses:` is a fact about the
// workflow that wrote it.
func TestProvenanceIsStatedPerFindingAndAbsenceIsNamed(t *testing.T) {
	doc := build(t, bomFixture(t), BOMOptions{})

	c := componentOf(doc, "octo-org/app/.github/workflows/ci.yml")
	if c == nil || len(c.Findings) == 0 {
		t.Fatal("the workflow with the unpinned reference has no finding against it")
	}
	f := c.Findings[0]
	if f.RunKey == "" {
		t.Error("a finding from a recorded run must name it")
	}
	if f.PackDigest != "sha256:pack" {
		t.Errorf("PackDigest = %q, want the digest the run recorded", f.PackDigest)
	}
	if f.ResultHash != "sha256:result" {
		t.Errorf("ResultHash = %q, want the finding's content hash", f.ResultHash)
	}
}

// Analyzed-but-untraceable is distinct from unanalyzed and must stay visible.
func TestAFindingWithNoRunIsReportedAsUntracedRatherThanDropped(t *testing.T) {
	doc := build(t, bomFixture(t), BOMOptions{})

	c := componentOf(doc, "actions/checkout")
	if c == nil {
		t.Fatal("the intermediary's composite action is inside the closure and must be a component")
	}
	var untraced *BOMFinding
	for i := range c.Findings {
		if c.Findings[i].RunKey == "" {
			untraced = &c.Findings[i]
		}
	}
	if untraced == nil {
		t.Fatal("the finding with no query run was dropped rather than reported as untraced")
	}
	if untraced.RuleID != "actions/unpinned-tag" {
		t.Errorf("RuleID = %q, want the rule to survive even when the run did not",
			untraced.RuleID)
	}
	if doc.Coverage.FindingsUntraced != 1 {
		t.Errorf("FindingsUntraced = %d, want 1 stated in the document itself",
			doc.Coverage.FindingsUntraced)
	}
}

// A CodeQL finding says which provider produced it and that the provider can be reproduced, so a
// later one that cannot is distinguishable rather than merely thinner.
func TestACodeQLFindingNamesItsProviderAndSaysItIsReproducible(t *testing.T) {
	doc := build(t, bomFixture(t), BOMOptions{})

	c := componentOf(doc, "octo-org/app/.github/workflows/ci.yml")
	if c == nil || len(c.Findings) == 0 {
		t.Fatal("the workflow with the unpinned reference has no finding against it")
	}
	f := c.Findings[0]
	if f.Provider != ProviderCodeQL {
		t.Errorf("Provider = %q, want %q", f.Provider, ProviderCodeQL)
	}
	if f.Reproducible == nil || !*f.Reproducible {
		t.Error("a CodeQL finding is reproducible from its pack digest and CLI version")
	}
}

// Not-reproducible and not-recorded are different claims.
func TestAFindingNobodyAttributedSaysSoRatherThanClaimingItCannotBeReproduced(t *testing.T) {
	doc := build(t, bomFixture(t), BOMOptions{})

	c := componentOf(doc, "actions/checkout")
	if c == nil || len(c.Findings) == 0 {
		t.Fatal("the unattributed finding is missing")
	}
	f := c.Findings[0]
	if f.Provider != "" {
		t.Errorf("Provider = %q, want it unset: nothing recorded a producer", f.Provider)
	}
	if f.Reproducible != nil {
		t.Errorf("Reproducible = %v, want no answer to a question nobody asked", *f.Reproducible)
	}
	if doc.Coverage.FindingsWithoutProvider != 1 {
		t.Errorf("FindingsWithoutProvider = %d, want 1 stated in the document itself",
			doc.Coverage.FindingsWithoutProvider)
	}
}

// A finding against somebody else's repository is not evidence about this document's scope.
func TestFindingsFromOutsideTheClosureDoNotAppear(t *testing.T) {
	doc := build(t, bomFixture(t), BOMOptions{})

	for _, c := range doc.Components {
		for _, f := range c.Findings {
			if f.Repository == "stranger/repo" {
				t.Errorf("%s carries a finding from outside the closure", c.Key)
			}
		}
	}
}

// The boundary each relationship crosses comes from the stored classification, so the document
// and the Explorer cannot disagree about what is a third-party dependency.
func TestRelationshipsCarryTheBoundaryTheyCross(t *testing.T) {
	doc := build(t, bomFixture(t), BOMOptions{})

	want := map[string]string{
		"actions/checkout": BoundaryCrossOwner,
		"octo-org/shared":  BoundarySameOwner,
	}
	seen := map[string]bool{}
	for _, r := range doc.Relationships {
		if w, ok := want[r.Target]; ok {
			seen[r.Target] = true
			if r.Boundary != w {
				t.Errorf("relationship to %s has boundary %q, want %q", r.Target, r.Boundary, w)
			}
		}
	}
	for target := range want {
		if !seen[target] {
			t.Errorf("no relationship to %s reached the document", target)
		}
	}
}

// "Verifiable" means a reader can regenerate it and compare. Map iteration order alone would
// break that, and it is the same test of determinism the ledger already passes for findings.
//
// The generation time is excluded, because it is the one field that is *supposed* to differ.
func TestTheSameRootProducesTheSameDocumentTwice(t *testing.T) {
	s := bomFixture(t)

	first := build(t, s, BOMOptions{})
	second := build(t, s, BOMOptions{})
	first.GeneratedAt, second.GeneratedAt = "", ""
	if mustJSON(t, first) != mustJSON(t, second) {
		t.Error("two reads of one corpus produced different documents")
	}
}

// An undeclared scope is not an empty one. Emitting a document with no components would report
// that the organization depends on nothing, which is a fabricated fact.
func TestADocumentForAnUndeclaredScopeRefusesRatherThanReportingNothing(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.BillOfMaterials(BOMOptions{}); err == nil {
		t.Error("a document was produced for a scope nobody declared")
	}
}

func build(t *testing.T, s *Store, opts BOMOptions) BOM {
	t.Helper()
	doc, err := s.BillOfMaterials(opts)
	if err != nil {
		t.Fatalf("BillOfMaterials: %v", err)
	}
	return doc
}

func componentOf(doc BOM, key string) *BOMComponent {
	for i := range doc.Components {
		if doc.Components[i].Key == key {
			return &doc.Components[i]
		}
	}
	return nil
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}
	return string(b)
}
