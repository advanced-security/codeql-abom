package store

import (
	"strings"
	"testing"
)

func TestMalformedPostgresURLsDoNotLeakCredentialsThroughDriverErrors(t *testing.T) {
	backend, err := OpenBackend("postgres://operator:top-secret@localhost/%zz", Options{})
	if backend != nil {
		_ = backend.Close()
		t.Fatal("malformed connection returned a backend")
	}
	if err == nil {
		t.Fatal("malformed connection succeeded")
	}
	for _, credential := range []string{"operator", "top-secret"} {
		if strings.Contains(err.Error(), credential) {
			t.Fatalf("driver error exposed connection credentials: %v", err)
		}
	}
}
