package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A suite is the unit ABOM expands over time, so what it resolves to has to be discovered from
// the CLI rather than listed in Go. Hard-coding the query set would mean a release for every
// query added, which is the opposite of a reprogrammable inspection engine.
func TestResolveQueriesReadsTheSuiteFromTheCLI(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "AbomEdges.ql")
	two := filepath.Join(dir, "PrintAST.ql")
	stub, argsFile := stubCodeQL(t, "printf '%s\\n%s\\n' "+one+" "+two+"\n")
	r := &Runner{CodeQLPath: stub}

	got, err := r.ResolveQueries(context.Background(), "abom-dependency-graph.qls")
	if err != nil {
		t.Fatalf("ResolveQueries: %v", err)
	}
	if len(got) != 2 || got[0] != one || got[1] != two {
		t.Errorf("got %v, want both queries the suite names", got)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	for _, want := range []string{"resolve", "queries", "abom-dependency-graph.qls"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

// An empty suite is an answer, not a failure: a suite may legitimately select nothing for a
// language, and treating that as an error would stop the whole run.
func TestResolveQueriesAcceptsAnEmptySuite(t *testing.T) {
	stub, _ := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	got, err := r.ResolveQueries(context.Background(), "empty.qls")
	if err != nil {
		t.Fatalf("ResolveQueries: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}

// How a query's results must be read is a property of the query, declared by the query. Reading
// it from the CLI is what lets a new query be added to a suite and ingested correctly without
// anyone editing the ingester.
func TestResolveMetadataReadsWhatTheQueryDeclares(t *testing.T) {
	stub, argsFile := stubCodeQL(t,
		`printf '{"id":"actions/tools/print-ast","kind":"graph","tags":"ast"}'`+"\n")
	r := &Runner{CodeQLPath: stub}

	md, err := r.ResolveMetadata(context.Background(), "/q/PrintAST.ql")
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if md.ID != "actions/tools/print-ast" {
		t.Errorf("ID = %q", md.ID)
	}
	if md.Kind != "graph" {
		t.Errorf("Kind = %q", md.Kind)
	}
	if !md.HasTag("ast") {
		t.Errorf("tags %q did not carry ast", md.Tags)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	for _, want := range []string{"resolve", "metadata", "--format=json", "PrintAST.ql"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

// A structural query declares no kind, which is exactly how it is told apart from one whose
// results are findings. Guessing from the filename would break the moment a query is renamed.
func TestAQueryWithoutAKindIsStructural(t *testing.T) {
	stub, _ := stubCodeQL(t, `printf '{"id":"abom/edges"}'`+"\n")
	r := &Runner{CodeQLPath: stub}

	md, err := r.ResolveMetadata(context.Background(), "/q/AbomEdges.ql")
	if err != nil {
		t.Fatalf("ResolveMetadata: %v", err)
	}
	if md.Kind != "" {
		t.Errorf("Kind = %q, want empty for a query that declares none", md.Kind)
	}
}

// Tags are a space-separated string in the CLI's own output, so a query tagged for several
// purposes has to match on any of them rather than on the whole string.
func TestTagsMatchIndividually(t *testing.T) {
	md := Metadata{Tags: "ast experimental abom-observation"}
	for _, tag := range []string{"ast", "experimental", "abom-observation"} {
		if !md.HasTag(tag) {
			t.Errorf("HasTag(%q) = false", tag)
		}
	}
	if md.HasTag("observation") {
		t.Error("HasTag matched a fragment of a tag rather than a whole one")
	}
}
