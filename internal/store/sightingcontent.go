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

// answers lists the analyzed commits whose sightings stand for a commit. Sighted evidence comes
// first, since identical content declares identical things: itself where its edges run kept sighting
// content, else each identical commit whose run did; failing both, itself where any edges run
// analyzed it, else each identical commit one did.
func (ev commitEvidence) answers(repository, commit string) []string {
	marked := ev.marked[repository]
	if marked[commit] {
		return []string{commit}
	}
	var sighted, twins []string
	for _, identical := range ev.equivalent[repository+"\x00"+commit] {
		if kept, ok := marked[identical]; ok {
			twins = append(twins, identical)
			if kept {
				sighted = append(sighted, identical)
			}
		}
	}
	if len(sighted) > 0 {
		return sighted
	}
	if _, ok := marked[commit]; ok {
		return []string{commit}
	}
	return twins
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
// and whose sightings answer for each, as answers orders them: an identical commit's sightings are
// how content equivalence settles a moved reference without analysis. A commit no reference
// resolves to any more is history, and absent. A repository no reference resolves is absent too,
// and keeps every commit it was analyzed at: nothing says which of them anyone runs.
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
	return liveCommitsOf(rows, ev), nil
}

func liveCommitsOf(rows []liveRow, ev commitEvidence) map[string]map[string][]string {
	live := map[string]map[string][]string{}
	for _, r := range rows {
		if live[r.repository] == nil {
			live[r.repository] = map[string][]string{}
		}
		live[r.repository][r.commit] = ev.answers(r.repository, r.commit)
	}
	return live
}

// liveAndStarting is LiveCommits and StartingCommits from one reading of the queue, so the two
// cannot describe different states of it.
func (s *Store) liveAndStarting() (live, starting map[string]map[string][]string, err error) {
	ev, err := s.commitEvidence()
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.liveRows()
	if err != nil {
		return nil, nil, err
	}
	live = liveCommitsOf(rows, ev)
	return live, startingCommits(rows, live, true), nil
}

// StartingCommits narrows live, which is LiveCommits, to where each repository's own declarations
// are a starting point's: the commits its depth-0 queue rows resolve to, which are its default
// branch and its protected branches, each answered for as live answers it. A commit only somebody
// else's reference resolves to is theirs: nobody runs a repository's own CI at a release a
// consumer pins.
//
// A repository a reference resolves is present. With no default-branch or protected-branch commit
// on record it has none, because what it runs is not known and its dependency commits are not a
// guess at it. With one, it also stands at "", a declaration no run recorded, which binds to no
// commit. A repository no reference resolves is absent, as in live, and keeps every commit.
func (s *Store) StartingCommits(
	live map[string]map[string][]string,
) (map[string]map[string][]string, error) {
	rows, err := s.liveRows()
	if err != nil {
		return nil, err
	}
	return startingCommits(rows, live, true), nil
}

// startingCommits is StartingCommits over rows already read. Without branches it is the default
// branch's alone, which is what a snapshot is filed under.
func startingCommits(
	rows []liveRow, live map[string]map[string][]string, branches bool,
) map[string]map[string][]string {
	out := make(map[string]map[string][]string, len(live))
	for repository := range live {
		out[repository] = map[string][]string{}
	}
	for _, r := range rows {
		if r.depth != 0 || (r.ref != "" && !branches) {
			continue
		}
		if out[r.repository] == nil {
			out[r.repository] = map[string][]string{}
		}
		out[r.repository][r.commit] = live[r.repository][r.commit]
	}
	for _, commits := range out {
		if len(commits) > 0 {
			commits[""] = []string{""}
		}
	}
	return out
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
//
// live is LiveCommits and starting StartingCommits of it, passed in because a reader that keeps the
// walk keeps the commits it stood at.
func (s *Store) EdgeDeclarations(
	live, starting map[string]map[string][]string,
) ([]EdgeDeclaration, error) {
	answers, err := liveAnswers(live, starting)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`WITH `+liveDeclared(AnalysisModeRegular)+`
		SELECT e.edge_row_id, e.src_node_row_id, e.dst_node_row_id, e.ref, e.commit_sha
		  FROM declared e
		 WHERE `+DependencyEdgeFilter+`
		 ORDER BY 1, 3, 4, e.analyzed, 5`, answers)
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
		var commit string
		if err := rows.Scan(&e.Row, &e.Src, &e.Dst, &e.Ref, &commit); err != nil {
			return nil, fmt.Errorf("scanning edge: %w", err)
		}
		at := declared{e.Row, e.Dst, e.Ref}
		i, seen := index[at]
		if !seen {
			i, index[at] = len(out), len(out)
			out = append(out, e)
		}
		if !slices.Contains(out[i].Commits, commit) {
			out[i].Commits = append(out[i].Commits, commit)
		}
	}
	return out, rows.Err()
}

