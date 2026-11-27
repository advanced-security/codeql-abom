package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/execution"
)

type attestorConfig struct {
	grant, trust, svid, socket string
}

// parseAttestorArgs reads the attestor's flags, which execution.Kubernetes writes into every Job.
func parseAttestorArgs(args []string, stderr io.Writer) (attestorConfig, error) {
	var c attestorConfig
	fs := flag.NewFlagSet("attestor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.grant, "grant", "", "file holding the signed task grant")
	fs.StringVar(&c.trust, "trust", "", "directory holding trust.json, the decision point's bundle")
	fs.StringVar(&c.svid, "svid", "/var/run/secrets/spiffe.io", "directory holding this workload's SPIFFE identity")
	fs.StringVar(&c.socket, "socket", "", "socket the worker beside this attestor reaches it on")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() > 0 {
		return c, errors.New("attestor: takes no arguments")
	}
	if c.grant == "" || c.trust == "" || c.socket == "" {
		return c, errors.New("attestor: name the grant with --grant, the decision bundle with --trust and the socket with --socket")
	}
	return c, nil
}

// runAttestor holds one attempt's secrets beside its worker and answers it on a socket until the pod
// stops: it verifies the grant, reads what the grant binds, and signs and writes what the worker
// leaves, reading nothing the worker or an agent wrote.
func runAttestor(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	c, err := parseAttestorArgs(args, stderr)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(c.grant)
	if err != nil {
		return fmt.Errorf("attestor: reading the grant: %w", err)
	}
	var grant authz.Envelope
	if err := json.Unmarshal(body, &grant); err != nil {
		return fmt.Errorf("attestor: the grant is not an envelope: %w", err)
	}
	bundle, err := authz.ReadBundle(c.trust)
	if err != nil {
		return fmt.Errorf("attestor: reading the decision bundle: %w", err)
	}
	identity, err := execution.LoadSVID(c.svid)
	if err != nil {
		return fmt.Errorf("attestor: %w", err)
	}
	key, err := takeAttemptKey()
	if err != nil {
		return fmt.Errorf("attestor: %w", err)
	}
	objects, err := execution.S3FromEnv(ctx)
	if err != nil {
		return fmt.Errorf("attestor: %w", err)
	}
	if err := os.Remove(c.socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("attestor: %w", err)
	}
	ln, err := net.Listen("unix", c.socket)
	if err != nil {
		return fmt.Errorf("attestor: %w", err)
	}
	a := execution.Hold(grant, execution.Secrets{Bundle: bundle, Identity: identity, AttemptKey: key, Objects: objects,
		// The identity rotates under a session that outlasts it, so each signature reads it again.
		Renew: func() (*execution.Identity, error) { return execution.LoadSVID(c.svid) }})
	// A grant that does not hold is still served, refused, so the worker's last words say why.
	if _, _, err := a.Grant(ctx); err != nil {
		fmt.Fprintf(stderr, "attestor: %v\n", err)
	}
	fmt.Fprintf(stdout, "attestor: %s holds its attempt's secrets and answers its worker on %s\n", identity.ID, c.socket)
	return execution.ServeAttestor(ctx, ln, a)
}

// takeAttemptKey reads the attempt's key from the environment, and leaves it gone from what anything
// this process starts would inherit.
func takeAttemptKey() (*execution.AttemptKey, error) {
	key, err := execution.ParseAttemptKey(os.Getenv(execution.AttemptKeySecret))
	_ = os.Unsetenv(execution.AttemptKeySecret)
	return key, err
}
