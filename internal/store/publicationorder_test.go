package store

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func TestConcurrentTransactionsCanVisitGraphRowsInDifferentOrders(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, 0)
	for _, key := range []string{"a", "b"} {
		if err := w.AddNode(Node{Key: key, Kind: "action"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	other := secondTestConnection(t, s)
	start, done := make(chan struct{}), make(chan error, 2)
	for i, connection := range []*Store{s, other} {
		go func() {
			<-start
			order := []string{"a", "b"}
			if i == 1 {
				order = []string{"b", "a"}
			}
			for n := 0; n < 10; n++ {
				if err := writeGraphPair(connection, order, fmt.Sprintf("%d/%d", i, n)); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
	}
	close(start)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	var versions int
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT display_name) FROM abom_node`).Scan(&versions); err != nil || versions != 1 {
		t.Fatalf("a publication split its graph rows: %d %v", versions, err)
	}
}

func secondTestConnection(t *testing.T, s *Store) *Store {
	t.Helper()
	var other *Store
	var err error
	if s.db.postgres {
		other, err = openPostgres(s.dsn, Options{})
	} else {
		other, err = Open(s.dsn, Options{})
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	return other
}

func writeGraphPair(s *Store, order []string, value string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `SELECT row_id FROM abom_node WHERE node_key = ?`
	if s.db.postgres {
		query += ` FOR UPDATE`
	}
	var id int64
	if err := tx.QueryRow(query, order[0]).Scan(&id); err != nil {
		return err
	}
	for _, key := range order {
		if _, err := tx.Exec(`UPDATE abom_node SET display_name = ? WHERE node_key = ?`, value, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Writers racing to record the same evidence, each in its own row order, must end where one writer
// alone would have.
func TestConcurrentWritersEndWhereSerialIngestionEnds(t *testing.T) {
	s, err := openTestStore(t, filepath.Join(t.TempDir(), "abom.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	repo, err := s.InsertOwnedRepository("acme/app")
	if err != nil {
		t.Fatal(err)
	}
	writers := []*Store{s, secondTestConnection(t, s), secondTestConnection(t, s)}
	start, done := make(chan struct{}), make(chan error, len(writers))
	for i, writer := range writers {
		go func() {
			<-start
			done <- writeSharedDeclaration(writer, repo, i%2 != 0)
		}()
	}
	close(start)
	for range writers {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if summary, err := s.Summary(); err != nil || summary.Nodes != 2 || summary.Edges != 1 {
		t.Fatalf("concurrent ingestion disagrees with serial ingestion: %+v, %v", summary, err)
	}
}

func writeSharedDeclaration(s Backend, repo int64, reverse bool) error {
	nodes := []Node{
		{Key: "acme/app/ci.yml", Kind: "workflow", RepositoryID: repo},
		{Key: "vendor/action", Kind: "action", IsExternal: true, Owner: "vendor", Repo: "action"},
	}
	if reverse {
		slices.Reverse(nodes)
	}
	w := s.NewWriter(0)
	for _, n := range nodes {
		if err := w.AddNode(n); err != nil {
			return err
		}
	}
	if err := w.AddEdge(Edge{Key: "shared-location", SrcKey: "acme/app/ci.yml", DstKey: "vendor/action",
		Kind: EdgeKindStepUsesAction, RefValue: "v1", RepositoryID: repo}); err != nil {
		return err
	}
	return w.Flush()
}

// Response caches key on the database state, so another process's commit has to change it.
func TestACommitThroughAnotherConnectionChangesTheDatabaseState(t *testing.T) {
	s := newTestStore(t)
	before, err := s.DatabaseState()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondTestConnection(t, s).InsertOwnedRepository("acme/second"); err != nil {
		t.Fatal(err)
	}
	if after, err := s.DatabaseState(); err != nil || after == before {
		t.Fatalf("another connection's commit left the cached state current: %v", err)
	}
}
