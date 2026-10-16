package main

import (
	"context"
	"io"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/sourcepolicy"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func TestRemoteSourcePolicyAgainstRecordedCorpus(t *testing.T) {
	path := os.Getenv("ABOM_SOURCE_POLICY_CORPUS")
	if path == "" {
		t.Skip("set ABOM_SOURCE_POLICY_CORPUS to a real corpus backup for a read-only policy comparison")
	}
	s, err := store.OpenBackend(path, store.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := s.Summary()
	if err != nil {
		t.Fatal(err)
	}
	names, err := s.RepositoryNames()
	if err != nil || len(names) == 0 {
		t.Fatalf("corpus names = %d, %v", len(names), err)
	}
	reader := newReader(context.Background(), defaultAPIURL(), nil)
	reader.MaxRetries = 0
	p := protectSources(s, reader, io.Discard)
	warnings := 0
	p.Warn = func(_, _ string) { warnings++ }
	sorted := make([]string, 0, len(names))
	for _, name := range names {
		sorted = append(sorted, name)
	}
	slices.Sort(sorted)
	allowed, denied := 0, 0
	for _, name := range sorted {
		err := p.Check(context.Background(), name)
		switch {
		case sourcepolicy.IsDenied(err):
			denied++
			t.Log(err)
		case err != nil:
			t.Fatal(err)
		default:
			allowed++
		}
	}
	after, err := s.Summary()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("source checks changed corpus evidence: before=%+v after=%+v err=%v", before, after, err)
	}
	if allowed == 0 {
		t.Fatal("no source allowed; the real positive path was not exercised")
	}
	t.Logf("Before: %d repositories had no sourcing guard. After: allowed=%d (warnings=%d), skipped=%d, API requests=%d",
		len(sorted), allowed, warnings, denied, reader.Stats().Requests)
	t.Logf("Corpus before/after unchanged: %+v", after)
}
