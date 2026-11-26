package expand

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// RemoteQuery is one query a remote analysis runs, as the coordinator's plan resolved it. Paths are
// the image's own, which coordinator and worker share.
type RemoteQuery struct {
	Path  string `json:"path"`
	ID    string `json:"id"`
	Edges bool   `json:"edges,omitempty"`
}

// RemoteTask is one repository at one reference, analyzed somewhere that holds no store.
type RemoteTask struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Ref   string `json:"ref,omitempty"`
	// Queries are the BQRS lanes, Findings the findings lane's query paths.
	Queries  []RemoteQuery `json:"queries"`
	Findings []string      `json:"findings,omitempty"`
	// PackFindings are findings queries in the custom pack, by their path inside it: the pack
	// travels with the grant, since the coordinator's path to it means nothing in a worker.
	PackFindings []string `json:"packFindings,omitempty"`
	// Admitted is why the coordinator's source policy let this repository be read. A worker holds no
	// store to ask, so it refuses a task without one rather than fetching on nobody's say.
	Admitted string `json:"admitted"`
}

// errNoAdmission is a task some path built without asking the source policy.
var errNoAdmission = errors.New("the task names no admission: only the coordinator's source policy says what may be fetched")

// RemoteOutput is what a remote analysis produced, materialized under one local directory.
type RemoteOutput struct {
	Commit string `json:"commit"`
	Ref    string `json:"ref"`
	// Digest is what the extractor read, before it is scoped to the source filter.
	Digest string `json:"digest"`
	// LockTree holds the checkout's .github/workflows, which is all a lock survey reads.
	LockTree      string            `json:"-"`
	ResultPath    string            `json:"-"`
	ResultQueryID string            `json:"-"`
	Extra         map[string]string `json:"-"`
	SarifPath     string            `json:"-"`
}

// RemoteRunner analyzes a task elsewhere and brings its output back under dir.
type RemoteRunner interface {
	Run(ctx context.Context, t RemoteTask, dir string) (RemoteOutput, error)
}

// AnalyzeTask is a worker's half of a remote analysis: fetch, read what the extractor would, build
// the database and run the task's queries, all under dir. It reads and writes no store; whether any
// of it was already on record is the coordinator's to decide.
func (e *Expander) AnalyzeTask(ctx context.Context, t RemoteTask, dir string) (RemoteOutput, error) {
	if t.Admitted == "" {
		return RemoteOutput{}, fmt.Errorf("%w: %w", ErrUnavailable, errNoAdmission)
	}
	src := filepath.Join(dir, "src")
	// By reference, as a local round fetches; the commit it reports is the one analyzed.
	tree, err := e.fetch(ctx, batch{owner: t.Owner, repo: t.Repo, ref: t.Ref}, src)
	if err != nil {
		return RemoteOutput{}, err
	}
	digest, err := CheckoutDigest(tree.Dir, sparsePatternsOf(e.Fetcher))
	if err != nil {
		return RemoteOutput{}, err
	}
	lock := filepath.Join(dir, "lock")
	if err := copyTree(filepath.Join(tree.Dir, lockDirectory), filepath.Join(lock, lockDirectory)); err != nil {
		return RemoteOutput{}, err
	}
	out := RemoteOutput{Commit: tree.SHA, Ref: tree.Ref, Digest: digest, LockTree: lock}

	if len(t.Queries) == 0 && len(t.Findings) == 0 {
		_ = os.RemoveAll(src)
		return out, nil
	}
	dbDir := filepath.Join(dir, "db")
	if err := e.Builder.CreateDatabase(ctx, tree.Dir, dbDir, "actions"); err != nil {
		return RemoteOutput{}, err
	}
	if out.ResultPath, out.ResultQueryID, out.Extra, err = e.runQueries(ctx, dbDir, dir, plannedQueries(t.Queries)); err != nil {
		return RemoteOutput{}, err
	}
	if len(t.Findings) > 0 {
		out.SarifPath = filepath.Join(dir, "findings.sarif")
		if err := e.Analyzer.AnalyzeQueries(ctx, dbDir, t.Findings, out.SarifPath); err != nil {
			return RemoteOutput{}, err
		}
	}
	// Only the outputs travel back; the checkout and database stay with the worker, which removes
	// all of dir when the attempt ends, so a failure above leaves nothing behind either.
	_ = os.RemoveAll(src)
	_ = os.RemoveAll(dbDir)
	return out, nil
}

