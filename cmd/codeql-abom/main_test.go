package main

import (
	"context"
	"flag"
	"os"
	"strings"
	"testing"
)

// -h prints each flag's default, and a PostgreSQL URL carries its password: inside the coordinator,
// `own -h` printed DATABASE_URL whole. The environment still supplies the value a flag was not given.
func TestHelpPrintsNoDefaultTheEnvironmentSupplies(t *testing.T) {
	const password = "hunter2-in-the-environment"
	for _, env := range []string{"ABOM_DB", "ABOM_READ_REPLICA"} {
		t.Setenv(env, "postgres://abom:"+password+"@127.0.0.1:1/abom")
	}
	commands := [][]string{{"audit", "verify"}, {"audit", "rotate-key"}}
	for _, action := range []string{"create", "status", "installations", "rotate-key"} {
		commands = append(commands, []string{"app", action})
	}
	for _, sc := range subcommands {
		commands = append(commands, []string{sc.name})
	}
	for _, c := range commands {
		var out strings.Builder
		_ = run(context.Background(), append(c, "-h"), &out, &out)
		if strings.Contains(out.String(), password) {
			t.Errorf("%s -h printed the password the environment holds", strings.Join(c, " "))
		}
	}

	fs := flag.NewFlagSet("own", flag.ContinueOnError)
	db := databaseFlag(fs)
	if err := fs.Parse(nil); err != nil || *db != os.Getenv("ABOM_DB") {
		t.Errorf("--db = %q (%v), want ABOM_DB when the flag is not given", *db, err)
	}
}

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

// An authoring session starts ql-mcp behind this relay, so it relays the server it is named in
// both directions, and answers everything the server said before the client hung up.
func TestSerializeMCPRelaysTheServerItNames(t *testing.T) {
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`
	stdin = strings.NewReader(call + "\n")
	t.Cleanup(func() { stdin = os.Stdin })
	var stdout, stderr strings.Builder
	if err := run(context.Background(), []string{"serialize-mcp", "--", "cat"}, &stdout, &stderr); err != nil {
		t.Fatalf("serialize-mcp: %v (%s)", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != call {
		t.Errorf("relayed %q, want what the server wrote back", stdout.String())
	}
	if err := run(context.Background(), []string{"serialize-mcp"}, &stdout, &stderr); err == nil {
		t.Error("serialize-mcp ran with no server named")
	}
}

// The relay a session starts ql-mcp behind is this binary's own serialize-mcp.
func TestQlmcpIsRelayedByThisBinary(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	line := relay()
	if len(line) != 3 || line[0] != self || line[2] != "--" {
		t.Fatalf("relay is %v, want %s serialize-mcp --", line, self)
	}
	known := false
	for _, sc := range subcommands {
		known = known || sc.name == line[1]
	}
	if !known {
		t.Errorf("the relay names %s, which is no subcommand", line[1])
	}
}
