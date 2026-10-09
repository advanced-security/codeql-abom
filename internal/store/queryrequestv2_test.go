package store

import (
	"reflect"
	"strings"
	"testing"
)

const linkedCommit = "0123456789abcdef0123456789abcdef01234567"

func linkedExample() RequestExample {
	return RequestExample{
		Side: ExampleNonCompliant, Text: "runs-on: self-hosted\n",
		URL:        "https://github.com/octo/app/blob/main/.github/workflows/ci.yml#L10-L12",
		Repository: "octo/app", Path: ".github/workflows/ci.yml", Ref: "main", Commit: linkedCommit,
		StartLine: 10, EndLine: 12,
	}
}

func aFullRequest() QueryRequest {
	r := aRequest()
	r.Compliant, r.NonCompliant = "", ""
	r.Title = "Self-hosted runner reached from a pull request"
	r.Slug = "self-hosted-pr-runner"
	r.Recommendation = "Run untrusted pull requests on GitHub-hosted runners."
	r.Examples = []RequestExample{
		{Side: ExampleCompliant, Text: "runs-on: ubuntu-latest"},
		{Side: ExampleNonCompliant, Text: "runs-on: self-hosted"},
		{Side: ExampleCompliant, Text: "runs-on: windows-latest"},
		linkedExample(),
	}
	r.References = []RequestReference{{Value: "cwe-829"}, {Value: "CVE-2025-30066"}}
	sev := 7.5
	r.Metadata = RequestedMetadata{
		Description: "A pull request can run code on a self-hosted runner.",
		Precision:   "high", ProblemSeverity: "error", SecuritySeverity: &sev,
		Tags: []string{"security", "actions"},
	}
	return r
}

func onlyRequest(t *testing.T, s *Store) QueryRequest {
	t.Helper()
	got, err := s.QueryRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d requests, want 1", len(got))
	}
	return got[0]
}

// A query is tested by every example the operator gave, so none may be dropped or reordered, and a
// reader of the old columns still sees one example per side.
func TestARequestKeepsEveryExampleOnEachSideInOrder(t *testing.T) {
	s := newTestStore(t)
	want := aFullRequest()
	if _, err := s.SaveQueryRequest(want); err != nil {
		t.Fatal(err)
	}
	r := onlyRequest(t, s)
	if !reflect.DeepEqual(r.Examples, want.Examples) {
		t.Errorf("examples came back as %+v\nwant %+v", r.Examples, want.Examples)
	}
	if r.Compliant != "runs-on: ubuntu-latest" || r.NonCompliant != "runs-on: self-hosted" {
		t.Errorf("the first example per side is %q / %q", r.Compliant, r.NonCompliant)
	}
	if r.Title != want.Title || r.Slug != want.Slug || r.Recommendation != want.Recommendation {
		t.Errorf("identity and guidance came back as %q %q %q", r.Title, r.Slug, r.Recommendation)
	}
}

// A request with nothing on one side cannot become a test that tells the two apart.
func TestARequestNeedsAnExampleOnEachSide(t *testing.T) {
	s := newTestStore(t)
	for name, keep := range map[string]string{
		"no non-compliant example": ExampleCompliant,
		"no compliant example":     ExampleNonCompliant,
	} {
		r := aFullRequest()
		r.Examples = []RequestExample{{Side: keep, Text: "x"}}
		if _, err := s.SaveQueryRequest(r); err == nil {
			t.Errorf("a request with %s was kept", name)
		}
	}
	r := aFullRequest()
	r.Examples = append(r.Examples, RequestExample{Side: ExampleCompliant, Text: " \n"})
	if _, err := s.SaveQueryRequest(r); err == nil {
		t.Error("a blank example was kept")
	}
	r = aFullRequest()
	r.Examples[0].Side = "maybe"
	if _, err := s.SaveQueryRequest(r); err == nil {
		t.Error("an example on neither side was kept")
	}
}

// A request filed with only the two text fields, as every request before examples was, reads as
// one example per side rather than as none.
func TestARequestWithOnlyTheTwoTextsReadsAsOneExamplePerSide(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.SaveQueryRequest(aRequest()); err != nil {
		t.Fatal(err)
	}
	want := []RequestExample{
		{Side: ExampleCompliant, Text: "runs-on: ubuntu-latest"},
		{Side: ExampleNonCompliant, Text: "runs-on: self-hosted"},
	}
	if r := onlyRequest(t, s); !reflect.DeepEqual(r.Examples, want) {
		t.Errorf("examples read %+v", r.Examples)
	}
}

