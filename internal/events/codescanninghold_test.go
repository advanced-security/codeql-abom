package events

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

const defaultSetupRun = "dynamic/github-code-scanning/codeql"

// defaultSetup records what reading a repository's default setup said, as the setup reader does.
func (f *fixture) defaultSetup(fullName, state string, languages ...string) {
	f.t.Helper()
	if err := f.store.RecordSetupReading(store.SetupReading{Repository: fullName, ReadAt: time.Now(),
		State: state, Languages: languages}); err != nil {
		f.t.Fatalf("RecordSetupReading: %v", err)
	}
}

func (f *fixture) holding(wait time.Duration) *Processor {
	p := f.processor()
	p.CodeScanningWait = wait
	return p
}

func (f *fixture) heldFor(fullName string) string {
	f.t.Helper()
	var commit *string
	if err := f.store.DB().QueryRow(`SELECT awaiting_code_scanning FROM abom_frontier
		WHERE node_key = ? AND ref = ''`, fullName).Scan(&commit); err != nil {
		f.t.Fatalf("reading the hold on %s: %v", fullName, err)
	}
	if commit == nil {
		return ""
	}
	return *commit
}

// A push is analyzed within seconds and code scanning's own analysis of it lands about a minute
// later, so analyzing at once builds the database code scanning is about to supply: the row waits
// for that analysis, under a ceiling, and the delivery says so.
func TestAPushToADefaultSetupRepositoryWaitsForCodeScanning(t *testing.T) {
	f := newFixture(t)
	f.defaultSetup("acme/service", "configured", "actions", "python")
	f.answers["acme/service@"] = shaB
	before := time.Now()

	f.deliver("p-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	drainWith(t, f.holding(10*time.Minute))

	if d := f.settled("p-1", store.EventDone, ReasonRequeued); !strings.Contains(d.Detail, "held for code scanning") {
		t.Errorf("detail %q does not say the row waits for code scanning", d.Detail)
	}
	if got := f.heldFor("acme/service"); got != shaB {
		t.Fatalf("the row is held for %q, want %s", got, shaB)
	}
	row := f.row("acme/service", "")
	ceiling, err := time.Parse(time.RFC3339Nano, row.NextAttemptAt)
	if err != nil || ceiling.Before(before.Add(10*time.Minute)) || ceiling.After(time.Now().Add(10*time.Minute)) {
		t.Errorf("the ceiling is %q (%v), want ten minutes after the push was read", row.NextAttemptAt, err)
	}
	if claimed, err := f.queue.Claim(1); err != nil || len(claimed) != 0 {
		t.Errorf("claimed %+v (%v) while it waits", claimed, err)
	}

	f.deliver("w-1", "workflow_run", workflowRunPayload("acme/service", "completed", defaultSetupRun, shaB))
	drainWith(t, f.holding(10*time.Minute))

	if d := f.delivery("w-1"); !strings.Contains(d.Detail, "released 1") {
		t.Errorf("detail %q does not say what the run released", d.Detail)
	}
	if got := f.heldFor("acme/service"); got != "" {
		t.Errorf("the row is still held for %q after code scanning analyzed it", got)
	}
	claimed, err := f.queue.Claim(1)
	if err != nil || len(claimed) != 1 || claimed[0].NodeKey != "acme/service" {
		t.Errorf("claimed %+v (%v) once released, want acme/service first", claimed, err)
	}
}

// Waiting buys nothing where nothing will arrive: no wait configured, default setup not reading
// Actions, its setup never read, a protected branch, whose analyses code scanning's database does
// not follow, or a commit code scanning already analyzed.
func TestAPushIsAnalyzedAtOnceWhereCodeScanningHasNothingToSupply(t *testing.T) {
	for _, c := range []struct {
		name    string
		wait    time.Duration
		prepare func(f *fixture)
		branch  string
	}{
		{"no wait configured", 0, func(f *fixture) { f.defaultSetup("acme/service", "configured", "actions") }, "main"},
		{"default setup without actions", 10 * time.Minute,
			func(f *fixture) { f.defaultSetup("acme/service", "configured", "python") }, "main"},
		{"default setup not configured", 10 * time.Minute,
			func(f *fixture) { f.defaultSetup("acme/service", "not-configured", "actions") }, "main"},
		{"default setup never read", 10 * time.Minute, func(*fixture) {}, "main"},
		{"already analyzed by code scanning", 10 * time.Minute, func(f *fixture) {
			f.defaultSetup("acme/service", "configured", "actions")
			f.deliver("w-0", "workflow_run", workflowRunPayload("acme/service", "completed", defaultSetupRun, shaB))
		}, "main"},
		{"a protected branch", 10 * time.Minute, func(f *fixture) {
			f.defaultSetup("acme/service", "configured", "actions")
			if _, err := f.store.SetProtectedBranches("acme/service", []string{"main", "release"}); err != nil {
				f.t.Fatal(err)
			}
			f.analyzed("acme/service", "release", 0, shaA)
		}, "release"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			c.prepare(f)
			ref := ""
			if c.branch != "main" {
				ref = c.branch
			}
			f.answers["acme/service@"+ref] = shaB

			f.deliver("p-1", "push", pushPayload("acme/service", "refs/heads/"+c.branch, shaA, shaB))
			drainWith(t, f.holding(c.wait))

			f.settled("p-1", store.EventDone, ReasonRequeued)
			row := f.row("acme/service", ref)
			if row.Status != frontier.StatusPending || row.NextAttemptAt != "" {
				t.Errorf("row = %+v, want pending and claimable now", row)
			}
			if got := f.heldFor("acme/service"); got != "" {
				t.Errorf("the default branch's row is held for %q", got)
			}
		})
	}
}

// Code scanning switched off reports no more analyses, so what waited on it is released rather
// than left to its ceiling.
func TestCodeScanningSwitchedOffReleasesWhatWaitedOnIt(t *testing.T) {
	f := newFixture(t)
	f.defaultSetup("acme/service", "configured", "actions")
	f.answers["acme/service@"] = shaB
	f.deliver("p-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	drainWith(t, f.holding(10*time.Minute))
	if got := f.heldFor("acme/service"); got != shaB {
		t.Fatalf("the row is held for %q, want %s", got, shaB)
	}

	p := f.holding(10 * time.Minute)
	p.Databases = func(_ context.Context, repository, language string) (catalog.Database, error) {
		return catalog.Database{Repository: repository, Language: language, CommitOID: shaA}, nil
	}
	p.Setup = func(context.Context, string) error {
		f.defaultSetup("acme/service", "not-configured", "actions")
		return nil
	}
	f.deliver("s-1", "security_and_analysis", securityAndAnalysisPayload("acme/service"))
	drainWith(t, p)

	if got := f.heldFor("acme/service"); got != "" {
		t.Errorf("the row is still held for %q after code scanning was switched off", got)
	}
	if d := f.delivery("s-1"); !strings.Contains(d.Detail, "released 1") {
		t.Errorf("detail %q does not say what was released", d.Detail)
	}
}
