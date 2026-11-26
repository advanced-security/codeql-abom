package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/coordination"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func runAudit(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	switch {
	case len(args) > 0 && args[0] == "verify":
		return auditVerify(args[1:], stdout, stderr)
	case len(args) > 0 && args[0] == "rotate-key":
		return auditRotateKey(ctx, args[1:], stdout, stderr)
	}
	fmt.Fprintln(stderr, "Usage: codeql-abom audit verify|rotate-key [--db PATH] [--authz-dir DIR]")
	return errors.New("audit: name a subcommand: verify or rotate-key")
}

// auditRotateKey makes a new decision key and retires the old one at the log's head, trusted for the
// overlap for what is presented now. It takes the coordinator's lease, so no server is signing with
// the old key while it retires: an entry that key logged afterwards would fail the log for good.
func auditRotateKey(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("audit rotate-key", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", os.Getenv("ABOM_DB"), "SQLite database path or PostgreSQL URL (defaults to ABOM_DB)")
	dir := fs.String("authz-dir", "", "directory holding the decision key and trust bundle (default: authz beside --db)")
	overlap := fs.Duration("overlap", 7*24*time.Hour, "how long the old key stays trusted after the rotation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	keys := cmp.Or(*dir, store.DefaultAuthzDir(*dbPath))
	if keys == "" {
		return errors.New("audit rotate-key: name the key directory with --authz-dir, or a SQLite database with --db")
	}
	if *overlap <= 0 {
		return errors.New("audit rotate-key: --overlap must be positive, or what the old key signed stops verifying at once")
	}
	if *dbPath == "" {
		return errors.New("audit rotate-key: name the database the rotation is recorded in with --db")
	}
	if key := os.Getenv(authz.KMSKeyEnv); key != "" {
		return fmt.Errorf("audit rotate-key: decisions are signed with the KMS key %s names (%s); rotate by "+
			"naming a new key there and restarting, which retires this one", authz.KMSKeyEnv, key)
	}
	// The outgoing key decides its own retirement, so the log says which key replaced which.
	s, err := store.OpenBackend(*dbPath, store.Options{AuthzDir: keys})
	if err != nil {
		return fmt.Errorf("opening %s: %w", store.DatabaseLabel(*dbPath), err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", store.DatabaseLabel(*dbPath), err)
	}
	_, leased, release, err := coordination.Hold(ctx, s)
	if errors.Is(err, coordination.ErrCoordinatorActive) {
		return errors.New("audit rotate-key: a server is running against this database and signing with the " +
			"current key; stop it, rotate, and start it again")
	}
	if err != nil {
		return fmt.Errorf("audit rotate-key: %w", err)
	}
	defer func() { _ = release() }()
	if _, err := decideCommand(ctx, leased, "audit", append([]string{"rotate-key"}, args...)); err != nil {
		return err
	}
	now := time.Now()
	signer, err := authz.RotateLocal(keys, leased, *overlap, now)
	if err != nil {
		return fmt.Errorf("audit rotate-key: %w", err)
	}
	fmt.Fprintf(stdout, "audit rotate-key: decisions are now signed with %s; what the old key logged still "+
		"verifies, and a grant it signed is trusted until %s.\n",
		signer.KeyID(), now.Add(*overlap).UTC().Format(time.RFC3339))
	return nil
}

// auditVerify walks the log offline: every signature against the trust bundle, and the chain.
func auditVerify(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("audit verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", os.Getenv("ABOM_DB"), "SQLite database path or PostgreSQL URL (defaults to ABOM_DB)")
	dir := fs.String("authz-dir", "", "directory holding the trust bundle (default: authz beside --db)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		return errors.New("audit verify: name the database with --db or ABOM_DB")
	}
	trust := cmp.Or(*dir, store.DefaultAuthzDir(*dbPath))
	if trust == "" {
		return errors.New("audit verify: a PostgreSQL database keeps no trust bundle beside it; name it with --authz-dir")
	}
	bundle, err := authz.ReadBundle(trust)
	if err != nil {
		return fmt.Errorf("audit verify: reading the trust bundle in %s: %w", trust, err)
	}
	s, err := store.OpenBackend(*dbPath, store.Options{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("audit verify: opening %s: %w", store.DatabaseLabel(*dbPath), err)
	}
	defer s.Close()
	entries, err := s.Assertions(0, 0)
	if err != nil {
		return fmt.Errorf("audit verify: %w", err)
	}

	r := authz.VerifyLog(entries, bundle)
	if r.Failure != nil {
		fmt.Fprintf(stdout, "audit verify: %d assertions verified before entry %d, which fails: %s; nothing after it is trusted\n",
			r.Verified, r.Failure.Seq, r.Failure.Why)
		return fmt.Errorf("audit verify: entry %d of %d does not verify", r.Failure.Seq, len(entries))
	}
	checkpoints := "checkpoints"
	if r.Checkpoints == 1 {
		checkpoints = "checkpoint"
	}
	fmt.Fprintf(stdout, "audit verify: %d assertions verified, %d %s, head %s\n", r.Verified, r.Checkpoints, checkpoints, r.Head)
	return nil
}
