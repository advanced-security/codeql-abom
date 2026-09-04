package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// shutdownGrace bounds how long serve waits for in-flight requests once asked to stop.
const shutdownGrace = 5 * time.Second

// runServe answers interface queries over HTTP until the context is canceled.
func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	addr := fs.String("addr", "localhost:8080", "address to listen on")
	uiDir := fs.String("ui-dir", "", "directory containing the published interface (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("serve: --db is required")
	}

	s, err := store.Open(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	srv := &serve.Server{Store: s}
	if *uiDir != "" {
		srv.UI = os.DirFS(*uiDir)
	}

	httpServer := &http.Server{Addr: *addr, Handler: srv.Handler()}

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(stdout, "serve: listening on http://%s\n", *addr)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("serve: shutting down: %w", err)
		}
		return nil
	}
}
