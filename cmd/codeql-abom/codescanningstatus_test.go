package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/setup"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func codeScanningDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "octo", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"octo/covered", "octo/unread"} {
		if _, err := s.InsertOwnedRepository(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordSetupReading(store.SetupReading{Repository: "octo/covered",
		ReadAt: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), State: "configured",
		Languages: []string{"actions", "python"}}); err != nil {
		t.Fatal(err)
	}
	return path
}

// The command reads the classification the interface reads, so the two cannot disagree.
func TestCodeScanningStatusPrintsWhatWasReadBeforeWhatItFound(t *testing.T) {
	db := codeScanningDB(t)
	var out, errs bytes.Buffer
	if err := run(context.Background(), []string{"code-scanning", "status", "--db", db}, &out, &errs); err != nil {
		t.Fatalf("code-scanning status: %v (%s)", err, errs.String())
	}
	text := out.String()
	for _, want := range []string{"2 declared repositories, 1 read", "covered: 1", "not-read: 1",
		"octo/covered", "default setup analyzes actions, python"} {
		if !strings.Contains(text, want) {
			t.Errorf("output does not say %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "declared repositories") > strings.Index(text, "octo/covered") {
		t.Errorf("the coverage line must come before any repository:\n%s", text)
	}
}

func TestCodeScanningStatusAnswersJSONForOneClass(t *testing.T) {
	db := codeScanningDB(t)
	var out, errs bytes.Buffer
	if err := run(context.Background(), []string{"code-scanning", "status", "--db", db, "--class", "not-read", "--json"},
		&out, &errs); err != nil {
		t.Fatalf("code-scanning status: %v (%s)", err, errs.String())
	}
	var got struct {
		Items []struct {
			Repository string `json:"repository"`
		} `json:"items"`
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding %s: %v", out.String(), err)
	}
	if len(got.Items) != 1 || got.Items[0].Repository != "octo/unread" || got.Counts["covered"] != 1 {
		t.Errorf("--class not-read answered %+v", got)
	}
	if err := run(context.Background(), []string{"code-scanning", "status", "--db", db, "--class", "sideways"},
		&out, &errs); err == nil {
		t.Error("an unknown class must be refused, not answered with nothing")
	}
}

// The write commands drive the server's own endpoints, so they are tested against its handler: a fake
// answering by hand kept passing after the server began paging a preview's rows and the ledger.
func TestTheWriteCommandsReadWhatTheServerSends(t *testing.T) {
	path := codeScanningDB(t)
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	yes, no := true, false
	if err := s.RecordListedFacts([]store.ListedFacts{{Repository: "octo/unread", HasWorkflows: &yes,
		HasActionsDir: &no, HasRootAction: &no}}, at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO abom_frontier (node_key, ref, depth, status, enqueued_at)
		VALUES ('octo/unread', '', 0, 'done', '2026-10-06T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSetupReading(store.SetupReading{Repository: "octo/unread", ReadAt: at,
		State: "not-configured", Languages: []string{"actions"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSecurityConfigurations("octo", at, nil, nil); err != nil {
		t.Fatal(err)
	}
	srv := &serve.Server{Store: s, CodeScanningWrites: &setup.Service{Store: s, Targets: []string{"octo/unread"},
		Runner: &setup.Runner{Store: s}, Login: func(context.Context) (string, error) { return "data-douser", nil }}}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var out, errs bytes.Buffer
	cli := func(args ...string) string {
		t.Helper()
		out.Reset()
		if err := run(context.Background(), append([]string{"code-scanning"}, args...), &out, &errs); err != nil {
			t.Fatalf("%v: %v (%s)", args, err, errs.String())
		}
		return out.String()
	}
	preview := cli("preview", "--server", ts.URL)
	for _, want := range []string{"preview 1", "enable", "octo/unread", `"languages":["actions"]`, "2 rows"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview does not say %q:\n%s", want, preview)
		}
	}
	if approved := cli("approve", "--server", ts.URL, "--preview", "1"); !strings.Contains(approved, "approved by data-douser") ||
		!strings.Contains(approved, "octo/unread") {
		t.Errorf("approve says:\n%s", approved)
	}
	batch, err := s.StartSetupBatch(1, "test", at)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendSetupWrite(store.SetupWrite{BatchID: batch, Repository: "octo/unread", At: at,
		Step: store.SetupStepSent, Status: 202, Before: []string{}}); err != nil {
		t.Fatal(err)
	}
	if ledger := cli("ledger", "--server", ts.URL, "--batch", "1"); !strings.Contains(ledger, "octo/unread sent 202") ||
		!strings.Contains(ledger, "1 step") {
		t.Errorf("ledger says:\n%s", ledger)
	}
}
