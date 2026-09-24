package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/harness"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// recordingFile holds what CodeQL did over the corpus query directory. Beside the source trees
// rather than among the fixtures, every one of which is read as a repository.
const recordingFile = "authoring.json"

// seededBy is who the corpus says asked for its queries, started the session and reviewed.
const seededBy = "operator"

// What happens to a query, in order. A layout writes the same files when recording and when
// building, which is what lets a recorded run describe the files the corpus holds.
const (
	layStub    = "stub"    // as ABOM scaffolds it, with the fixture: a query that reports nothing
	layWritten = "written" // the query as written
	layAccept  = "accept"  // the last run's report accepted as the .expected
	layEmpty   = "empty"   // an .expected written with nothing in it
	doRun      = "run"     // its tests run
	doPrint    = "print"   // PrintAST over its test database
	doFinish   = "finish"  // the agent working it stops, before anybody accepts its report
)

// corpusQuery is one query in the directory, and the request that asked for it. Between them
// the queries leave a mark in every state: nothing shown, passing, a warning and a failure.
type corpusQuery struct {
	name, id                             string
	description, compliant, noncompliant string
	// agent is why the agent chose this query, empty where the operator did.
	agent string
	// help is written beside the query, and is empty where nobody wrote any.
	help string
	// fixture is the workflow its test runs over, with `# $ Alert` on each line it must report.
	fixture string
	// written replaces the stub at layWritten.
	written string
	steps   []string
	// session is whether an agent worked the request, which only one query's does.
	session bool
}

var corpusQueries = []corpusQuery{
	{
		name: "CheckoutTagPin", id: "acme/checkout-tag-pin",
		description:  "Report actions/checkout pinned to a tag, which its owner can move, rather than to a commit.",
		compliant:    "- uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2",
		noncompliant: "- uses: actions/checkout@v4",
		help: "# Checkout pinned to a tag\n\nA tag can be moved to other code by whoever controls " +
			"the repository. Pin `actions/checkout` to the full commit SHA of a release instead.\n",
		fixture: "on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n" +
			"      - uses: actions/checkout@v4 # $ Alert\n" +
			"      - uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2\n",
		written: `/**
 * @name Checkout pinned to a tag
 * @description actions/checkout referenced by a tag or branch, which can be moved, rather than
 *              by a commit.
 * @kind problem
 * @problem.severity warning
 * @id acme/checkout-tag-pin
 */

import actions

from UsesStep step
where
  step.getCallee() = "actions/checkout" and
  not step.getVersion().regexpMatch("[0-9a-f]{40}")
select step.getCalleeNode(), "actions/checkout is pinned to " + step.getVersion() + ", which can be moved."
`,
		steps:   []string{layStub, doRun, layWritten, doRun, doPrint, doFinish, layAccept, doRun},
		session: true,
	},
	{
		name: "WritePermissions", id: "acme/write-all-permissions",
		description:  "Report a workflow that grants its token write-all.",
		compliant:    "permissions:\n  contents: read",
		noncompliant: "permissions: write-all",
		help:         "# Workflow grants write-all\n\nA token that can write everything turns any step it reaches into a way to change the repository.\n",
		fixture: "on: pull_request_target\npermissions: write-all # $ Alert\njobs:\n  build:\n" +
			"    runs-on: ubuntu-latest\n    steps:\n      - run: echo building\n",
		// Its stub's report accepted by hand, which is the mistake the expected mark exists for.
		steps: []string{layStub, doRun, layAccept},
	},
	{
		name: "UnpinnedSetup", id: "acme/unpinned-setup-action",
		description:  "Report a setup action pinned to a major version tag.",
		compliant:    "- uses: actions/setup-node@39370e3970a6d050c480ffad4ff0ed4d3fdee5af # v4.1.0",
		noncompliant: "- uses: actions/setup-node@v4",
		agent:        "the fixtures pin setup-node, which no other query in the pack looks at",
		help:         "# Setup action pinned to a tag\n\nPin setup actions to a commit, as with any other action.\n",
		fixture: "on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n" +
			"      - uses: actions/setup-node@v4 # $ Alert\n",
		steps: []string{layStub, layEmpty},
	},
	{
		name: "SelfHostedRunner", id: "acme/self-hosted-runner",
		description:  "Report a job that runs on a self-hosted runner and is reached from a pull request.",
		compliant:    "runs-on: ubuntu-latest",
		noncompliant: "runs-on: self-hosted",
		fixture: "on: pull_request\njobs:\n  build:\n    runs-on: self-hosted # $ Alert\n    steps:\n" +
			"      - run: make\n",
		steps: []string{layStub},
	},
}

