package store

import (
	"errors"
	"testing"
)

// A printed tree is named by the digest of what it says, so an event can name it before any store
// has numbered it, as a worker's must, and the name is the same wherever the tree was kept.
func TestAPrintedTreeIsNamedByTheDigestOfWhatItSays(t *testing.T) {
	s := newTestStore(t)
	request, session := aSessionOn(t, s)
	_, other := aSessionOn(t, s)
	body := "#1 Job a\n"
	id, err := s.RecordAuthoringAST(AuthoringAST{SessionID: session, QueryID: "acme/q",
		PrintedAt: "2026-11-26T12:00:00Z", Query: "PrintAST", Database: "test/Q/Q.testproj",
		Body: body, CLIVersion: "2.27.1"})
	if err != nil {
		t.Fatal(err)
	}

	digest := TreeDigest(body)
	got, err := s.AuthoringASTByDigest(session, digest)
	if err != nil || got.RowID != id || got.Body != body || got.Digest != digest {
		t.Fatalf("the tree named %s reads %+v, %v; want row %d with its body", digest, got, err, id)
	}
	if _, err := s.AuthoringASTByDigest(other, digest); !errors.Is(err, ErrNoAuthoringAST) {
		t.Errorf("another session's tree was found by digest: %v", err)
	}
	listed, err := s.AuthoringASTs(request)
	if err != nil || len(listed) != 1 || listed[0].Digest != digest {
		t.Errorf("the listing names %+v, %v; want the digest beside each tree", listed, err)
	}
	if _, err := s.RecordAuthoringAST(AuthoringAST{SessionID: session, PrintedAt: "2026-11-26T12:00:00Z",
		Query: "PrintAST", Database: "test/Q/Q.testproj", Body: body, CLIVersion: "2.27.1",
		Digest: TreeDigest("something else")}); err == nil {
		t.Error("a tree was kept under a digest that is not of what it says")
	}
}

// A draft is named by the digest of what it proposes, so the agent can be told which draft it kept
// before any store has numbered it.
func TestADraftIsNamedByTheDigestOfWhatItProposes(t *testing.T) {
	s := newTestStore(t)
	run := startedRun(t, s)
	session, err := s.StartDraftSession(run, "actions/unpinned-tag", "operator", "claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	draft := RequestDraft{SessionID: session, Description: "report a tag", Compliant: "uses: a/b@0123",
		NonCompliant: "uses: a/b@v1", Rationale: "the pick ran on a tag"}
	if _, err := s.RecordRequestDraft(draft); err != nil {
		t.Fatal(err)
	}

	got, err := s.DraftSessions(run)
	if err != nil || len(got) != 1 || got[0].Draft == nil || got[0].Draft.Digest != DraftDigest(draft) {
		t.Fatalf("drafts %+v, %v; want the draft named by %s", got, err, DraftDigest(draft))
	}
	changed := draft
	changed.Rationale = "another reason"
	if DraftDigest(changed) == DraftDigest(draft) {
		t.Error("two drafts proposing different things share a name")
	}
}
