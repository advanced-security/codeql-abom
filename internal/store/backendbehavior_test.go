package store

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestTheBackendContractIncludesEverySQLiteStorageOperation(t *testing.T) {
	concrete, contract := reflect.TypeFor[*Store](), reflect.TypeFor[Backend]()
	for i := 0; i < concrete.NumMethod(); i++ {
		method := concrete.Method(i)
		// Raw SQLite fixtures are not portable persistence capabilities.
		if method.Name == "DB" || method.Name == "SnapshotInto" {
			continue
		}
		if _, ok := contract.MethodByName(method.Name); !ok {
			t.Errorf("SQLite operation %s is absent from the backend contract", method.Name)
		}
	}
}

func TestSQLiteFailedBackendOpenReturnsNilRatherThanAnInterfaceHoldingANilPointer(t *testing.T) {
	s, err := OpenBackend(filepath.Join(t.TempDir(), "missing", "abom.db"), Options{})
	if err == nil {
		if s != nil {
			_ = s.Close()
		}
		t.Fatal("opening inside a missing directory succeeded")
	}
	if s != nil {
		t.Fatalf("opening failed but returned a non-nil backend: %T", s)
	}
}
