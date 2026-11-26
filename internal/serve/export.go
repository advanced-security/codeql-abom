package serve

import (
	"context"
	"errors"
	"io/fs"
	"net/http"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/publish"
	"github.com/advanced-security/codeql-abom/internal/querypack"
)

// Publisher is the one writer ABOM holds: *publish.Publisher in production.
type Publisher interface {
	Target(context.Context) (publish.Target, error)
	Open(context.Context, publish.Target, publish.Export) (url string, reused bool, err error)
}

type exportAnswer struct {
	URL      string `json:"url"`
	Target   string `json:"target"`
	Decision string `json:"decision"`
	Bundle   string `json:"bundle"`
	// Reused is a pull request already open for this bundle, which answered without a new one.
	Reused bool `json:"reused"`
}

// exportQuery opens a pull request of one query version in the publisher's repository: every file
// that makes it run and be tested, exactly the bundle you reviewed, and a record of the decision to
// send it. ABOM opens it; you merge it there, and that repository's own workflow tests and publishes.
func (s *Server) exportQuery(w http.ResponseWriter, r *http.Request) {
	if s.Publisher == nil {
		writeProblem(w, problemNoPublisher,
			"no publisher App is configured; create one with `codeql-abom app create --publisher`")
		return
	}
	reviewed := r.URL.Query().Get("bundle")
	if reviewed == "" {
		writeProblem(w, problemBundleRequired, "name the bundle you reviewed with ?bundle=, the SHA-256 the components showed")
		return
	}
	m, ok := s.componentsOf(w, r)
	if !ok {
		return
	}
	if m.BundleSHA256 != reviewed {
		writeProblem(w, problemBundleChanged, "the query's files changed since you reviewed them: you saw bundle sha256:"+
			reviewed+", and they are sha256:"+m.BundleSHA256+" now; read the components again")
		return
	}
	target, err := s.Publisher.Target(r.Context())
	var misinstalled *publish.Misinstalled
	switch {
	case errors.As(err, &misinstalled):
		writeProblem(w, problemPublisherMisinstall, err.Error())
		return
	case err != nil:
		writeProblem(w, problemUpstream, "the publisher App's installation could not be read", withUpstream(err.Error()))
		return
	}
	files, ok := s.exportedFiles(w, m)
	if !ok {
		return
	}
	evidence := publish.EvidenceOf(m, target, files)
	principal := principalOf(r)
	if asker, ok := authz.RequesterOf(r.Context()); ok {
		principal, evidence.Route = asker.Principal, asker.Route
	}
	d, err := s.Store.Decide(r.Context(), authz.Request{Operation: authz.OpExportQuery, Principal: principal,
		Subject: authz.ExportSubject(m.Query, m.Version), Evidence: evidence, Allow: true})
	if err != nil {
		undecided(w, "the decision to export could not be recorded, so nothing was written", err)
		return
	}
	export, err := publish.Compose(m, evidence, files, d.Digest(), principal.Called(), s.testsOf(m))
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	url, reused, err := s.Publisher.Open(r.Context(), target, export)
	if err != nil {
		writeProblem(w, problemUpstream, "the pull request could not be opened", withUpstream(err.Error()))
		return
	}
	writeJSON(w, exportAnswer{URL: url, Target: target.String(), Decision: d.Digest(), Bundle: m.BundleSHA256,
		Reused: reused}, nil)
}

// exportedFiles reads back what m bundles, answering why it cannot when a file is no longer the one
// whose digest was reviewed.
func (s *Server) exportedFiles(w http.ResponseWriter, m querypack.Manifest) ([]querypack.File, bool) {
	files, err := querypack.Files(s.QueryDir, m)
	var bad *querypack.ComponentError
	var refused *querypack.Refused
	switch {
	case err == nil:
		return files, true
	case errors.As(err, &bad) && errors.Is(err, querypack.ErrChanged):
		writeProblem(w, problemComponentChanged, bad.Path+" changed since its components were read; read them again")
	case errors.As(err, &bad) && (errors.As(err, &refused) || errors.Is(err, fs.ErrNotExist)):
		writeProblem(w, problemComponentChanged, bad.Path+" is no longer a regular file; read the components again")
	case errors.As(err, &bad):
		writeJSON(w, nil, bad.Err)
	default:
		writeJSON(w, nil, err)
	}
	return nil, false
}

// testsOf is what ABOM recorded of m's tests at its version.
func (s *Server) testsOf(m querypack.Manifest) publish.Tests {
	tests, err := s.Store.QueryTestsOf(m.Query)
	if err != nil {
		return publish.Tests{Unread: err}
	}
	for i := len(tests) - 1; i >= 0; i-- {
		if t := tests[i]; t.QueryVersion == m.Version {
			return publish.Tests{Ran: true, Passed: t.Total - t.Failed, Total: t.Total, CLIVersion: t.CLIVersion}
		}
	}
	return publish.Tests{}
}