// untargeted is a request no query answers yet, so the agent decides which one it is about.
var untargeted = store.QueryRequest{
	Description: "Report a job that checks out a pull request's head and then runs a script from it.",
	Compliant:   "- uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2",
	NonCompliant: "- uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2\n" +
		"  with:\n    ref: ${{ github.event.pull_request.head.sha }}\n- run: ./build.sh",
	Kind: store.RequestMissed, RequestedBy: seededBy,
}

// finished is what the seeded session's agent said when it stopped.
const finished = "acme/checkout-tag-pin reports the tag-pinned checkout its fixture marks and leaves " +
	"the SHA-pinned one alone; its report is ready for review, and accepting it is the operator's call."

// recording is what CodeQL did over the corpus query directory, recorded by --record-queries so
// that building it needs no CLI.
type recording struct {
	// CLIVersion is the CodeQL that ran every test and printed the tree.
	CLIVersion string        `json:"cliVersion"`
	Runs       []recordedRun `json:"runs"`
	Tree       recordedTree  `json:"tree"`
}

// recordedRun is one test run, with the files it ran over so a recording of others is refused.
type recordedRun struct {
	Query    string            `json:"query"`
	Version  string            `json:"version"`
	Tests    string            `json:"tests"`
	Fixtures string            `json:"fixtures"`
	Result   runner.TestResult `json:"result"`
	// Report is the .actual the run left, which accepting it makes the .expected.
	Report string `json:"report"`
}

type recordedTree struct {
	Query       string `json:"query"`
	Database    string `json:"database"`
	SourceFiles string `json:"sourceFiles"`
	Fixtures    string `json:"fixtures"`
	Body        string `json:"body"`
	Truncated   bool   `json:"truncated"`
}

// author is what running and printing do: CodeQL when recording, the recording when building.
type author interface {
	begin(q corpusQuery, dir string, request int64) error
	lay(q corpusQuery, dir, step string) error
	run(ctx context.Context, q authoring.Query, dir string) error
	report(q corpusQuery) (string, error)
	print(ctx context.Context, q authoring.Query, dir string) error
	finish(q corpusQuery) error
}

// walk takes every corpus query through its steps in dir, a scaffolded pack.
func walk(ctx context.Context, dir string, a author) error {
	for i, q := range corpusQueries {
		request := int64(i + 1)
		for _, step := range q.steps {
			if err := walkStep(ctx, dir, q, request, step, a); err != nil {
				return fmt.Errorf("%s at %s: %w", q.id, step, err)
			}
		}
	}
	return nil
}

func walkStep(ctx context.Context, dir string, q corpusQuery, request int64, step string, a author) error {
	expected := filepath.Join(dir, "test", q.name, q.name+".expected")
	switch step {
	case layStub:
		tests, err := scaffoldCorpusQuery(dir, q, request)
		if err != nil {
			return err
		}
		if err := put(filepath.Join(tests, ".github", "workflows", "ci.yml"), q.fixture); err != nil {
			return err
		}
		if err := a.begin(q, dir, request); err != nil {
			return err
		}
	case layWritten:
		if err := put(filepath.Join(dir, "src", q.name+".ql"), q.written); err != nil {
			return err
		}
	case layAccept:
		report, err := a.report(q)
		if err != nil {
			return err
		}
		if err := put(expected, report); err != nil {
			return err
		}
	case layEmpty:
		if err := put(expected, ""); err != nil {
			return err
		}
	case doRun, doPrint:
		found, ok, err := authoring.Find(dir, q.id)
		switch {
		case err != nil:
			return err
		case !ok:
			return errors.New("not in the query directory")
		case step == doRun:
			return a.run(ctx, found, dir)
		}
		return a.print(ctx, found, dir)
	case doFinish:
		return a.finish(q)
	default:
		return fmt.Errorf("no step %q", step)
	}
	return a.lay(q, dir, step)
}

func scaffoldCorpusQuery(dir string, q corpusQuery, request int64) (string, error) {
	_, tests, err := customql.ScaffoldQuery(dir, q.name, q.id, request)
	if err != nil {
		return "", err
	}
	if q.help != "" {
		if err := put(filepath.Join(dir, "src", q.name+".md"), q.help); err != nil {
			return "", err
		}
	}
	return tests, nil
}

