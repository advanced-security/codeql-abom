package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func runReader(ctx context.Context, cfg serveConfig, stdout, stderr io.Writer) error {
	primary, err := store.OpenBackend(cfg.dbPath, store.Options{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("opening read-only store: %w", err)
	}
	defer primary.Close()
	if _, err := primary.GraphVersion(); err != nil {
		return fmt.Errorf("a reader needs a schema initialized by the coordinator: %w", err)
	}
	srv := &serve.Server{
		Store: primary, ReadOnly: true, Graphviz: cfg.graphviz, GroupProperty: cfg.groupProperty, Log: stderr,
	}
	if cfg.uiDir != "" {
		srv.UI = os.DirFS(cfg.uiDir)
	}
	if cfg.readReplica != "" {
		replica, err := openReadReplica(cfg, primary)
		if err != nil {
			return err
		}
		defer replica.Close()
		srv.SetReplica(replica, cfg.replicaMaxLag)
	}
	return serveHTTP(ctx, &http.Server{Addr: cfg.addr, Handler: srv.Handler()}, cfg.addr, stdout)
}

func openReadReplica(cfg serveConfig, primary store.Backend) (store.Backend, error) {
	reader, err := store.OpenBackend(cfg.readReplica, store.Options{ReadOnly: true})
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