// A linked example is evidence of a commit: without the commit and the lines read there, a fixture
// built from it changes when the branch moves.
func TestALinkedExampleSaysTheCommitAndLinesItWasReadAt(t *testing.T) {
	s := newTestStore(t)
	for name, change := range map[string]func(*RequestExample){
		"no commit":          func(e *RequestExample) { e.Commit = "" },
		"a branch as commit": func(e *RequestExample) { e.Commit = "main" },
		"no repository":      func(e *RequestExample) { e.Repository = "" },
		"no path":            func(e *RequestExample) { e.Path = "" },
		"lines backwards":    func(e *RequestExample) { e.StartLine, e.EndLine = 12, 10 },
		"no first line":      func(e *RequestExample) { e.StartLine = 0 },
		"no text":            func(e *RequestExample) { e.Text = "" },
	} {
		r := aFullRequest()
		e := linkedExample()
		change(&e)
		r.Examples = append(r.Examples[:3], e)
		if _, err := s.SaveQueryRequest(r); err == nil {
			t.Errorf("a linked example with %s was kept", name)
		}
	}
	r := aFullRequest()
	r.Examples[0].Commit = linkedCommit
	if _, err := s.SaveQueryRequest(r); err == nil {
		t.Error("a pasted example claiming a commit was kept")
	}
}

