package store

import (
	"fmt"
	"slices"
	"strings"
)

// sightedContent narrows a run to one whose every edge is sighted with what its commit declared.
const sightedContent = " AND sighted_content = 1"

// EdgeRunExists is QueryRunExists for a run whose results are edges.
//
// A run recorded before sightings kept content is not enough. Its commit shares rows with every
// other commit declaring the same locations, and those rows hold whichever was written last, so a
// walk standing at its commit would read another commit's targets. Only re-observing it says what
// its own commit declared.
func (s *Store) EdgeRunExists(q QueryRun) (bool, error) {
	return s.runOnRecord(q, sightedContent)
}

// EdgeRunExistsForContent is QueryRunExistsForContent for a run whose results are edges, under
// the same condition as EdgeRunExists.
func (s *Store) EdgeRunExistsForContent(q QueryRun, digest string) (bool, error) {
	return s.runForContent(q, digest, " AND r.sighted_content = 1")
}

// MarkSightedContent records that every edge a run observed is now sighted with what its commit
// declared. Called once the run's edges are flushed, so a run interrupted before then has not
// answered for its commit and is analyzed again.
func (s *Store) MarkSightedContent(runID int64) error {
	if _, err := s.db.Exec(`UPDATE abom_query_run SET sighted_content = 1 WHERE row_id = ?`,
		runID); err != nil {
		return fmt.Errorf("marking run %d's sightings as carrying content: %w", runID, err)
	}
	return nil
}

// edgesRun is the condition that a run's results are edges: a run of its query sighted an edge,
// or it wrote one. The store never learns a result's shape, and a run attributed an edge before
// sightings existed sighted nothing.
func edgesRun(run string) string {
	return `(` + run + `.query_id IN (SELECT DISTINCT sq.query_id FROM abom_edge_sighting ss
	           JOIN abom_query_run sq ON sq.row_id = ss.query_run_id)
	     OR ` + run + `.row_id IN (SELECT query_run_id FROM abom_edge_regular
	                        WHERE query_run_id IS NOT NULL))`
}

// commitEvidence is what the ledger says about each analyzed commit's edges, read once per answer.
type commitEvidence struct {
	// marked holds, by repository and commit, whether an edges run there kept sighting content.
	marked map[string]map[string]bool
	// equivalent lists, by repository and commit, the commits whose indexed content is identical.
	equivalent map[string][]string
}

