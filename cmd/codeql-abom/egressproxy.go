package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/egress"
)

// runEgressProxy forwards an authoring pod's tunnels to the hosts it names and refuses, and records,
// everything else. Which pods reach it is the NetworkPolicy's to say, and it reaches an allowed name
// only at public addresses.
func runEgressProxy(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("egress-proxy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", ":3128", "address to listen on")
	allow := fs.String("allow", strings.Join(egress.CopilotHosts, ","),
		"comma-separated hosts tunnels may reach; a leading dot allows a suffix")
	ports := fs.String("ports", "443", "comma-separated ports tunnels may reach")
	tunnels := fs.Int("max-tunnels", 256, "tunnels open at once, past which one is refused")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("egress-proxy: takes no arguments")
	}
	hosts, open := splitList(*allow), splitList(*ports)
	if len(hosts) == 0 || len(open) == 0 || *tunnels < 1 {
		return errors.New("egress-proxy: name at least one host and port, and room for a tunnel")
	}
	fmt.Fprintf(stdout, "egress-proxy: tunnels from %s reach only %s, on %s, at public addresses\n", *addr,
		strings.Join(hosts, ", "), strings.Join(open, ", "))
	return egress.Listen(ctx, *addr, &egress.Proxy{Allow: hosts, Ports: open, MaxTunnels: *tunnels, Log: stdout})
}

// splitList is the non-empty items of a comma-separated flag.
func splitList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