// remoteQueries and plannedQueries carry a plan's lanes across the boundary and back.
func remoteQueries(planned []plannedQuery) []RemoteQuery {
	var out []RemoteQuery
	for _, q := range planned {
		out = append(out, RemoteQuery{Path: q.path, ID: q.id, Edges: q.shape == ingest.ShapeEdges})
	}
	return out
}

func plannedQueries(remote []RemoteQuery) []plannedQuery {
	out := make([]plannedQuery, 0, len(remote))
	for _, q := range remote {
		shape := ingest.ShapeObservations
		if q.Edges {
			shape = ingest.ShapeEdges
		}
		out = append(out, plannedQuery{path: q.Path, id: q.ID, shape: shape})
	}
	return out
}

// admit asks the source policy whether repository may be read in a worker, answering why it may. A
// worker holds no store to ask, so nothing reaches one without this.
func (e *Expander) admit(ctx context.Context, repository string) (string, error) {
	if e.SourcePolicy == nil {
		return "", errors.New("no source policy is configured to admit " + repository + " for remote analysis")
	}
	return e.SourcePolicy.Admission(ctx, repository)
}

// prepareRemote hands the fetch and the CodeQL work to the remote runner and keeps every store read
// and write here: content, lock coverage and the ledger are recorded from what came back, and a lane
// the ledger already holds is dropped rather than ingested twice.
func (e *Expander) prepareRemote(
	ctx context.Context, b batch, opts Options, planned tierPlan, dir string, outstanding *pending,
) (work.Prepared, error) {
	admitted, err := e.admit(ctx, b.full())
	if err != nil {
		return work.Prepared{}, err
	}
	// Only what the ledger lacks at the resolved commit is sent; unresolved, every lane is.
	sent := pending{bqrs: planned.bqrs, findings: len(planned.findings) > 0}
	if outstanding != nil {
		sent = *outstanding
	}
	task := RemoteTask{Owner: b.owner, Repo: b.repo, Ref: b.ref, Queries: remoteQueries(sent.bqrs), Admitted: admitted}
	if sent.findings {
		task.Findings = planned.findings
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return work.Prepared{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	// The worker clones on this process's say, so at its rate: the pool is the crawl's, wherever
	// the clone runs.
	if err := e.Pace.Take(ctx); err != nil {
		return work.Prepared{}, err
	}
	e.remotes.Add(1)
	out, err := e.Remote.Run(ctx, task, dir)
	if err != nil {
		return work.Prepared{}, err
	}
	now, err := e.recordObserved(b, planned, Tree{Dir: out.LockTree, SHA: out.Commit, Ref: out.Ref},
		scopedDigest(e.sourceFilter(), out.Digest))
	if err != nil {
		return work.Prepared{}, err
	}
	// The worker fetched by reference, so the commit can have moved since it was resolved, and the
	// new one need a lane it was not sent: that is planned again rather than settled short.
	if lacking := now.without(sent); len(lacking) > 0 {
		return work.Prepared{}, fmt.Errorf("%s moved to %s while a worker analyzed it, and needs %s it was not sent",
			b.full(), out.Commit, strings.Join(lacking, ", "))
	}
	if now.empty() {
		if !opts.KeepArtifacts {
			_ = os.RemoveAll(dir)
		}
		return alreadyRecorded(b, out.Commit, out.Ref), nil
	}
	p := work.Prepared{Job: work.Job{Repository: b.key()}, Commit: out.Commit, AnalyzedRef: out.Ref}
	wanted := map[string]bool{}
	for _, q := range now.bqrs {
		wanted[q.id] = true
	}
	if wanted[out.ResultQueryID] {
		p.ResultPath, p.ResultQueryID = out.ResultPath, out.ResultQueryID
	}
	for id, path := range out.Extra {
		if wanted[id] {
			if p.Extra == nil {
				p.Extra = map[string]string{}
			}
			p.Extra[id] = path
		}
	}
	if now.findings {
		p.SarifPath = out.SarifPath
	}
	return p, nil
}

// copyTree copies a directory of regular files, and makes the destination empty when there is none:
// a checkout without .github/workflows is a lock survey's answer too.
func copyTree(from, to string) error {
	if err := os.MkdirAll(to, 0o755); err != nil {
		return err
	}
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
