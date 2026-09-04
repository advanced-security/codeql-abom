package main

import (
	"context"
	"strings"
	"testing"
)

// A typo in a subcommand name must be rejected, not silently ignored, since a run that does
// nothing looks identical to a run that succeeded.
func TestRunRejectsUnknownSubcommand(t *testing.T) {
	var stdout, stderr strings.Builder
	err := run(context.Background(), []string{"bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run accepted an unknown subcommand")
	}
	if !strings.Contains(stderr.String(), "discover") {
		t.Errorf("usage on stderr = %q, want it to list subcommands", stderr.String())
	}
}

// No arguments is a usage error, not a silent no-op.
func TestRunRequiresASubcommand(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), nil, &stdout, &stderr); err == nil {
		t.Fatal("run accepted no arguments")
	}
	if !strings.Contains(stdout.String(), "Subcommands:") {
		t.Errorf("usage on stdout = %q, want the subcommand list", stdout.String())
	}
}

// --help must list every subcommand without treating the request as an error.
func TestRunHelpListsEverySubcommand(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), []string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("run(--help): %v", err)
	}
	for _, sc := range subcommands {
		if !strings.Contains(stdout.String(), sc.name) {
			t.Errorf("usage missing subcommand %q:\n%s", sc.name, stdout.String())
		}
	}
}

// Each subcommand name must dispatch to a distinct, non-nil handler, or a copy-paste mistake in
// the table would silently run the wrong subcommand.
func TestSubcommandsHaveDistinctHandlers(t *testing.T) {
	seen := map[string]bool{}
	for _, sc := range subcommands {
		if sc.name == "" {
			t.Fatal("subcommand with an empty name")
		}
		if seen[sc.name] {
			t.Errorf("subcommand %q registered more than once", sc.name)
		}
		seen[sc.name] = true
		if sc.run == nil {
			t.Errorf("subcommand %q has no handler", sc.name)
		}
		if sc.desc == "" {
			t.Errorf("subcommand %q has no usage description", sc.name)
		}
	}
}
