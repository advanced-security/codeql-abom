package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// A summary that omitted a check that is not running, or why one skipped, would read as health.
func TestCheckReportsEachBuiltCheckAndNamesTheOnesNotRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	srv, asked := fakeGitHub(t, strings.Repeat("c", 40))
	t.Setenv("GITHUB_TOKEN", "test-token")

	var out bytes.Buffer
	if err := run(context.Background(), []string{"check", "--db", path, "--api-url", srv.URL}, &out,
		io.Discard); err != nil {
		t.Fatalf("check: %v\n%s", err, out.String())
	}
	if n := asked.Load(); n != 0 {
		t.Errorf("a database with nothing to sample asked GitHub %d times", n)
	}
	for _, want := range []string{"check 7", "check 8", "check 9", "disagreed unread",
		"the incremental path refused", "not built: check 1 ", "not built: check 10 "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the summary does not say %q:\n%s", want, out.String())
		}
	}

	s, err = store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	latest, err := s.LatestCheckRuns()
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []int{7, 8, 9} {
		if run, ok := latest[check]; !ok || !strings.HasSuffix(run.Build, graph.ReachMarkVersion) {
			t.Errorf("check %d's run is %+v, want one filed under a build naming %s", check, run,
				graph.ReachMarkVersion)
		}
	}
}

func TestCheckRequiresADatabase(t *testing.T) {
	if err := run(context.Background(), []string{"check"}, io.Discard, io.Discard); err == nil {
		t.Error("check without --db succeeded")
	}
}

var (
	fakeRepoLine  = regexp.MustCompile(`(r\d+): repository\(owner: \$(o\d+), name: \$(n\d+)\)`)
	fakeFieldLine = regexp.MustCompile(`(\w+): (?:defaultBranchRef|ref\(qualifiedName: \$\w+\))`)
)

// fakeGitHub answers every reference with one commit, lists main as the only protected branch, and
// counts what it was asked.
func fakeGitHub(t *testing.T, commit string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.URL.Path != "/graphql" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"name":"main","protected":true}]`)
			return
		}
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding the query: %v", err)
		}
		data := []string{`"rateLimit":{"cost":1,"remaining":4990,"limit":5000,"resetAt":"2026-10-07T13:00:00Z"}`}
		for line := range strings.SplitSeq(req.Query, "\n") {
			m := fakeRepoLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			var fields []string
			for _, f := range fakeFieldLine.FindAllStringSubmatch(line[len(m[0]):], -1) {
				fields = append(fields, fmt.Sprintf(`%q:{"target":{"__typename":"Commit","oid":%q}}`, f[1], commit))
			}
			data = append(data, fmt.Sprintf(`%q:{%s}`, m[1], strings.Join(fields, ",")))
		}
		fmt.Fprint(w, `{"data":{`+strings.Join(data, ",")+`}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// checkedDatabase is an owned repository on its default branch and the action it uses, both resolved.
func checkedDatabase(t *testing.T, commit string) string {
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
	if _, err := s.UpsertRepository("octo/app", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefaultBranch("octo/app", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProtectedBranches("octo/app", []string{"main"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		key, owner, repo, ref string
		depth                 int
	}{{"octo/app", "octo", "app", "", 0}, {"vendor/act", "vendor", "act", "v1", 1}} {
		if _, err := s.DB().Exec(`INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status,
			enqueued_at, resolved_sha, analyzed_at) VALUES (?, ?, ?, ?, ?, 'done', '2026-10-01T00:00:00Z', ?,
			'2026-10-01T00:00:00Z')`, row.key, row.ref, row.owner, row.repo, row.depth, commit); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// The checker cannot starve discovery, so everything it spends is drawn from its own pools, and it
// records that spend where every other meter is read.
func TestTheCheckSpendsOnlyItsOwnPoolsAndRecordsWhatItSpent(t *testing.T) {
	commit := strings.Repeat("c", 40)
	path := checkedDatabase(t, commit)
	srv, asked := fakeGitHub(t, commit)
	t.Setenv("GITHUB_TOKEN", "test-token")

	var out bytes.Buffer
	if err := run(context.Background(), []string{"check", "--db", path, "--api-url", srv.URL},
		&out, io.Discard); err != nil {
		t.Fatalf("check: %v\n%s", err, out.String())
	}
	if asked.Load() == 0 {
		t.Fatalf("nothing was asked of GitHub, so what follows is vacuous:\n%s", out.String())
	}
	for _, want := range []string{"check 3", "check 4", "3d external references, sampled at random: compared 1 of 1",
		"4c owned repositories, sampled at random: compared 1 of 1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the summary does not say %q:\n%s", want, out.String())
		}
	}

	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	meters, err := s.PoolMeters(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	units := map[string]bool{}
	for _, m := range meters {
		if m.Pool != work.PoolConsistency {
			t.Errorf("the check spent %+v from %s, which is not the checker's pool", m.Total, m.Pool)
		}
		units[m.Unit] = true
	}
	for _, unit := range []string{work.UnitWorkerSeconds, work.UnitREST, work.UnitGraphQL} {
		if !units[unit] {
			t.Errorf("the check recorded no spend in %s; it recorded %v", unit, units)
		}
	}
}

// A check family that is off says so: zero disagreements from checks that never ran would read as
// health.
func TestTheChecksThatReadGitHubSayTheyAreOffWhenTurnedOff(t *testing.T) {
	path := checkedDatabase(t, strings.Repeat("c", 40))
	var out bytes.Buffer
	if err := run(context.Background(), []string{"check", "--db", path, "--external=false"}, &out,
		io.Discard); err != nil {
		t.Fatalf("check: %v\n%s", err, out.String())
	}
	for _, want := range []string{"off: check 3 ", "off: check 4 "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the summary does not say %q:\n%s", want, out.String())
		}
	}
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	latest, err := s.LatestCheckRuns()
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []int{3, 4} {
		if run, ok := latest[check]; ok {
			t.Errorf("check %d is off and filed a run: %+v", check, run)
		}
	}
}