// analysisModesIn lists, as SQL literals, the run modes whose observations a reader of mode reads.
func analysisModesIn(mode string) string {
	switch mode {
	case AnalysisModeExtended:
		return `'extended'`
	case AnalysisModeAnyShape:
		return `'regular', 'extended'`
	default:
		return `'regular'`
	}
}

// observedIn is what each run of a mode recorded at each location, as a CTE named `observed`:
// every sighting whose content was recorded, and the row itself at the commit of the run that
// wrote it, which is "" for a row no run recorded. A sighting whose content was never recorded
// says nothing, so it is not here at all. observedEdges is the regular case of the same reading.
//
// The run states the mode, so the live rows are read directly rather than through the mode view,
// whose test per row cost more than the rest of the reading. A row its own run sighted with
// content is that sighting already. Duplicates remain; every reader groups or deduplicates.
func observedIn(mode string) string {
	modes := analysisModesIn(mode)
	return `observed(edge_row_id, src_node_row_id, dst_node_row_id, ref, commit_sha,
         repository_row_id, file_path, edge_kind, ref_raw) AS (
	SELECT sg.edge_row_id, e.src_node_row_id, sg.dst_node_row_id, COALESCE(sg.ref_value, ''),
	       LOWER(COALESCE(q.db_commit, '')), e.repository_row_id, e.file_path, e.edge_kind,
	       COALESCE(sg.ref_raw, '')
	  FROM abom_edge_sighting sg
	  JOIN abom_query_run q ON q.row_id = sg.query_run_id AND q.analysis_mode IN (` + modes + `)
	  JOIN abom_edge_any_mode e ON e.row_id = sg.edge_row_id
	 WHERE sg.dst_node_row_id IS NOT NULL
	UNION ALL
	SELECT e.row_id, e.src_node_row_id, e.dst_node_row_id, COALESCE(e.ref_value, ''),
	       LOWER(COALESCE(q.db_commit, '')), e.repository_row_id, e.file_path, e.edge_kind,
	       COALESCE(e.ref_raw, '')
	  FROM abom_edge_any_mode e
	  LEFT JOIN abom_query_run q ON q.row_id = e.query_run_id
	 WHERE COALESCE(q.analysis_mode, 'regular') IN (` + modes + `)
	   AND NOT EXISTS (SELECT 1 FROM abom_edge_sighting own
	                    WHERE own.edge_row_id = e.row_id AND own.query_run_id = e.query_run_id
	                      AND own.dst_node_row_id IS NOT NULL)
)`
}