func put(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

// recorder runs CodeQL and keeps what it said.
type recorder struct {
	cli *runner.Runner
	out recording
}

func (r *recorder) begin(corpusQuery, string, int64) error { return nil }
func (r *recorder) lay(corpusQuery, string, string) error  { return nil }
func (r *recorder) finish(corpusQuery) error               { return nil }
func (r *recorder) report(q corpusQuery) (string, error)   { return lastReport(r.out, q.name) }
func (r *recorder) print(ctx context.Context, q authoring.Query, dir string) error {
	database, fixtures, why, err := authoring.Printable(dir, q)
	switch {
	case err != nil:
		return err
	case why != "":
		return errors.New(why)
	}
	digest, err := authoring.DigestFixtures(dir, q.Tests)
	if err != nil {
		return err
	}
	tree, err := r.cli.PrintAST(ctx, database, fixtures)
	if err != nil {
		return err
	}
	rel := func(p string) string {
		out, err := filepath.Rel(dir, p)
		if err != nil {
			return p
		}
		return filepath.ToSlash(out)
	}
	files := make([]string, 0, len(fixtures))
	for _, f := range fixtures {
		files = append(files, rel(f))
	}
	// The tree names each file by its absolute path, which is this machine's scratch directory.
	body := tree.Body
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		body = strings.ReplaceAll(body, real+string(filepath.Separator), "")
	}
	body = strings.ReplaceAll(body, dir+string(filepath.Separator), "")
	r.out.Tree = recordedTree{Query: q.ID, Database: rel(database), SourceFiles: strings.Join(files, ","),
		Fixtures: digest, Body: body, Truncated: tree.Truncated}
	return nil
}

func (r *recorder) run(ctx context.Context, q authoring.Query, dir string) error {
	seen, err := filesOf(dir, q)
	if err != nil {
		return err
	}
	seen.Result, err = r.cli.RunTests(ctx, q.Tests)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "%-20s %s\n", seen.Query, seen.Result.Detail)
	for _, test := range q.Tests {
		body, err := os.ReadFile(strings.TrimSuffix(test, ".qlref") + ".actual")
		if err == nil {
			seen.Report = string(body)
		}
	}
	r.out.Runs = append(r.out.Runs, seen)
	return nil
}

// filesOf is what a run would say it ran over.
func filesOf(dir string, q authoring.Query) (recordedRun, error) {
	tests, err := authoring.DigestTests(dir, q.Tests)
	if err != nil {
		return recordedRun{}, err
	}
	fixtures, err := authoring.DigestFixtures(dir, q.Tests)
	if err != nil {
		return recordedRun{}, err
	}
	return recordedRun{Query: filepath.Base(strings.TrimSuffix(q.Path, ".ql")), Version: q.Version,
		Tests: tests, Fixtures: fixtures}, nil
}

func lastReport(r recording, name string) (string, error) {
	for i := len(r.Runs) - 1; i >= 0; i-- {
		if r.Runs[i].Query == name {
			return r.Runs[i].Report, nil
		}
	}
	return "", fmt.Errorf("nothing ran %s before its report was accepted", name)
}

