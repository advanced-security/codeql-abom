package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func runReader(ctx context.Context, cfg serveConfig, stdout, stderr io.Writer) error {
	srv, closeReader, err := openReader(cfg)
	if err != nil {
		return err
	}
	defer closeReader()
	srv.Log = stderr
	return serveWithProbes(ctx, cfg, srv, stdout)
}

// openReader opens a server that only reads, verifying declarations with the key directory the
// coordinator signs with, as every view of the database does.
func openReader(cfg serveConfig) (*serve.Server, func(), error) {
	primary, err := store.OpenBackend(cfg.dbPath, store.Options{ReadOnly: true, AuthzDir: cfg.authzDir})
	if err != nil {
		return nil, nil, fmt.Errorf("opening read-only store: %w", err)
	}
	if _, err := primary.GraphVersion(); err != nil {
		_ = primary.Close()
		return nil, nil, fmt.Errorf("a reader needs a schema initialized by the coordinator: %w", err)
	}
	srv := &serve.Server{Store: primary, ReadOnly: true, Graphviz: cfg.graphviz, GroupProperty: cfg.groupProperty}
	if cfg.uiDir != "" {
		srv.UI = os.DirFS(cfg.uiDir)
	}
	closeReader := func() { _ = primary.Close() }
	if cfg.readReplica != "" {
		replica, err := openReadReplica(cfg, primary)
		if err != nil {
			closeReader()
			return nil, nil, err
		}
		srv.SetReplica(replica, cfg.replicaMaxLag)
		closeReader = func() { _ = replica.Close(); _ = primary.Close() }
	}
	return srv, closeReader, nil
}

func openReadReplica(cfg serveConfig, primary store.Backend) (store.Backend, error) {
	reader, err := store.OpenBackend(cfg.readReplica, store.Options{ReadOnly: true, AuthzDir: cfg.authzDir})
	if err != nil {
		return nil, fmt.Errorf("opening read replica: %w", err)
	}
	fail := func(err error) (store.Backend, error) { return nil, errors.Join(err, reader.Close()) }
	state, err := reader.ReplicationState()
	if err != nil {
		return fail(err)
	}
	if !state.Recovery {
		return fail(errors.New("the configured read replica is not a PostgreSQL standby"))
	}
	source, err := primary.ReplicationState()
	if err != nil {
		return fail(err)
	}
	if source.Identity != state.Identity || source.Database != state.Database || source.Schema != state.Schema {
		return fail(errors.New("the configured read replica does not belong to the primary ABOM store"))
	}
	return reader, nil
}