// liveDeclared is EdgeDeclarations' reading in SQL, for readers that filter, page and count: CTEs
// ending in one named `declared`, a row per observation and per live commit it answers for, with
// `analyzed` the commit it was observed at and `commit_sha` the one a walk may stand at.
//
// A sighting without recorded content adds nothing; a commit nothing resolves to any more is
// history and adds nothing; a repository no reference resolves keeps every commit it was analyzed
// at; a row no run recorded binds to no commit. A line of a repository under review in a workflow
// that something triggers is live only at its starting commits: nobody runs a repository's own CI
// at a release a consumer pins. A composite action, and a workflow another repository calls, stay
// live wherever consumers resolve them.
// Binds one argument where it sits in the statement: liveAnswers of LiveCommits and StartingCommits.
func liveDeclared(mode string) string {
	// Materialized, or each observation re-reads the JSON rather than an index built on it once.
	return `live_answer(repository, commit_sha, answer, starting) AS MATERIALIZED (
	SELECT json_extract(j.value, '$.r'), json_extract(j.value, '$.c'), json_extract(j.value, '$.a'),
	       COALESCE(json_extract(j.value, '$.s'), 0)
	  FROM json_each(?) j
),
triggered_line(row_id) AS (
	SELECT s.row_id FROM abom_node_live s
	  JOIN abom_node_live c ON c.row_id = s.container_node_row_id AND c.node_kind = 'workflow'
	 WHERE NOT EXISTS (SELECT 1 FROM abom_edge_any_mode x
	                    WHERE x.dst_node_row_id = c.row_id
	                      AND x.repository_row_id IS NOT c.repository_row_id)
),
` + observedIn(mode) + `,
declared(edge_row_id, src_node_row_id, dst_node_row_id, ref, ref_raw, edge_kind,
         repository_row_id, analyzed, commit_sha) AS MATERIALIZED (
	SELECT o.edge_row_id, o.src_node_row_id, o.dst_node_row_id, o.ref, o.ref_raw, o.edge_kind,
	       o.repository_row_id, o.commit_sha, COALESCE(la.commit_sha, o.commit_sha)
	  FROM observed o
	  LEFT JOIN repository r ON r.row_id = o.repository_row_id
	  LEFT JOIN live_answer la
	    ON o.commit_sha <> '' AND la.answer = o.commit_sha
	   AND la.repository = LOWER(r.repository_full_name)
	 WHERE (la.commit_sha IS NOT NULL OR o.commit_sha = ''
	        OR LOWER(COALESCE(r.repository_full_name, '')) NOT IN (SELECT repository FROM live_answer))
	   AND (COALESCE(r.origin, '') <> '` + OriginSeed + `' OR la.starting = 1
	        OR LOWER(r.repository_full_name) NOT IN (SELECT repository FROM live_answer)
	        OR (o.commit_sha = '' AND LOWER(r.repository_full_name) IN
	              (SELECT repository FROM live_answer WHERE starting = 1))
	        OR o.src_node_row_id NOT IN (SELECT row_id FROM triggered_line))
)`
}

// declaredEdges is liveDeclared with one more CTE, `declared_edge`: a row per distinct declaration,
// shaped like abom_edge, so a reader of the row reads what was declared at the commits in use by
// naming it instead. `commits` lists those commits, comma-joined, NULL where no run recorded one.
//
// The location's own columns come from the row, because a sighting does not carry them. The facts
// derived from a target are read from a row declaring the same thing, since each is a function of
// what it is derived from: a reference style of the reference and its target, a boundary of the
// target and the declaring repository, and a route of the line and its target. Where no row
// declares the same thing nothing derived it, so it is absent. Depth is the line's own.
func declaredEdges(mode string) string {
	return liveDeclared(mode) + `,
declared_style(dst_node_row_id, ref_value, ref_style) AS (
	SELECT dst_node_row_id, ref_value, MAX(ref_style) FROM abom_edge_any_mode
	 WHERE ref_style IS NOT NULL GROUP BY 1, 2
),
declared_boundary(repository_row_id, dst_node_row_id, boundary) AS (
	SELECT repository_row_id, dst_node_row_id, MAX(boundary) FROM abom_edge_any_mode
	 WHERE boundary IS NOT NULL GROUP BY 1, 2
),
declared_edge(row_id, edge_key, edge_kind, src_node_row_id, dst_node_row_id, ref_value,
              repository_row_id, file_path, start_line, boundary, ref_style,
              advisory_route, finding_route, owned_depth, commits) AS (
	SELECT x.row_id, x.edge_key, x.edge_kind, x.src_node_row_id, d.dst_node_row_id,
	       NULLIF(d.ref, ''), x.repository_row_id, x.file_path, x.start_line, b.boundary,
	       st.ref_style,
	       CASE WHEN d.dst_node_row_id = x.dst_node_row_id THEN x.advisory_route END,
	       CASE WHEN d.dst_node_row_id = x.dst_node_row_id THEN x.finding_route END,
	       x.owned_depth, d.commits
	  FROM (SELECT edge_row_id, dst_node_row_id, ref,
	               GROUP_CONCAT(DISTINCT NULLIF(commit_sha, '')) AS commits
	          FROM declared GROUP BY edge_row_id, dst_node_row_id, ref) d
	  JOIN abom_edge_any_mode x ON x.row_id = d.edge_row_id
	  LEFT JOIN declared_style st
	    ON st.dst_node_row_id = d.dst_node_row_id AND st.ref_value = d.ref
	  LEFT JOIN declared_boundary b
	    ON b.repository_row_id = x.repository_row_id AND b.dst_node_row_id = d.dst_node_row_id
)`
}