// recordQueries runs the corpus query directory's tests and prints its tree with the CLI, and
// writes what CodeQL said to path.
func recordQueries(path string) error {
	dir, err := os.MkdirTemp("", "abom-corpus-queries-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	runner.SetCustomPackRoot(dir)
	ctx := context.Background()
	r := &recorder{cli: &runner.Runner{}}
	if r.out.CLIVersion, err = r.cli.Version(ctx); err != nil {
		return err
	}
	if _, err := customql.Scaffold(dir, r.out.CLIVersion); err != nil {
		return err
	}
	// A scaffolded pack names its dependencies and locks none, and nothing compiles until they are.
	for _, args := range [][]string{
		{"pack", "install", dir},
		{"pack", "install", filepath.Join(dir, "test"), "--additional-packs=" + dir},
	} {
		if out, err := exec.CommandContext(ctx, "codeql", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("codeql %s: %w: %s", strings.Join(args[:2], " "), err, out)
		}
	}
	if err := walk(ctx, dir, r); err != nil {
		return err
	}
	// Refused rather than written: a recording of a query that does not report what its fixture
	// marks would seed a passing mark the query never earned.
	for _, run := range r.out.Runs {
		if run.Query == corpusQueries[0].name && run.Result.Passed {
			body, err := json.MarshalIndent(r.out, "", "  ")
			if err != nil {
				return err
			}
			return os.WriteFile(path, append(body, '\n'), 0o644)
		}
	}
	return fmt.Errorf("%s never passed its own tests, so nothing was recorded", corpusQueries[0].id)
}

// replayer builds from a recording, through the same functions the server records with.
type replayer struct {
	st       *store.Store
	rec      recording
	next     int
	requests map[string]int64
	session  int64
	dir      string
	bounds   harness.Bounds
}

func (p *replayer) report(q corpusQuery) (string, error) {
	done := recording{Runs: p.rec.Runs[:p.next]}
	return lastReport(done, q.name)
}

func (p *replayer) run(ctx context.Context, q authoring.Query, dir string) error {
	if p.next >= len(p.rec.Runs) {
		return errors.New("the recording holds no run for this; run scripts/regenerate-corpus-fixtures.sh")
	}
	want := p.rec.Runs[p.next]
	seen, err := filesOf(dir, q)
	if err != nil {
		return err
	}
	if seen.Query != want.Query || seen.Version != want.Version || seen.Tests != want.Tests ||
		seen.Fixtures != want.Fixtures {
		return fmt.Errorf("the recording ran %s over other files than the corpus lays out; run "+
			"scripts/regenerate-corpus-fixtures.sh", want.Query)
	}
	p.next++
	if p.session != 0 {
		if err := p.call(harness.ToolTest, nil, "", harness.DecisionSucceeded, want.Result.Detail); err != nil {
			return err
		}
	}
	replay := func(context.Context, []string) (runner.TestResult, error) { return want.Result, nil }
	_, err = authoring.RecordTests(ctx, p.st, replay, dir, q, p.rec.CLIVersion)
	return err
}

func (p *replayer) print(_ context.Context, q authoring.Query, dir string) error {
	tree := p.rec.Tree
	digest, err := authoring.DigestFixtures(dir, q.Tests)
	switch {
	case err != nil:
		return err
	case tree.Query != q.ID || tree.Fixtures != digest:
		return errors.New("the recording printed other fixtures than the corpus lays out; run " +
			"scripts/regenerate-corpus-fixtures.sh")
	case p.session == 0:
		return errors.New("a tree in the corpus is kept by the session that printed it")
	}
	id, err := p.st.RecordAuthoringAST(store.AuthoringAST{
		SessionID: p.session, QueryID: q.ID, PrintedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Query: "PrintAST", Database: tree.Database, SourceFiles: tree.SourceFiles, TestDigest: digest,
		Body: tree.Body, Truncated: tree.Truncated, CLIVersion: p.rec.CLIVersion,
	})
	if err != nil {
		return err
	}
	_, err = p.st.RecordAuthoringEvent(store.AuthoringEvent{SessionID: p.session, At: now(),
		Hook: harness.HookArtifact, Tool: harness.Server + "-codeql_query_run", Subject: tree.Database,
		Decision: harness.DecisionNoted, Step: harness.StepAST,
		Detail: fmt.Sprintf("kept %d bytes of what PrintAST printed, as printout %d", len(tree.Body), id)})
	return err
}

// begin starts the session that works a request, whose first calls are the gate's to decide.
func (p *replayer) begin(q corpusQuery, dir string, request int64) error {
	p.requests[q.name] = request
	if !q.session {
		return nil
	}
	id, err := p.st.StartAuthoringSession(request, seededBy, harness.DefaultModel)
	if err != nil {
		return err
	}
	bounds, err := harness.NewBounds(dir, nil)
	if err != nil {
		return err
	}
	tests := filepath.Join(dir, "test", q.name)
	if p.bounds, err = bounds.Bind(filepath.Join(dir, "src", q.name+".ql"), tests); err != nil {
		return err
	}
	p.session, p.dir = id, dir
	if err := p.call("bash", map[string]any{"command": "rm -rf ~"}, "rm -rf ~", "", ""); err != nil {
		return err
	}
	fixture := filepath.Join(tests, ".github", "workflows", "ci.yml")
	return p.call("create", map[string]any{"path": fixture}, p.rel(fixture), harness.DecisionSucceeded, "")
}

func (p *replayer) lay(q corpusQuery, dir, step string) error {
	switch {
	case step == layWritten && p.session != 0:
		query := filepath.Join(dir, "src", q.name+".ql")
		return p.call("edit", map[string]any{"path": query}, p.rel(query), harness.DecisionSucceeded, "")
	case step == layAccept && q.session:
		return p.accepted(q, dir)
	}
	return nil
}

// accepted records the operator accepting the report the session left, as the review does before
// it writes the .expected and runs the tests itself.
func (p *replayer) accepted(q corpusQuery, dir string) error {
	found, _, err := authoring.Find(dir, q.id)
	if err != nil {
		return err
	}
	fixtures, err := authoring.DigestFixtures(dir, found.Tests)
	if err != nil {
		return err
	}
	report, err := p.report(q)
	if err != nil {
		return err
	}
	_, err = p.st.RecordAuthoringReview(store.AuthoringReview{RequestID: p.requests[q.name], QueryID: q.id,
		QueryVersion: found.Version, TestDigest: fixtures, Actual: report, Verdict: store.ReviewApproved,
		ReviewedBy: seededBy})
	return err
}

// finish ends the session as its agent did, and what follows is the operator's.
func (p *replayer) finish(q corpusQuery) error {
	if p.session == 0 {
		return nil
	}
	if _, err := p.st.RecordAuthoringEvent(store.AuthoringEvent{SessionID: p.session, At: now(),
		Hook: harness.HookMessage, Tool: "agent", Decision: harness.DecisionNoted, Detail: finished}); err != nil {
		return err
	}
	if err := p.st.EndAuthoringSession(p.session, store.SessionFinished, finished); err != nil {
		return err
	}
	p.session = 0
	return nil
}

// call records what the gate decides about a call before it runs, and, where it was let through,
// how it ended.
func (p *replayer) call(tool string, args map[string]any, subject, outcome, detail string) error {
	verdict := p.bounds.Call(tool, args)
	decision := harness.DecisionAllowed
	if !verdict.Allowed {
		decision = harness.DecisionRefused
	}
	if _, err := p.st.RecordAuthoringEvent(store.AuthoringEvent{SessionID: p.session, At: now(),
		Hook: harness.HookPreToolUse, Tool: tool, Subject: subject, Decision: decision,
		Step: verdict.Step, Reason: verdict.Reason}); err != nil {
		return err
	}
	if !verdict.Allowed || outcome == "" {
		return nil
	}
	_, err := p.st.RecordAuthoringEvent(store.AuthoringEvent{SessionID: p.session, At: now(),
		Hook: harness.HookPostToolUse, Tool: tool, Subject: subject, Decision: outcome,
		Step: verdict.Step, Detail: detail})
	return err
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// rel names a path in the query directory the way the timeline does.
func (p *replayer) rel(path string) string {
	out, err := filepath.Rel(p.dir, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(out)
}

// corpusMarker is left in a directory the corpus wrote, and is what lets a rebuild replace it.
const corpusMarker = ".abom-corpus"

// clearQueries empties dir for a rebuild, refusing one the corpus did not write: it is replaced
// wholesale, and pointed at an operator's own pack that would delete their queries.
func clearQueries(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return err
	case len(entries) == 0:
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, corpusMarker)); err != nil {
		return fmt.Errorf("refusing to replace %q, which the corpus did not write", dir)
	}
	return os.RemoveAll(dir)
}

// seedQueries lays the corpus query directory out in dir from the recording beside the fixtures,
// with the requests that asked for its queries, the session that worked one and what it printed.
func seedQueries(s *store.Store, fixtures, dir string) error {
	// Replaced rather than merged, as the database is: files left from another build would be
	// read as the operator's.
	if err := clearQueries(dir); err != nil {
		return err
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(fixtures), recordingFile))
	if err != nil {
		return fmt.Errorf("reading the query recording: %w; run scripts/regenerate-corpus-fixtures.sh", err)
	}
	var rec recording
	if err := json.Unmarshal(body, &rec); err != nil {
		return fmt.Errorf("reading the query recording: %w", err)
	}
	if err := put(filepath.Join(dir, corpusMarker), "written by cmd/abom-corpus; a rebuild replaces this directory\n"); err != nil {
		return err
	}
	for i, q := range corpusQueries {
		r := store.QueryRequest{Description: q.description, Compliant: q.compliant,
			NonCompliant: q.noncompliant, Kind: store.RequestMissed, RequestedBy: seededBy}
		if q.agent == "" {
			r.QueryID, r.TargetSource = q.id, store.TargetOperator
		}
		id, err := s.SaveQueryRequest(r)
		if err != nil {
			return err
		}
		// The stub each query is scaffolded with names its request, and the recording holds it.
		if id != int64(i+1) {
			return fmt.Errorf("request for %s is #%d, and the recording's stub names #%d", q.id, id, i+1)
		}
		if q.agent != "" {
			if err := s.DecideTarget(id, q.id, q.agent); err != nil {
				return err
			}
		}
	}
	if _, err := s.SaveQueryRequest(untargeted); err != nil {
		return err
	}
	p := &replayer{st: s, rec: rec, requests: map[string]int64{}}
	if _, err := customql.Scaffold(dir, rec.CLIVersion); err != nil {
		return err
	}
	if err := walk(context.Background(), dir, p); err != nil {
		return err
	}
	if p.next != len(rec.Runs) {
		return fmt.Errorf("the recording holds %d runs the corpus never makes; run "+
			"scripts/regenerate-corpus-fixtures.sh", len(rec.Runs)-p.next)
	}
	return nil
}
