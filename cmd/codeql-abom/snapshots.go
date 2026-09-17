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
// The allow-list is a required argument rather than an option with a permissive default. A
// submission cannot be withdrawn, there is no delete endpoint, and on 2026-09-08 one written to an
// unintended repository could only be repaired by writing again — so a build configured with no
// targets must be able to preview and unable to submit.
func wireSnapshots(srv *serve.Server, s *store.Store, token string, targets []string,
	headSHA func(context.Context, string) (string, error)) {

	client := submit.NewClient(token, targets)

	// Renewal iterates this and nothing else, so it can only write where the operator has already
	// said a snapshot may go.
	srv.ApprovedTargets = func() []string {
		out := make([]string, 0, len(client.Allowed))
		for repository := range client.Allowed {
			out = append(out, repository)
		}
		sort.Strings(out)
		return out
	}

	srv.SnapshotPreview = func(repository string) (serve.SnapshotPreview, error) {
		closure, err := s.SubmissionClosure(repository)
		if err != nil {
			return serve.SnapshotPreview{}, err
		}
		snap := submit.Build(submit.Metadata{Repository: repository}, closure)

		out := serve.SnapshotPreview{
			Repository:  repository,
			Manifest:    submit.SyntheticManifestPath,
			Approved:    client.Allowed[strings.ToLower(repository)],
			PackageURLs: []string{},
		}
		// Counted as distinct packages, not as closure rows: one action pinned by four workflows
		// is one dependency, and counting rows makes a repository look four times as exposed.
		direct := map[string]bool{}
		for _, e := range closure {
			if e.Relationship != "direct" {
				continue
			}
			if p := normalize.PackageURL(e.Owner, e.Repo, e.Subpath, e.Ref); p != "" {
				direct[p] = true
			}
		}
		out.Direct = len(direct)
		for _, m := range snap.Manifests {
			for _, r := range m.Resolved {
				out.PackageURLs = append(out.PackageURLs, r.PackageURL)
			}
		}
		sort.Strings(out.PackageURLs)
		out.Transitive = len(out.PackageURLs)

		// Reported beside the counts so a shallow answer is not read as a complete one: a
		// repository whose composite dependencies were never analyzed at the pinned ref has an
		// unknown supply chain, not an empty one.
		pins, err := s.UnanalyzedCompositePins()
		if err != nil {
			return out, err
		}
		for _, p := range pins {
			if p.Pins > 0 {
				out.Unanalyzed++
			}
		}

		// A submission never expires and nothing re-derives a path ABOM has claimed, so a
		// repository whose supply chain has moved keeps publishing the old answer until something
		// notices. This is that something.
		last, err := s.LastSubmission(repository)
		if err != nil {
			return out, err
		}
		out.LastSubmittedAt = last.SubmittedAt
		out.Submitted = last.Submitted
		// Never submitted is not stale; it is unsubmitted, and the two want different actions. A
		// recorded submission with no digest predates renewal and is treated as needing one, which
		// re-sends rather than assuming still-current.
		out.NeedsRenewal = last.Submitted && last.PackageDigest != submit.Digest(snap)
		return out, nil
	}

	srv.SnapshotSubmit = func(repository string) (serve.SnapshotResult, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		sha, err := headSHA(ctx, repository)
		if err != nil {
			return serve.SnapshotResult{}, fmt.Errorf("resolving head of %s: %w", repository, err)
		}
		closure, err := s.SubmissionClosure(repository)
		if err != nil {
			return serve.SnapshotResult{}, err
		}

		meta := submit.Metadata{
			Repository: repository,
			SHA:        sha,
			Ref:        "refs/heads/" + defaultBranchRef,
			ScannedAt:  time.Now().UTC().Format(time.RFC3339),
			JobID:      sha,
		}
		snap := submit.Build(meta, closure)

		var packages int
		for _, m := range snap.Manifests {
			packages += len(m.Resolved)
		}

		res, err := client.Submit(ctx, meta, snap)
		record := store.Submission{
			Repository: repository, SHA: sha,
			Correlator: submit.Correlator, Detector: submit.DetectorName,
			Manifest: submit.SyntheticManifestPath, PackageURLs: packages,
			PackageDigest: submit.Digest(snap),
			Released:      packages == 0,
			Result:        res.Result, Message: res.Message,
			SubmittedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err != nil {
			// A refused submission is recorded too. Without the row a later reader cannot tell a
			// target that was never tried from one that was tried and blocked. It is not a
			// release: nothing was written, and the path was never handed back.
			record.Result, record.Message = "REFUSED", err.Error()
			record.Released = false
			_ = s.RecordSubmission(record)
			return serve.SnapshotResult{}, err
		}
		if err := s.RecordSubmission(record); err != nil {
			return serve.SnapshotResult{}, fmt.Errorf(
				"the snapshot was submitted and could not be recorded, "+
					"so it is live and untracked on %s: %w", repository, err)
		}
		return serve.SnapshotResult{
			Repository: repository, Result: res.Result, Message: res.Message,
			Submitted: packages, Released: packages == 0,
		}, nil
	}
}

// defaultBranchRef is the branch a snapshot is attributed to. Snapshots are not gated on the SHA
// they name — measured 2026-09-08, one submitted against a non-HEAD commit was applied anyway —
// so this is metadata rather than a validity condition.
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