// The slug is how a request is named from outside, so two requests may not share one and a kept
// request's slug never changes.
func TestARequestsSlugIsUniqueAndNeverChanges(t *testing.T) {
	s := newTestStore(t)
	id, err := s.SaveQueryRequest(aFullRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveQueryRequest(aFullRequest()); err == nil ||
		!strings.Contains(err.Error(), "self-hosted-pr-runner") {
		t.Errorf("a second request with the same slug: %v", err)
	}
	for _, bad := range []string{"Self Hosted", "-lead", "trail-", "a--b", "a/b"} {
		r := aFullRequest()
		r.Slug = bad
		if _, err := s.SaveQueryRequest(r); err == nil {
			t.Errorf("slug %q was kept", bad)
		}
	}
	edit := aFullRequest()
	edit.RowID, edit.Slug = id, "renamed"
	if _, err := s.SaveQueryRequest(edit); err == nil {
		t.Error("an edit renamed the slug")
	}
	edit.Slug, edit.Title = "", "Retitled"
	if _, err := s.SaveQueryRequest(edit); err != nil {
		t.Fatal(err)
	}
	if r := onlyRequest(t, s); r.Slug != "self-hosted-pr-runner" || r.Title != "Retitled" {
		t.Errorf("an edit with no slug left %q titled %q", r.Slug, r.Title)
	}
	// Requests from before slugs share the empty one.
	for range 2 {
		if _, err := s.SaveQueryRequest(aRequest()); err != nil {
			t.Fatalf("a request with no slug: %v", err)
		}
	}
}

// A CVE or CWE reference is what a reader filters findings by, so it becomes the tag CodeQL's
// style guide spells for it.
func TestReferencesAreNormalizedAndImplyTags(t *testing.T) {
	s := newTestStore(t)
	r := aFullRequest()
	r.References = []RequestReference{
		{Value: " cve-2025-30066 "}, {Value: "CWE-20"}, {Value: "cwe-829"},
		{Value: "ghsa-mrrh-fwg8-r2c3"}, {Value: "https://docs.github.com/en/actions"},
		{Value: "CWE-829"},
	}
	if _, err := s.SaveQueryRequest(r); err != nil {
		t.Fatal(err)
	}
	got := onlyRequest(t, s)
	want := []RequestReference{
		{Kind: ReferenceCVE, Value: "CVE-2025-30066"}, {Kind: ReferenceCWE, Value: "CWE-20"},
		{Kind: ReferenceCWE, Value: "CWE-829"}, {Kind: ReferenceGHSA, Value: "GHSA-mrrh-fwg8-r2c3"},
		{Kind: ReferenceURL, Value: "https://docs.github.com/en/actions"},
	}
	if !reflect.DeepEqual(got.References, want) {
		t.Errorf("references read %+v", got.References)
	}
	tags := []string{"external/cve/cve-2025-30066", "external/cwe/cwe-020", "external/cwe/cwe-829"}
	if !reflect.DeepEqual(got.ReferenceTags, tags) {
		t.Errorf("implied tags %v, want %v", got.ReferenceTags, tags)
	}
	for _, bad := range []string{"CVE-25-1", "CWE-x", "GHSA-1", "javascript:alert(1)",
		"http://insecure.example", "https://", "a note"} {
		r := aFullRequest()
		r.Slug = ""
		r.References = []RequestReference{{Value: bad}}
		if _, err := s.SaveQueryRequest(r); err == nil {
			t.Errorf("reference %q was kept", bad)
		}
	}
}

// Requested metadata is written into a query's QLDoc, so it must be a value CodeQL accepts and must
// not be able to close the comment.
func TestRequestedMetadataIsAValueCodeQLAccepts(t *testing.T) {
	s := newTestStore(t)
	high, zero := 11.0, 0.0
	for name, change := range map[string]func(*QueryRequest){
		"an unknown precision":         func(r *QueryRequest) { r.Metadata.Precision = "huge" },
		"an unknown problem severity":  func(r *QueryRequest) { r.Metadata.ProblemSeverity = "fatal" },
		"a security severity above 10": func(r *QueryRequest) { r.Metadata.SecuritySeverity = &high },
		"a security severity without the security tag": func(r *QueryRequest) {
			r.Metadata.Tags = []string{"actions"}
		},
		"a tag with a space":          func(r *QueryRequest) { r.Metadata.Tags = append(r.Metadata.Tags, "two words") },
		"a description closing QLDoc": func(r *QueryRequest) { r.Metadata.Description = "x */ y" },
		"a title closing QLDoc":       func(r *QueryRequest) { r.Title = "x */" },
		"a title on two lines":        func(r *QueryRequest) { r.Title = "x\ny" },
	} {
		r := aFullRequest()
		r.Slug = ""
		change(&r)
		if _, err := s.SaveQueryRequest(r); err == nil {
			t.Errorf("%s was kept", name)
		}
	}

	r := aFullRequest()
	r.Metadata.SecuritySeverity = &zero
	r.Metadata.Tags = []string{"security", "actions", "security"}
	if _, err := s.SaveQueryRequest(r); err != nil {
		t.Fatal(err)
	}
	got := onlyRequest(t, s)
	if got.Metadata.SecuritySeverity == nil || *got.Metadata.SecuritySeverity != 0 {
		t.Errorf("a security severity of zero read back as %v", got.Metadata.SecuritySeverity)
	}
	if !reflect.DeepEqual(got.Metadata.Tags, []string{"security", "actions"}) {
		t.Errorf("tags read %v", got.Metadata.Tags)
	}

	if _, err := s.SaveQueryRequest(aRequest()); err != nil {
		t.Fatal(err)
	}
	all, _ := s.QueryRequests()
	if m := all[0].Metadata; m.SecuritySeverity != nil || m.Tags == nil || len(m.Tags) != 0 {
		t.Errorf("a request asking for no metadata reads %+v", m)
	}
}

// Improving a query changes what it finds, not what it is called or how severe it is: those are
// the catalog's.
func TestARequestToImproveAQueryProposesNoMetadata(t *testing.T) {
	s := newTestStore(t)
	r := aFullRequest()
	r.QueryID, r.TargetSource, r.TargetDecision = "acme/self-hosted", TargetOperator, DecisionImprove
	if _, err := s.SaveQueryRequest(r); err == nil || !strings.Contains(err.Error(), "acme/self-hosted") {
		t.Errorf("an improvement proposing metadata: %v", err)
	}
	r.Metadata = RequestedMetadata{}
	if _, err := s.SaveQueryRequest(r); err != nil {
		t.Fatal(err)
	}
	if got := onlyRequest(t, s); got.TargetDecision != DecisionImprove {
		t.Errorf("decision read %q", got.TargetDecision)
	}
	for name, change := range map[string]func(*QueryRequest){
		"a decision with no target": func(r *QueryRequest) { r.QueryID, r.TargetSource = "", "" },
		"an unknown decision":       func(r *QueryRequest) { r.TargetDecision = "maybe" },
	} {
		r := aFullRequest()
		r.Slug, r.Metadata = "", RequestedMetadata{}
		r.QueryID, r.TargetSource, r.TargetDecision = "acme/x", TargetOperator, DecisionCreate
		change(&r)
		if _, err := s.SaveQueryRequest(r); err == nil {
			t.Errorf("%s was kept", name)
		}
	}
}

// An edit says what is asked for now, so examples, references and tags it drops are gone.
func TestEditingARequestReplacesItsExamplesReferencesAndTags(t *testing.T) {
	s := newTestStore(t)
	id, err := s.SaveQueryRequest(aFullRequest())
	if err != nil {
		t.Fatal(err)
	}
	edit := aFullRequest()
	edit.RowID = id
	edit.Examples = edit.Examples[:2]
	edit.References = []RequestReference{}
	edit.Metadata.SecuritySeverity, edit.Metadata.Tags = nil, []string{"actions"}
	if _, err := s.SaveQueryRequest(edit); err != nil {
		t.Fatal(err)
	}
	r := onlyRequest(t, s)
	if len(r.Examples) != 2 || len(r.References) != 0 || len(r.ReferenceTags) != 0 ||
		!reflect.DeepEqual(r.Metadata.Tags, []string{"actions"}) || r.Metadata.SecuritySeverity != nil {
		t.Errorf("an edit left %+v", r)
	}
}
