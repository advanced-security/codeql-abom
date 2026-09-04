package sarifin

import (
	"strings"
	"testing"
)

func TestReadProvenance(t *testing.T) {
	const doc = `{"version":"2.1.0","runs":[{
		"tool":{"driver":{"name":"CodeQL"}},
		"results":[],
		"versionControlProvenance":[
			{"repositoryUri":"https://github.com/514-labs/moosestack",
			 "revisionId":"99bcb9f9fff7230e23db49fd5b70af62af511bf9"}]}]}`

	got, err := ReadProvenance(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadProvenance: %v", err)
	}
	if got.Repository != "514-labs/moosestack" {
		t.Errorf("Repository = %q, want 514-labs/moosestack", got.Repository)
	}
	if got.CommitSHA != "99bcb9f9fff7230e23db49fd5b70af62af511bf9" {
		t.Errorf("CommitSHA = %q", got.CommitSHA)
	}
}

// Provenance may appear after the results array, so the reader must not stop early.
func TestReadProvenanceAfterResults(t *testing.T) {
	const doc = `{"runs":[{
		"results":[{"ruleId":"a"},{"ruleId":"b"}],
		"versionControlProvenance":[{"repositoryUri":"https://github.com/octo/app"}]}]}`

	got, err := ReadProvenance(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadProvenance: %v", err)
	}
	if got.Repository != "octo/app" {
		t.Errorf("Repository = %q, want octo/app", got.Repository)
	}
}

// A repository name is only usable if it is owner/name; anything else must be reported absent
// rather than silently mis-attributed.
func TestReadProvenanceRejectsUnusableURI(t *testing.T) {
	for _, uri := range []string{"https://github.com/", "not-a-url", ""} {
		doc := `{"runs":[{"versionControlProvenance":[{"repositoryUri":"` + uri + `"}]}]}`
		got, err := ReadProvenance(strings.NewReader(doc))
		if err != nil {
			t.Fatalf("ReadProvenance(%q): %v", uri, err)
		}
		if got.Repository != "" {
			t.Errorf("ReadProvenance(%q) = %q, want empty", uri, got.Repository)
		}
	}
}

func TestReadProvenanceAbsent(t *testing.T) {
	got, err := ReadProvenance(strings.NewReader(`{"runs":[{"results":[]}]}`))
	if err != nil {
		t.Fatalf("ReadProvenance: %v", err)
	}
	if got.Repository != "" || got.CommitSHA != "" {
		t.Errorf("got %+v, want empty provenance", got)
	}
}

// Enterprise hosts must resolve to owner/name just as github.com does.
func TestReadProvenanceEnterpriseHost(t *testing.T) {
	const doc = `{"runs":[{"versionControlProvenance":[
		{"repositoryUri":"https://github.example.com/octo-org/app"}]}]}`

	got, err := ReadProvenance(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ReadProvenance: %v", err)
	}
	if got.Repository != "octo-org/app" {
		t.Errorf("Repository = %q, want octo-org/app", got.Repository)
	}
}

func TestReadProvenanceTruncated(t *testing.T) {
	if _, err := ReadProvenance(strings.NewReader(`{"runs":[{"results":[`)); err == nil {
		t.Error("expected an error for truncated input")
	}
}
