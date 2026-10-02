package serve

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// What a row without a declaration says instead, where the reason is not the sweep's own.
const (
	declarationReading     = "Declared metadata not read yet: reading it now."
	declarationUnversioned = "Declared metadata cannot be known: no version was recorded for these runs."
	declarationNoReader    = "Declared metadata not read: this server runs no CodeQL to read it with."
)

// declarationState is what reading declarations has learned in this process.
type declarationState struct {
	// sweeping is held for as long as the one sweep under way runs.
	sweeping sync.Mutex
	mu       sync.Mutex
	// unreadable holds why each version looked for was not found. Kept until the server stops: a
	// version is its content, so one no file on disk is will not become one by being asked again.
	unreadable map[string]string
	// failed is why the last sweep could not run at all, which says nothing about the disk.
	failed string
}

// noteDeclarations says on every row without a declaration why it has none, and starts reading the
// ones nothing has looked for yet. A version is its content, so this reads each one once.
func (s *Server) noteDeclarations(rows []catalogRow) {
	var pending []store.CatalogQuery
	s.declarations.mu.Lock()
	for i := range rows {
		q := &rows[i]
		key := q.QueryID + "\x00" + q.QueryVersion
		switch why, looked := s.declarations.unreadable[key]; {
		case q.Lane || q.Metadata != nil:
		case q.QueryVersion == "":
			q.MetadataUnread = declarationUnversioned
		case s.ReadDeclarations == nil:
			q.MetadataUnread = declarationNoReader
		case looked:
			q.MetadataUnread = why
		case s.declarations.failed != "":
			q.MetadataUnread = declarationReading + " The last attempt failed: " + s.declarations.failed
			pending = append(pending, q.CatalogQuery)
		default:
			q.MetadataUnread = declarationReading
			pending = append(pending, q.CatalogQuery)
		}
	}
	s.declarations.mu.Unlock()
	if len(pending) == 0 || !s.declarations.sweeping.TryLock() {
		return
	}
	// Detached from the request: a listing that closes is not somebody asking to stop reading.
	go s.readDeclarations(context.Background(), pending)
}

// ReadUnreadDeclarations reads what every catalog version nothing has read declares, as the server
// starts: a corpus written before declarations were recorded, or listed under a CodeQL since
// replaced, is read without anybody opening the catalog or running anything.
func (s *Server) ReadUnreadDeclarations(ctx context.Context) {
	if s.ReadDeclarations == nil {
		return
	}
	pending, err := s.unreadVersions()
	if err != nil {
		if s.Log != nil {
			fmt.Fprintf(s.Log, "serve: listing the catalog to read its declarations: %v\n", err)
		}
		return
	}
	if len(pending) == 0 || !s.declarations.sweeping.TryLock() {
		return
	}
	s.readDeclarations(ctx, pending)
}

// unreadVersions is every catalog version that has a version and no declaration on record.
func (s *Server) unreadVersions() ([]store.CatalogQuery, error) {
	_, working, err := s.workingQueries()
	if err != nil {
		return nil, err
	}
	catalog, err := s.Store.QueryCatalog(working...)
	if err != nil {
		return nil, err
	}
	var out []store.CatalogQuery
	for _, q := range catalog {
		if !q.Lane && q.Metadata == nil && q.QueryVersion != "" {
			out = append(out, q)
		}
	}
	return out, nil
}

// readDeclarations runs one sweep the caller holds the lock for.
func (s *Server) readDeclarations(ctx context.Context, pending []store.CatalogQuery) {
	defer s.declarations.sweeping.Unlock()
	unreadable, err := s.ReadDeclarations(ctx, pending)
	s.declarations.mu.Lock()
	defer s.declarations.mu.Unlock()
	if err != nil {
		s.declarations.failed = err.Error()
		if s.Log != nil {
			fmt.Fprintf(s.Log, "serve: reading what %d query versions declare: %v\n", len(pending), err)
		}
		return
	}
	s.declarations.failed = ""
	if s.declarations.unreadable == nil {
		s.declarations.unreadable = map[string]string{}
	}
	maps.Copy(s.declarations.unreadable, unreadable)
	if s.Log != nil {
		fmt.Fprintf(s.Log, "serve: read what %d of %d query versions declare; %d cannot be read\n",
			len(pending)-len(unreadable), len(pending), len(unreadable))
	}
}
