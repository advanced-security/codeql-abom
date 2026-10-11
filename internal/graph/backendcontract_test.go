package graph

import (
	"errors"
	"reflect"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

type wrappedBackend struct {
	store.Backend
	transactions, reachWrites, exposureWrites, rollbacks int
	failMark                                             bool
}

type wrappedGraphWriter struct{ store.GraphWriter }
type wrappedFrontier struct{ store.Frontier }

func (s *wrappedBackend) NewWriter(size int) store.GraphWriter {
	return wrappedGraphWriter{s.Backend.NewWriter(size)}
}

func (s *wrappedBackend) Frontier(opts store.FrontierOptions) store.Frontier {
	return wrappedFrontier{s.Backend.Frontier(opts)}
}

func (s *wrappedBackend) BeginDerivation() (store.DerivationWriter, error) {
	w, err := s.Backend.BeginDerivation()
	if err != nil {
		return nil, err
	}
	s.transactions++
	return &wrappedDerivation{DerivationWriter: w, backend: s}, nil
}

type wrappedDerivation struct {
	store.DerivationWriter
	backend *wrappedBackend
}

var errMarkRefused = errors.New("test backend refused the reach mark")

func (w *wrappedDerivation) WriteReach(node int64, reach store.ReachWrite) (int, error) {
	w.backend.reachWrites++
	return w.DerivationWriter.WriteReach(node, reach)
}

func (w *wrappedDerivation) WriteExposure(node int64, ghsa string, routes []store.ExposureRoute) (int, error) {
	w.backend.exposureWrites++
	return w.DerivationWriter.WriteExposure(node, ghsa, routes)
}

func (w *wrappedDerivation) MarkReach(version string, inputs map[int64]store.ReachInput, whole bool) error {
	if w.backend.failMark {
		return errMarkRefused
	}
	return w.DerivationWriter.MarkReach(version, inputs, whole)
}

func (w *wrappedDerivation) Rollback() error {
	w.backend.rollbacks++
	return w.DerivationWriter.Rollback()
}

func TestGraphAndQueueAcceptBackendAndWriterImplementationsOtherThanSQLiteTypes(t *testing.T) {
	s := derivationFixture(t)
	backend := &wrappedBackend{Backend: s}
	b := &Builder{Store: backend, Models: &Models{}}
	if _, err := b.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if backend.transactions != 3 || backend.reachWrites == 0 || backend.exposureWrites == 0 {
		t.Fatalf("derivation bypassed its contracts: %+v", backend)
	}
	want := askDrillDown(t, &Builder{Store: s}, "actions/checkout")
	if got := askDrillDown(t, b, "actions/checkout"); !reflect.DeepEqual(got, want) {
		t.Fatal("wrapping the backend changed the traversal answer")
	}
	askDrillDown(t, b, "actions/checkout")
	if b.Models.Loads() != 1 {
		t.Fatal("the wrapper lost the backend's stable cache state")
	}
	w := backend.NewWriter(1)
	if err := w.AddNode(store.Node{Key: "vendor/contract", Kind: "action", IsExternal: true}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	w.ResetLocal()
	q := &frontier.Queue{Store: backend}
	if n, err := q.Enqueue([]frontier.Item{{NodeKey: "vendor/contract", Owner: "vendor", Repo: "contract", Ref: "v1"}}); err != nil || n != 1 {
		t.Fatalf("enqueue: %d, %v", n, err)
	}
	claimed, err := q.Claim(1)
	if err != nil || len(claimed) != 1 || claimed[0].Ref != "v1" {
		t.Fatalf("claim: %+v, %v", claimed, err)
	}
	askDrillDown(t, b, "actions/checkout")
	if b.Models.Loads() != 2 {
		t.Fatal("a contract write failed to retire the cached graph")
	}
}

func TestIncrementalDerivationRollsBackReachAndExposureWhenTheBackendRefusesItsMark(t *testing.T) {
	s := derivationFixture(t)
	backend := &wrappedBackend{Backend: s}
	b := &Builder{Store: backend}
	if _, err := b.Rebuild(); err != nil {
		t.Fatal(err)
	}
	exec(t, s, `UPDATE abom_frontier SET resolved_sha = 'c2' WHERE node_key = 'vendor/comp' AND ref = 'v1'`)
	before := derivedRows(t, s)
	mark, _, err := s.ReadReachMark()
	if err != nil {
		t.Fatal(err)
	}
	backend.transactions, backend.reachWrites, backend.exposureWrites, backend.rollbacks = 0, 0, 0, 0
	backend.failMark = true
	if _, err := b.Derive(); !errors.Is(err, errMarkRefused) {
		t.Fatalf("Derive: %v, want the backend's refusal", err)
	}
	if backend.transactions != 1 || backend.reachWrites == 0 || backend.exposureWrites == 0 || backend.rollbacks != 1 {
		t.Fatalf("the refusal did not follow both writes in one transaction: %+v", backend)
	}
	if diffs := differences(derivedRows(t, s), before); len(diffs) != 0 {
		t.Fatalf("the failed derivation persisted rows: %v", diffs)
	}
	after, _, err := s.ReadReachMark()
	if err != nil || !reflect.DeepEqual(after, mark) {
		t.Fatalf("the failed derivation changed its mark: %+v, %v", after, err)
	}
	backend.failMark = false
	if _, err := b.Derive(); err != nil {
		t.Fatal(err)
	}
	if diffs := differences(derivedRows(t, s), before); len(diffs) == 0 {
		t.Fatal("the successful retry changed nothing, so the rollback proved nothing")
	}
}
