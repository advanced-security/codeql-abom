package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/normalize"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/submit"
)

// wireSnapshots attaches the dependency submission path to the server.
//
// The allow-list is required because a submitted snapshot cannot be withdrawn; no targets means
// preview only.
func wireSnapshots(srv *serve.Server, s *store.Store, token string, targets []string,
	headSHA func(context.Context, string) (string, error)) {

	w := snapshotWiring{s: s, client: submit.NewClient(token, targets), headSHA: headSHA}
	srv.ApprovedTargets = w.approvedTargets
	srv.SnapshotPreview = w.preview
	srv.SnapshotSubmit = w.submit
}

type snapshotWiring struct {
	s       *store.Store
	client  *submit.Client
	headSHA func(context.Context, string) (string, error)
}

// Renewal iterates this and nothing else, so it can only write where the operator approved.
func (w snapshotWiring) approvedTargets() []string {
	out := make([]string, 0, len(w.client.Allowed))
	for repository := range w.client.Allowed {
		out = append(out, repository)
	}
	sort.Strings(out)
	return out
}

func (w snapshotWiring) preview(repository string) (serve.SnapshotPreview, error) {
	closure, err := w.s.SubmissionClosure(repository)
	if err != nil {
		return serve.SnapshotPreview{}, err
	}
	snap := submit.Build(submit.Metadata{Repository: repository}, closure)

	out := serve.SnapshotPreview{
		Repository:  repository,
		Manifest:    submit.SyntheticManifestPath,
		Approved:    w.client.Allowed[strings.ToLower(repository)],
		PackageURLs: packageURLs(snap),
	}
	out.Direct = directPackageCount(closure)
	out.Transitive = len(out.PackageURLs)

	if out.Unanalyzed, err = w.unanalyzedCompositePins(); err != nil {
		return out, err
	}
	if err := w.fillSubmissionRenewal(repository, snap, &out); err != nil {
		return out, err
	}
	return out, nil
}

func directPackageCount(closure []store.ClosureEntry) int {
	direct := map[string]bool{}
	for _, e := range closure {
		if e.Relationship != "direct" {
			continue
		}
		if p := normalize.PackageURL(e.Owner, e.Repo, e.Subpath, e.Ref); p != "" {
			direct[p] = true
		}
	}
	return len(direct)
}

func packageURLs(snap submit.Snapshot) []string {
	out := []string{}
	for _, m := range snap.Manifests {
		for _, r := range m.Resolved {
			out = append(out, r.PackageURL)
		}
	}
	sort.Strings(out)
	return out
}

func (w snapshotWiring) unanalyzedCompositePins() (int, error) {
	pins, err := w.s.UnanalyzedCompositePins()
	if err != nil {
		return 0, err
	}
	var unanalyzed int
	for _, p := range pins {
		if p.Pins > 0 {
			unanalyzed++
		}
	}
	return unanalyzed, nil
}

func (w snapshotWiring) fillSubmissionRenewal(
	repository string, snap submit.Snapshot, out *serve.SnapshotPreview,
) error {
	last, err := w.s.LastSubmission(repository)
	if err != nil {
		return err
	}
	out.LastSubmittedAt = last.SubmittedAt
	out.Submitted = last.Submitted
	out.NeedsRenewal = last.Submitted && last.PackageDigest != submit.Digest(snap)
	return nil
}

func (w snapshotWiring) submit(repository string) (serve.SnapshotResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sha, err := w.headSHA(ctx, repository)
	if err != nil {
		return serve.SnapshotResult{}, fmt.Errorf("resolving head of %s: %w", repository, err)
	}
	closure, err := w.s.SubmissionClosure(repository)
	if err != nil {
		return serve.SnapshotResult{}, err
	}

	meta := snapshotMetadata(repository, sha)
	snap := submit.Build(meta, closure)
	packages := packageCount(snap)

	res, err := w.client.Submit(ctx, meta, snap)
	record := submissionRecord(repository, sha, snap, packages, res)
	if err != nil {
		record.Result, record.Message = "REFUSED", err.Error()
		record.Released = false
		_ = w.s.RecordSubmission(record)
		return serve.SnapshotResult{}, err
	}
	if err := w.s.RecordSubmission(record); err != nil {
		return serve.SnapshotResult{}, fmt.Errorf(
			"the snapshot was submitted and could not be recorded, "+
				"so it is live and untracked on %s: %w", repository, err)
	}
	return serve.SnapshotResult{
		Repository: repository, Result: res.Result, Message: res.Message,
		Submitted: packages, Released: packages == 0,
	}, nil
}

func snapshotMetadata(repository, sha string) submit.Metadata {
	return submit.Metadata{
		Repository: repository,
		SHA:        sha,
		Ref:        "refs/heads/" + defaultBranchRef,
		ScannedAt:  time.Now().UTC().Format(time.RFC3339),
		JobID:      sha,
	}
}

func packageCount(snap submit.Snapshot) int {
	var packages int
	for _, m := range snap.Manifests {
		packages += len(m.Resolved)
	}
	return packages
}

func submissionRecord(
	repository, sha string, snap submit.Snapshot, packages int, res submit.Result,
) store.Submission {
	return store.Submission{
		Repository: repository, SHA: sha,
		Correlator: submit.Correlator, Detector: submit.DetectorName,
		Manifest: submit.SyntheticManifestPath, PackageURLs: packages,
		PackageDigest: submit.Digest(snap),
		Released:      packages == 0,
		Result:        res.Result, Message: res.Message,
		SubmittedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// defaultBranchRef is metadata; GitHub accepts snapshots even when the named SHA is not HEAD.
const defaultBranchRef = "main"

// splitTargets parses the allow-list. An empty or whitespace entry is dropped rather than
// becoming a target that matches the empty repository name.
func splitTargets(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}
