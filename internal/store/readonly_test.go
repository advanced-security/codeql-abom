package store

import (
	"reflect"
	"strings"
	"testing"
)

func TestReadOnlyBackendsReadButCannotMigrateOrWrite(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertRepository("readonly/source", ""); err != nil {
		t.Fatal(err)
	}
	backend, err := OpenBackend(s.dsn, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	before, err := backend.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Migrate(); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only migration was not refused: %v", err)
	}
	if _, err := backend.UpsertRepository("readonly/forbidden", ""); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only write was not refused: %v", err)
	}
	writer := backend.NewWriter(1)
	if err := writer.AddNode(Node{Key: "readonly/forbidden", Kind: "action"}); err == nil {
		t.Fatal("read-only graph writer persisted a node")
	}
	after, err := s.Summary()
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("read-only connection changed its source: %+v %+v %v", before, after, err)
	}
	reader := backend.(*Store)
	if reader.db.postgres {
		var setting string
		if err := reader.db.DB.QueryRow(`SELECT current_setting('default_transaction_read_only')`).Scan(&setting); err != nil || setting != "on" {
			t.Fatalf("PostgreSQL is not enforcing read-only transactions: %q %v", setting, err)
		}
	}
}
