package setup

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The classes a preview adds to the classification: never written either.
const (
	PreviewExcluded   = "excluded"
	PreviewNotATarget = "not-a-target"
	PreviewHeld       = "held"
)

// PreviewRequest names what a preview covers.
type PreviewRequest struct {
	ViewName  string
	Predicate string
	// Repositories is the view's population; empty covers every repository under the declaration.
	Repositories []string
	// Exclude names repositories left out, one by one.
	Exclude   []string
	Accounts  []string
	CreatedBy string
}

// Preview classifies every repository the request covers from what was last read, and records the
// body each write would send. It asks GitHub nothing: it is the simulation a batch is approved from.
func Preview(s store.Backend, req PreviewRequest, allowed func(string) bool, now time.Time) (store.SetupPreview, error) {
	classes, err := s.SetupClasses(req.Repositories...)
	if err != nil {
		return store.SetupPreview{}, err
	}
	p := store.SetupPreview{CreatedAt: now, CreatedBy: req.CreatedBy, ViewName: req.ViewName,
		Predicate: req.Predicate, Accounts: req.Accounts, Excluded: req.Exclude, Rows: []store.SetupPreviewRow{}}
	for _, c := range classes {
		if c.Class == store.SetupNotOwned {
			continue
		}
		row := store.SetupPreviewRow{Repository: c.Repository, Class: c.Class, Evidence: c.Evidence,
			Languages: c.Languages, ReadAt: c.SetupReadAt}
		switch {
		case slices.ContainsFunc(req.Exclude, func(x string) bool { return strings.EqualFold(x, c.Repository) }):
			row.Class, row.Evidence = PreviewExcluded, "excluded by name from this preview"
		case !Writable(c.Class):
		case allowed == nil || !allowed(c.Repository):
			row.Class = PreviewNotATarget
			row.Evidence = "outside the write targets, so never written from here: " + c.Evidence
		default:
			body, err := Body(c)
			if err != nil {
				row.Class, row.Evidence = PreviewHeld, err.Error()
				break
			}
			raw, _ := json.Marshal(body)
			row.Intended = string(raw)
		}
		p.Rows = append(p.Rows, row)
	}
	id, err := s.RecordSetupPreview(p)
	if err != nil {
		return store.SetupPreview{}, err
	}
	recorded, _, err := s.SetupPreviewOf(id)
	return recorded, err
}

// Targets is the write guard the server's flags describe: repositories named one by one, and
// accounts whose declared repositories may all be written.
func Targets(repositories, accounts []string) func(string) bool {
	return func(repository string) bool {
		owner, _, _ := strings.Cut(repository, "/")
		return slices.ContainsFunc(repositories, func(r string) bool { return strings.EqualFold(r, repository) }) ||
			slices.ContainsFunc(accounts, func(a string) bool { return strings.EqualFold(a, owner) })
	}
}
