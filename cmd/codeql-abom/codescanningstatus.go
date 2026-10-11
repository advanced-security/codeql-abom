package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/setup"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// runCodeScanning dispatches the Actions code scanning commands.
func runCodeScanning(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("code-scanning: name a command: " + codeScanningCommands)
	}
	switch args[0] {
	case "status":
		return runCodeScanningStatus(args[1:], stdout, stderr)
	case "preview", "approve", "batch", "batches", "pause", "resume", "stop", "ledger":
		return runCodeScanningWrite(ctx, args[0], args[1:], stdout, stderr)
	}
	return fmt.Errorf("code-scanning: unknown command %q; the commands are %s", args[0], codeScanningCommands)
}

const codeScanningCommands = "status, preview, approve, batch, batches, pause, resume, stop, ledger"

// runCodeScanningWrite drives the server's own endpoints, so nothing is done from here that the
// interface could not do and see.
func runCodeScanningWrite(ctx context.Context, command string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("code-scanning "+command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("server", "http://127.0.0.1:8099", "the running ABOM server")
	view := fs.String("view", "", "preview: the named view whose repositories to preview (default: every declared one)")
	exclude := fs.String("exclude", "", "preview: comma-separated repositories to leave out")
	preview := fs.Int64("preview", 0, "approve, batch: the preview")
	batch := fs.Int64("batch", 0, "pause, resume, stop, ledger: the batch")
	repository := fs.String("repository", "", "ledger: one repository's steps")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var method, path string
	var body any
	switch command {
	case "preview":
		method, path = http.MethodPost, "/api/code-scanning/previews"
		body = map[string]any{"view": *view, "exclude": splitTargets(*exclude)}
	case "approve":
		method, path = http.MethodPost, fmt.Sprintf("/api/code-scanning/previews/%d/approve", *preview)
	case "batch":
		method, path, body = http.MethodPost, "/api/code-scanning/batches", map[string]any{"preview": *preview}
	case "batches":
		method, path = http.MethodGet, "/api/code-scanning/batches"
	case "pause", "resume", "stop":
		method, path = http.MethodPost, fmt.Sprintf("/api/code-scanning/batches/%d/%s", *batch, command)
	case "ledger":
		q := url.Values{}
		if *repository != "" {
			q.Set("repository", *repository)
		}
		if *batch != 0 {
			q.Set("batch", strconv.FormatInt(*batch, 10))
		}
		method, path = http.MethodGet, "/api/code-scanning/ledger"
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
	}
	base := strings.TrimSuffix(*server, "/")
	answer, err := callServer(ctx, method, base+path, body)
	if err != nil {
		return fmt.Errorf("code-scanning %s: %w", command, err)
	}
	if err := printCodeScanning(ctx, stdout, base, command, answer); err != nil {
		return fmt.Errorf("code-scanning %s: %w", command, err)
	}
	return nil
}

// previewRowsShown is how many of a preview's rows a command prints: the ones a batch would write
// come first, so the page holds what an approver decides on.
const previewRowsShown = 50

func callServer(ctx context.Context, method, target string, body any) ([]byte, error) {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		return nil, err
	}
	if method != http.MethodGet {
		req.Header.Set("X-Abom-Control", "1")
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		var refusal struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(answer, &refusal) == nil && refusal.Error != "" {
			return nil, errors.New(refusal.Error)
		}
		return nil, fmt.Errorf("the server answered %d", resp.StatusCode)
	}
	return answer, nil
}

