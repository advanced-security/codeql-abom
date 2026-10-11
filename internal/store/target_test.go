package store

import (
	"strings"
	"testing"
)

func TestPostgreSQLTargetsNeverExposeCredentialsInLabels(t *testing.T) {
	for _, target := range []string{
		"postgres://operator:secret@localhost:5432/abom?password=also-secret",
		"postgresql://operator:secret@localhost/abom?sslkey=/private/key",
		"postgresql://operator:bad%password@localhost/abom",
	} {
		if !IsPostgresTarget(target) {
			t.Fatalf("not recognized as a PostgreSQL target: %q", target)
		}
		label := DatabaseLabel(target)
		for _, secret := range []string{"operator", "secret", "password", "/private/key"} {
			if strings.Contains(label, secret) {
				t.Fatalf("database label contains credential material: %q", label)
			}
		}
	}
}

func TestFailedPostgreSQLOpensDoNotDisclosePasswords(t *testing.T) {
	for _, target := range []string{
		"postgresql://operator:very-secret@127.0.0.1:1/abom?connect_timeout=1",
		"postgresql://operator:very-secret%zz@localhost/abom",
	} {
		backend, err := OpenBackend(target, Options{})
		if backend != nil {
			_ = backend.Close()
		}
		if err == nil {
			t.Fatal("an invalid connection unexpectedly succeeded")
		}
		if strings.Contains(err.Error(), "very-secret") {
			t.Fatal("a PostgreSQL connection error disclosed its password")
		}
	}
}

func TestSQLiteDatabaseLabelsRemainPaths(t *testing.T) {
	for _, target := range []string{"abom.db", "/home/operator/abom.db", "file:abom.db?mode=ro"} {
		if IsPostgresTarget(target) || DatabaseLabel(target) != target {
			t.Fatalf("SQLite target was reinterpreted: %q", target)
		}
	}
}
