package harness

import "github.com/advanced-security/codeql-abom/internal/store"

// Records is everything a session reads and keeps. The single binary hands it the store; a worker
// hands it a snapshot of what the session reads, as it stood when the session started, and a journal
// of what the session keeps, which the coordinator replays into the store.
type Records interface {
	// What a session reads while it works.
	QueryRequests() ([]store.QueryRequest, error)
	QueryCatalog(working ...store.WorkingQuery) ([]store.CatalogQuery, error)
	QueryTestsOf(queryID string) ([]store.QueryTest, error)
	NewestASTs() (map[string]store.AuthoringAST, error)
	AuditSession(session int64, through ...int64) (store.AuditSession, error)
	AnalysisRun(id int64) (store.AnalysisRun, error)

	// What a session keeps while it works.
	RecordAuthoringEvent(e store.AuthoringEvent) (int64, error)
	RecordAuthoringAST(a store.AuthoringAST) (int64, error)
	AddAuthoringSpend(session int64, spent store.AuthoringSpend) error
	SetAuthoringSessionModel(session int64, model string) error
	DecideTarget(request int64, queryID, rationale string) error
	RecordQueryTest(t store.QueryTest) error
	RecordRequestDraft(d store.RequestDraft) (int64, error)

	// Where a session begins and ends.
	StartAuthoringSession(request int64, startedBy, model string) (int64, error)
	StartDraftSession(run int64, queryID, startedBy, model string) (int64, error)
	EndAuthoringSession(session int64, state, detail string) error
	AuthoringSessions(request int64) ([]store.AuthoringSession, error)
	DraftSessions(run int64) ([]store.DraftSession, error)
}