func (s *Store) commitEvidence() (commitEvidence, error) {
	ev := commitEvidence{marked: map[string]map[string]bool{}, equivalent: map[string][]string{}}
	rows, err := s.db.Query(`
		SELECT LOWER(q.repository), LOWER(q.db_commit), MAX(q.sighted_content)
		  FROM abom_query_run q
		 WHERE q.analysis_mode = 'regular' AND COALESCE(q.db_commit, '') <> '' AND ` + edgesRun("q") + `
		 GROUP BY 1, 2`)
	if err != nil {
		return ev, fmt.Errorf("reading which commits an edges run analyzed: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var repository, commit string
		var marked int
		if err := rows.Scan(&repository, &commit, &marked); err != nil {
			return ev, fmt.Errorf("scanning an analyzed commit: %w", err)
		}
		if ev.marked[repository] == nil {
			ev.marked[repository] = map[string]bool{}
		}
		ev.marked[repository][commit] = marked == 1
	}
	if err := rows.Err(); err != nil {
		return ev, err
	}
	same, err := s.db.Query(`
		SELECT LOWER(a.repository), LOWER(a.commit_sha), LOWER(b.commit_sha)
		  FROM abom_commit_content a
		  JOIN abom_commit_content b
		    ON b.repository = a.repository AND b.indexed_digest = a.indexed_digest
		   AND b.commit_sha <> a.commit_sha
		 ORDER BY 1, 2, 3`)
	if err != nil {
		return ev, fmt.Errorf("reading commits with identical content: %w", err)
	}
	defer same.Close()
	for same.Next() {
		var repository, commit, identical string
		if err := same.Scan(&repository, &commit, &identical); err != nil {
			return ev, fmt.Errorf("scanning commits with identical content: %w", err)
		}
		key := repository + "\x00" + commit
		ev.equivalent[key] = append(ev.equivalent[key], identical)
	}
	return ev, same.Err()
}

// answers lists the analyzed commits whose sightings stand for a commit: itself where an edges run
// analyzed it, and otherwise every one with identical content that one did.
func (ev commitEvidence) answers(repository, commit string) []string {
	if _, ok := ev.marked[repository][commit]; ok {
		return []string{commit}
	}
	var out []string
	for _, identical := range ev.equivalent[repository+"\x00"+commit] {
		if _, ok := ev.marked[repository][identical]; ok {
			out = append(out, identical)
		}
	}
	return out
}

// unknown reports whether a commit was analyzed and what it declared is still not known: every
// edges run answering for it predates sighting content, in a repository analyzed at another commit
// too, whose rows it may share and which may hold that commit's targets. A repository analyzed at
// one commit shares no row, so its record is the commit's own. A commit nothing analyzed is
// unanalyzed, which is a different statement and not this one.
func (ev commitEvidence) unknown(repository, commit string) bool {
	answers := ev.answers(repository, commit)
	if len(answers) == 0 || len(ev.marked[repository]) < 2 {
		return false
	}
	for _, a := range answers {
		if ev.marked[repository][a] {
			return false
		}
	}
	return true
}

// LiveCommits says, for every repository a reference resolves, which commits a walk may stand at
// and whose sightings answer for each: its own where an edges run analyzed it, or those of an
// analyzed commit with identical content where none did, which is how content equivalence settles
// a moved reference without analysis. A commit no reference resolves to any more is history, and
// absent. A repository no reference resolves is absent too, and keeps every commit it was analyzed
// at: nothing says which of them anyone runs.
//
// Repositories and commits are lowercased.
func (s *Store) LiveCommits() (map[string]map[string][]string, error) {
	ev, err := s.commitEvidence()
	if err != nil {
		return nil, err
	}
	return s.liveCommits(ev)
}

func (s *Store) liveCommits(ev commitEvidence) (map[string]map[string][]string, error) {
	rows, err := s.liveRows()
	if err != nil {
		return nil, err
	}
	live := map[string]map[string][]string{}
	for _, r := range rows {
		if live[r.repository] == nil {
			live[r.repository] = map[string][]string{}
		}
		live[r.repository][r.commit] = ev.answers(r.repository, r.commit)
	}
	return live, nil
}

// EdgeDeclaration is what one declaring location said, at the live commits that said it. A location whose
// commits declared different things is one EdgeDeclaration per thing declared.
type EdgeDeclaration struct {
	Row, Src, Dst int64
	Ref           string
	// Commits are the live commits that declare this. "" is a row no run recorded, which binds to
	// no commit.
	Commits []string
}

// EdgeDeclarations reads every dependency edge as what its declaring location said at the commits a walk
// may stand at, in a stable order.
//
// Per sighting rather than per row: a location is one row however many commits declare it, and the
// row holds the last writer's target. A commit no reference resolves to any more is history and
// adds nothing, and a live commit settled as identical to an analyzed one reads that one's.
func (s *Store) EdgeDeclarations() ([]EdgeDeclaration, error) {
	live, err := s.LiveCommits()
	if err != nil {
		return nil, err
	}
	// Which live commits each analyzed commit answers for: itself, and any settled as identical.
	answers := map[string]map[string][]string{}
	for repository, commits := range live {
		answers[repository] = map[string][]string{}
		for commit, by := range commits {
			for _, analyzed := range by {
				answers[repository][analyzed] = append(answers[repository][analyzed], commit)
			}
		}
		for analyzed := range answers[repository] {
			slices.Sort(answers[repository][analyzed])
		}
	}
	rows, err := s.db.Query(`WITH ` + observedEdges + `
		SELECT e.edge_row_id, e.src_node_row_id, e.dst_node_row_id, e.ref, e.commit_sha,
		       LOWER(COALESCE(r.repository_full_name, ''))
		  FROM observed e LEFT JOIN repository r ON r.row_id = e.repository_row_id
		 WHERE ` + DependencyEdgeFilter + `
		 ORDER BY 1, 3, 4, 5`)
	if err != nil {
		return nil, fmt.Errorf("loading edges: %w", err)
	}
	defer rows.Close()
	out := []EdgeDeclaration{}
	type declared struct {
		row, dst int64
		ref      string
	}
	index := map[declared]int{}
	for rows.Next() {
		var e EdgeDeclaration
		var commit, repository string
		if err := rows.Scan(&e.Row, &e.Src, &e.Dst, &e.Ref, &commit, &repository); err != nil {
			return nil, fmt.Errorf("scanning edge: %w", err)
		}
		commits := []string{commit}
		if resolved, ok := answers[repository]; ok && commit != "" {
			if commits = resolved[commit]; len(commits) == 0 {
				continue
			}
		}
		at := declared{e.Row, e.Dst, e.Ref}
		i, seen := index[at]
		if !seen {
			i, index[at] = len(out), len(out)
			out = append(out, e)
		}
		for _, c := range commits {
			if !slices.Contains(out[i].Commits, c) {
				out[i].Commits = append(out[i].Commits, c)
			}
		}
	}
	return out, rows.Err()
}

// liveRow is a queue row whose reference resolved to a commit, which makes that commit live.
type liveRow struct {
	nodeKey, ref, repository, commit, status string
}

// liveRows reads every queue row whose reference resolved, in the order the crawl claims, with
// repositories and commits lowercased.
func (s *Store) liveRows() ([]liveRow, error) {
	rows, err := s.db.Query(`
		SELECT node_key, ref, LOWER(owner || '/' || repo), LOWER(resolved_sha), status
		  FROM abom_frontier
		 WHERE COALESCE(resolved_sha, '') <> '' AND COALESCE(owner, '') <> ''
		 ORDER BY depth, score DESC, enqueued_at, node_key, ref`)
	if err != nil {
		return nil, fmt.Errorf("reading the commits the queue stands at: %w", err)
	}
	defer rows.Close()
	out := []liveRow{}
	for rows.Next() {
		var r liveRow
		if err := rows.Scan(&r.nodeKey, &r.ref, &r.repository, &r.commit, &r.status); err != nil {
			return nil, fmt.Errorf("scanning a commit the queue stands at: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UnobservedClosureError refuses a submission whose closure reads declarations ABOM has no certain
// record of. A snapshot replaces what GitHub found, so one built from them could delete
// dependencies the repository really has.
type UnobservedClosureError struct {
	Repository string
	Commits    []string
}

func (e *UnobservedClosureError) Error() string {
	shown := e.Commits
	if len(shown) > 5 {
		shown = shown[:5]
	}
	return fmt.Sprintf("refusing to submit %s: its supply chain reads %d commits whose declarations "+
		"were recorded before ABOM kept what each commit declared (%s), so the snapshot could leave "+
		"out dependencies the repository has; they are known once those commits are analyzed again",
		e.Repository, len(e.Commits), strings.Join(shown, ", "))
}

// Unobserved marks the refusal for a transport that maps it without importing this type.
func (e *UnobservedClosureError) Unobserved() {}

// UnobservedClosureCommits lists, as owner/repo@commit, every commit a repository's submission
// closure reads declarations from whose content is not known: the repository's own commits and the
// one each dependency is read at. Empty, never nil, when there is none.
func (s *Store) UnobservedClosureCommits(fullName string) ([]string, error) {
	ev, err := s.commitEvidence()
	if err != nil {
		return nil, err
	}
	live, err := s.liveCommits(ev)
	if err != nil {
		return nil, err
	}
	closure, err := s.submissionClosure(fullName, live)
	if err != nil {
		return nil, err
	}
	out := []string{}
	seen := map[string]bool{}
	note := func(repository, commit string) {
		key := repository + "@" + commit
		if commit == "" || seen[key] {
			return
		}
		seen[key] = true
		if ev.unknown(repository, commit) {
			out = append(out, key)
		}
	}
	own := strings.ToLower(fullName)
	if commits, ok := live[own]; ok {
		for commit := range commits {
			note(own, commit)
		}
	} else {
		for commit := range ev.marked[own] {
			note(own, commit)
		}
	}
	for _, e := range closure {
		note(strings.ToLower(e.Owner+"/"+e.Repo), strings.ToLower(e.ReadAt))
	}
	slices.Sort(out)
	return out, nil
}

// Reobservation is what one pass of re-observation found and did.
type Reobservation struct {
	// Unobserved counts the commits a reference resolves to whose declarations are not known:
	// every edges run there predates sightings keeping content, in a repository analyzed at another
	// commit too.
	Unobserved int `json:"unobserved"`
	// Outstanding counts the dependencies already back in the queue at a commit on record, for any
	// reason. Each observes its commit when it is analyzed, so each takes a place in the window.
	Outstanding int `json:"outstanding"`
	// Requeued counts the dependencies this pass put back.
	Requeued int `json:"requeued"`
}

// UnobservedCommitCount is how many commits a reference resolves to have declarations ABOM does not
// know: what Reobserve works through, measured without putting anything back.
func (s *Store) UnobservedCommitCount() (int, error) {
	got, err := s.Reobserve(0)
	return got.Unobserved, err
}

// Reobserve puts back analyzed dependencies whose commit's declarations are not known, so the crawl
// analyzes those commits again, and keeps no more than window of them in the queue.
//
// Bounded, because all of them at once is a re-crawl: 2,456 rows at every start, when this was a
// startup requeue. Analyzed work already back for any other reason takes its place in the window,
// failed attempts included, so an outage cannot drain the whole backlog into retrying one window at
// a time. Taken in the order the crawl claims, and one row per commit: analyzing a commit answers
// every reference that resolves to it.
//
// What is on record stays, as for any requeue: the walks go on reading it until the new run
// replaces it.
func (s *Store) Reobserve(window int) (Reobservation, error) {
	var out Reobservation
	ev, err := s.commitEvidence()
	if err != nil {
		return out, err
	}
	rows, err := s.liveRows()
	if err != nil {
		return out, err
	}
	unobserved, queued := map[string]bool{}, map[string]bool{}
	for _, r := range rows {
		commit := r.repository + "@" + r.commit
		if r.status == "pending" || r.status == "running" {
			out.Outstanding++
			queued[commit] = true
		}
		if ev.unknown(r.repository, r.commit) {
			unobserved[commit] = true
		}
	}
	out.Unobserved = len(unobserved)
	room := window - out.Outstanding
	if room <= 0 || out.Unobserved == 0 {
		return out, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	put, err := tx.Prepare(`
		UPDATE abom_frontier
		   SET status = 'pending', reason = ?, attempts = 0, next_attempt_at = NULL,
		       claimed_at = NULL, completed_at = NULL
		 WHERE node_key = ? AND ref = ? AND status = 'done'`)
	if err != nil {
		return out, err
	}
	defer put.Close()
	for _, r := range rows {
		commit := r.repository + "@" + r.commit
		if out.Requeued == room {
			break
		}
		if r.status != "done" || queued[commit] || !unobserved[commit] {
			continue
		}
		if _, err := put.Exec("re-observing what "+r.commit+" declared", r.nodeKey, r.ref); err != nil {
			return out, fmt.Errorf("putting back %s@%s: %w", r.nodeKey, r.ref, err)
		}
		queued[commit] = true
		out.Requeued++
	}
	return out, tx.Commit()
}