// declaredCommits splits declared_edge's `commits`, sorted. Empty, never nil, where no run
// recorded a commit.
func declaredCommits(joined string) []string {
	out := []string{}
	if joined == "" {
		return out
	}
	out = strings.Split(joined, ",")
	slices.Sort(out)
	return out
}

// liveRow is a queue row whose reference resolved to a commit, which makes that commit live.
type liveRow struct {
	nodeKey, ref, repository, commit, status string
	depth                                    int
}

// liveRows reads every queue row whose reference resolved, in the order the crawl claims, with
// repositories and commits lowercased.
func (s *Store) liveRows() ([]liveRow, error) {
	rows, err := s.db.Query(`
		SELECT node_key, ref, LOWER(owner || '/' || repo), LOWER(resolved_sha), status, depth
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
		if err := rows.Scan(&r.nodeKey, &r.ref, &r.repository, &r.commit, &r.status,
			&r.depth); err != nil {
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
// closure reads declarations from whose content is not known: those its own declarations are read
// at, and the one each dependency is read at. Empty, never nil, when there is none.
func (s *Store) UnobservedClosureCommits(fullName string) ([]string, error) {
	ev, err := s.commitEvidence()
	if err != nil {
		return nil, err
	}
	live, err := s.liveCommits(ev)
	if err != nil {
		return nil, err
	}
	commits, governed, err := s.snapshotCommits(fullName, live, false)
	if err != nil {
		return nil, err
	}
	closure, err := s.submissionClosure(fullName, live, commits, governed)
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
	if governed {
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
	// unobserved names, as owner/repo@commit, the commits Unobserved counts.
	unobserved map[string]bool
}

// UnobservedCommitCount is how many commits a reference resolves to have declarations ABOM does not
// know: what Reobserve works through, measured without putting anything back.
func (s *Store) UnobservedCommitCount() (int, error) {
	got, err := s.Reobserve(0)
	return got.Unobserved, err
}

// UnobservedCommits names, as owner/repo@commit, the commits UnobservedCommitCount counts.
func (s *Store) UnobservedCommits() (map[string]bool, error) {
	got, err := s.Reobserve(0)
	return got.unobserved, err
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
	return s.ReobserveInside(window, nil)
}

// ReobserveInside is Reobserve kept to the repositories inside reports, as the owned closure bounds
// the crawl: a row the crawl never claims would hold its place in the window for good. A nil inside
// admits every repository.
func (s *Store) ReobserveInside(window int, inside func(repository string) bool) (Reobservation, error) {
	var out Reobservation
	ev, err := s.commitEvidence()
	if err != nil {
		return out, err
	}
	all, err := s.liveRows()
	if err != nil {
		return out, err
	}
	rows := all
	if inside != nil {
		rows = make([]liveRow, 0, len(all))
		for _, r := range all {
			if inside(r.repository) {
				rows = append(rows, r)
			}
		}
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
	out.Unobserved, out.unobserved = len(unobserved), unobserved
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
		       claimed_at = NULL, completed_at = NULL, requeued_for = 'reobserve'
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
