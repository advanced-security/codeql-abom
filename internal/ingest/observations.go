package ingest

import (
	"fmt"
	"io"

	"github.com/advanced-security/codeql-abom/internal/bqrs"
	"github.com/advanced-security/codeql-abom/internal/normalize"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// The observation column contract.
//
// A query joins ABOM by selecting these names, and is then ingested with no Go written for it.
// Column names rather than positions, so a query may add a column without breaking the reader
// and may omit the optional ones entirely.
const (
	ColSubjectKey  = "subjectKey"
	ColSubjectKind = "subjectKind"
	ColName        = "name"
	ColValue       = "value"
	ColValueNum    = "valueNum"
	ColFilePath    = "filePath"
	ColStartLine   = "startLine"
	// A query is evaluated against one repository's database and cannot know which repository
	// that is, so it may name a subject by where it sits instead of by a full node key. The
	// ingester knows the repository and builds the same key the graph uses.
	ColSubjectPath = "subjectPath"
	ColSubjectJob  = "subjectJob"
	// A step is identified by its position, because `id:` is optional and usually absent.
	ColSubjectStep = "subjectStep"
)

// ObservationsFromBQRS ingests any query that satisfies the observation contract.
//
// This is the generic path: nothing here knows what the query was measuring, only that it named
// a subject and said something about it. That is what lets the set of queries grow without the
// ingester growing with it.
func (i *Ingester) ObservationsFromBQRS(r io.Reader, opts Options) (int, error) {
	if opts.QueryRunID == 0 {
		return 0, fmt.Errorf("ingest: observations need Options.QueryRunID to attribute them")
	}

	var batch []store.Observation
	_, err := bqrs.StreamSelect(r, func(row bqrs.Row) error {
		obs, err := observationOf(row, opts.Repository)
		if err != nil {
			return err
		}
		batch = append(batch, obs)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return i.store.AddObservations(opts.QueryRunID, batch)
}

// subjectKeyOf resolves what the query said its subject was.
//
// A full node key wins when given. Otherwise the query named a place in the repository being
// analyzed, and the key is built the same way the graph builds it, so an observation lands on
// the node the reader will be looking at rather than beside it.
func subjectKeyOf(row bqrs.Row, repository string) (string, error) {
	if key, err := row.String(ColSubjectKey); err == nil && key != "" {
		return key, nil
	}
	path, err := row.String(ColSubjectPath)
	if err != nil || path == "" {
		return "", fmt.Errorf(
			"ingest: an observation query must select %q, or %q with the repository it is run "+
				"against supplying the rest", ColSubjectKey, ColSubjectPath)
	}
	if repository == "" {
		return "", fmt.Errorf(
			"ingest: %q needs Options.Repository to become a node key", ColSubjectPath)
	}
	job, _ := row.String(ColSubjectJob)
	// Position is what identifies a step, and it is checked before the job because a composite
	// action's `runs:` block has no job to name. Reading an empty job as "not a step" put every
	// step in an action manifest on the file's own key, where the uniqueness constraint then
	// kept one row per file.
	if step, err := row.Int(ColSubjectStep); err == nil && step >= 0 {
		return normalize.StepKey(repository, path, job, step), nil
	}
	if job != "" {
		return normalize.JobKey(repository, path, job), nil
	}
	return normalize.WorkflowKey(repository, path), nil
}

func observationOf(row bqrs.Row, repository string) (store.Observation, error) {
	key, err := subjectKeyOf(row, repository)
	if err != nil {
		return store.Observation{}, err
	}
	name, err := row.String(ColName)
	if err != nil {
		return store.Observation{}, fmt.Errorf(
			"ingest: an observation query must select %q: %w", ColName, err)
	}

	obs := store.Observation{
		SubjectKind: store.SubjectNode,
		SubjectKey:  key,
		Name:        name,
	}
	// Everything below is optional: a query that only says "this is true of that" is a complete
	// observation, and demanding more would rule out the simplest useful queries.
	if kind, err := row.String(ColSubjectKind); err == nil && kind != "" {
		obs.SubjectKind = kind
	}
	if value, err := row.String(ColValue); err == nil {
		obs.Value = value
	}
	if num, err := row.Float(ColValueNum); err == nil {
		obs.Number, obs.HasNumber = num, true
	}
	if path, err := row.String(ColFilePath); err == nil {
		obs.FilePath = path
	}
	if line, err := row.Int(ColStartLine); err == nil {
		obs.StartLine = line
	}
	return obs, nil
}
