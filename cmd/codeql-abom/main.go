// Command codeql-abom builds and serves an Actions Bill of Materials for a GitHub organization.
//
// Every subcommand is thin wiring over internal packages: this file and its siblings parse
// flags, open the store and call into store/graph/ingest/expand/serve, which is where the
// actual behavior lives.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// subcommand is one entry in the dispatch table, in the order usage prints them.
type subcommand struct {
	name string
	run  func(ctx context.Context, args []string, stdout, stderr io.Writer) error
	desc string
}

var subcommands = []subcommand{
	{"discover", runDiscover, "Find repositories in an organization that have CodeQL databases."},
	{"build", runBuild, "Fetch, query and ingest seed repositories."},
	{"expand-all", runExpandAll, "Follow dependencies upstream, then refresh every aggregate."},
	{"report", runReport, "Print a summary of the collected ABOM."},
	{"serve", runServe, "Serve the ABOM over HTTP for the user interface."},
	{"accounts", runAccounts, "Show the accounts you are verified to belong to."},
	{"own", runOwned, "Declare which accounts you own, which is what discovery is bounded by."},
	{"prune", runPrune, "Remove what the declared scope does not reach."},
	{"bom", runBOM, "Write a bill of materials for the declared scope, or for a named root."},
	{"stale", runStale, "Report findings no tracked run of their suite still reports."},
	{"branches", runBranches, "Record which branches the declared repositories protect."},
	{"selfcheck", runSelfcheck, "Analyze a source tree with a CodeQL suite as a smoke test."},
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

// run dispatches to the named subcommand. It is separate from main so tests can drive it
// without touching os.Args or os.Exit.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stdout)
		return errors.New("codeql-abom: missing subcommand")
	}

	switch args[0] {
	case "-h", "--help", "help":
		usage(stdout)
		return nil
	}

	for _, sc := range subcommands {
		if sc.name == args[0] {
			return sc.run(ctx, args[1:], stdout, stderr)
		}
	}

	usage(stderr)
	return fmt.Errorf("codeql-abom: unknown subcommand %q", args[0])
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: codeql-abom <subcommand> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	for _, sc := range subcommands {
		fmt.Fprintf(w, "  %-12s %s\n", sc.name, sc.desc)
	}
}