func printCodeScanning(ctx context.Context, stdout io.Writer, base, command string, answer []byte) error {
	switch command {
	case "preview", "approve":
		var p store.SetupPreview
		if err := json.Unmarshal(answer, &p); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "preview %d of %s", p.ID, orAll(p.ViewName))
		if p.ApprovedBy != "" {
			fmt.Fprintf(stdout, ", approved by %s", p.ApprovedBy)
		}
		fmt.Fprintln(stdout, ".")
		for _, c := range append(slices.Clone(store.SetupClassOrder), setup.PreviewExcluded, setup.PreviewNotATarget, setup.PreviewHeld) {
			if n := p.Counts[c]; n > 0 {
				fmt.Fprintf(stdout, "  %s: %d\n", c, n)
			}
		}
		raw, err := callServer(ctx, http.MethodGet,
			fmt.Sprintf("%s/api/code-scanning/previews/%d/rows?top=%d", base, p.ID, previewRowsShown), nil)
		if err != nil {
			return err
		}
		var rows store.SetupPreviewRows
		if err := json.Unmarshal(raw, &rows); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%d %s, those a batch would write first:\n", rows.Total, plural(rows.Total, "row", "rows"))
		for _, r := range rows.Items {
			fmt.Fprintf(stdout, "%-18s %s — %s %s\n", r.Class, r.Repository, r.Evidence, r.Intended)
		}
		if more := rows.Total - len(rows.Items); more > 0 {
			fmt.Fprintf(stdout, "... and %d more, from %s/api/code-scanning/previews/%d/rows?offset=%d\n",
				more, base, p.ID, len(rows.Items))
		}
	case "ledger":
		var page store.SetupLedgerPage
		if err := json.Unmarshal(answer, &page); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%d %s, newest first:\n", page.Total, plural(page.Total, "step", "steps"))
		for _, w := range page.Items {
			fmt.Fprintf(stdout, "%s %s %s %d %s\n", w.At.Format(time.RFC3339), w.Repository, w.Step, w.Status, w.Detail)
		}
	default:
		_, err := stdout.Write(append(answer, '\n'))
		return err
	}
	return nil
}

func orAll(view string) string {
	if view == "" {
		return "every declared repository"
	}
	return "the view " + view
}

// runCodeScanningStatus prints every repository under review in its Actions code scanning class,
// the same classification /api/code-scanning/readings serves.
func runCodeScanningStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("code-scanning status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := databaseFlag(fs)
	class := fs.String("class", "", "list only this class")
	asJSON := fs.Bool("json", false, "print the readings as JSON, as the API answers them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("code-scanning status: --db is required")
	}
	if *class != "" && !slices.Contains(store.SetupClassOrder, *class) {
		return fmt.Errorf("code-scanning status: class %q is not one of %v", *class, store.SetupClassOrder)
	}
	s, err := store.OpenBackend(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", store.DatabaseLabel(*dbPath), err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", store.DatabaseLabel(*dbPath), err)
	}
	readings, err := setup.Classify(s, *class)
	if err != nil {
		return fmt.Errorf("code-scanning status: %w", err)
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(readings)
	}

	// What was read before what it found, so "nothing to add" is never mistaken for "nothing read".
	fmt.Fprintf(stdout, "%d declared repositories, %d read", readings.Declared, readings.Read)
	if readings.OldestRead != "" {
		fmt.Fprintf(stdout, ", the oldest reading from %s", readings.OldestRead)
	}
	fmt.Fprintln(stdout, ".")
	for _, c := range store.SetupClassOrder {
		if n := readings.Counts[c]; n > 0 {
			fmt.Fprintf(stdout, "  %s: %d\n", c, n)
		}
	}
	fmt.Fprintln(stdout)
	for _, item := range readings.Items {
		fmt.Fprintf(stdout, "%-14s %s — %s\n", item.Class, item.Repository, item.Evidence)
	}
	return nil
}

// codeScanningWrites is the default setup write service. Reads stay the App's; the one write is made
// with the operator's own token, and only to the repositories and accounts the flags name.
func codeScanningWrites(ctx context.Context, cfg serveConfig, s store.Backend, reader setup.Reader) *setup.Service {
	sv := &setup.Service{Store: s, Targets: splitTargets(cfg.enableTargets), Accounts: splitTargets(cfg.enableAccounts)}
	if len(sv.Targets)+len(sv.Accounts) > 0 {
		patcher := &setup.Patcher{BaseURL: cfg.apiURL, Token: githubToken(ctx), Allowed: sv.Allowed()}
		sv.Login = patcher.Login
		sv.Runner = &setup.Runner{Store: s, Reader: reader, Patch: patcher,
			Pace:     processPools.Pool(work.PoolDefaultSetup, work.UnitREST).Take,
			InFlight: setupRunsInFlight, VerifyAfter: 2 * time.Minute, VerifyFor: 2 * time.Hour, BreakAfter: 3}
	}
	_ = sv.Start(ctx)
	return sv
}

// setupRunsInFlight bounds the setup runs outstanding at once: each one is a workflow on the
// operator's runners, and an organization's runners are shared with everything else it builds.
const setupRunsInFlight = 10

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
