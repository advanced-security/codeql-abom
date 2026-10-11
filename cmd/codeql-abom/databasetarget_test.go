package main

import (
	"flag"
	"io"
	"testing"
)

func TestDatabaseTargetCanStayOutOfProcessArguments(t *testing.T) {
	t.Setenv("ABOM_DB", "postgresql://operator:secret@localhost/abom")
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	target := databaseFlag(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if *target != "postgresql://operator:secret@localhost/abom" {
		t.Fatal("the database environment variable did not supply the target")
	}
	if err := fs.Parse([]string{"--db", "local.db"}); err != nil || *target != "local.db" {
		t.Fatalf("explicit database target did not override the environment: %v", err)
	}
}
