package store

import (
	"reflect"
	"testing"
)

func TestReadOnlyBackendsRenderFullAndScopedGraphsWithoutPersistingChanges(t *testing.T) {
	s := sharedGroupingFixture(t)
	reader, err := OpenBackend(s.dsn, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, scope := range []string{"", ScopeOwned, ScopeSupplyChain} {
		t.Run(scope, func(t *testing.T) {
			opts := FullGraphOptions{Scope: scope}
			want, err := s.FullGraph(opts)
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.DatabaseState()
			if err != nil {
				t.Fatal(err)
			}
			got, err := reader.FullGraph(opts)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("read-only %q graph differs from the primary", scope)
			}
			after, err := s.DatabaseState()
			if err != nil || before != after {
				t.Fatalf("read-only %q graph changed the source: %v", scope, err)
			}
		})
	}
}
