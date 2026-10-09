package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// LineFindings is what every rule reported on the `uses:` lines naming one version, each finding
// matched to a line at the commit its run analyzed.
type LineFindings struct {
	Count    int            `json:"count"`
	Severity SeverityCounts `json:"severity"`
	// Commits counts the repository commits at which a counted line named the version, and
	// Analyzed those a findings run reached. Count covers only the second.
	Commits  int `json:"commits"`
	Analyzed int `json:"analyzed"`
	// Unknown counts findings on these lines at commits whose declaration there was never
	// recorded, so whether they are this version's is not known. None of them is in Count.
	Unknown int `json:"unknown"`
}

// lineAt is one `uses:` line at one commit an edges run analyzed it at.
type lineAt struct {
	edge               int64
	repository, commit string
}

// lineState is what a line declared at one commit, as far as the sightings there say.
type lineState struct {
	known bool
	// names are the references the line named the node by there.
	names []string
}

type lineHit struct {
	finding int64
	band    string
}

// FindingsOnLines answers, for each reference of a node, what any rule reported on the `uses:`
// lines naming it in the repositories counted for that reference. A reference absent from the
// answer is one no findings run reached at any commit where its lines named it: not analyzed,
// which is not clean.
//
// A line is one location tracked across commits, and it names something else at some of them, so
// a finding is matched to it at the commit its own run analyzed and by what that commit's sighting
// says the line declared. Never by the edge row, which holds whichever commit wrote it last. Only
// the commits the walk stands at are read, so these are the lines the uses: lines column counts.
//
// live is LiveCommits, which the walk that counted the repositories has already read.
func (s *Store) FindingsOnLines(key string, counted map[string][]string, live map[string]map[string][]string,
) (map[string]LineFindings, error) {
	out := map[string]LineFindings{}
	countedBy, repositories := map[string]map[string]bool{}, []string{}
	for ref, names := range counted {
		for _, name := range names {
			name = strings.ToLower(name)
			if countedBy[ref] == nil {
				countedBy[ref] = map[string]bool{}
			}
			countedBy[ref][name] = true
			if !slices.Contains(repositories, name) {
				repositories = append(repositories, name)
			}
		}
	}
	if len(repositories) == 0 {
		return out, nil
	}
	var node int64
	err := s.db.QueryRow(`SELECT row_id FROM abom_node_live WHERE node_key = ?`, key).Scan(&node)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading node %q: %w", key, err)
	}
	states, owners, err := s.linesNaming(node, repositories, standingCommits(live))
	if err != nil {
		return nil, err
	}
	if len(states) == 0 {
		return out, nil
	}
	hits, err := s.findingsOnLineCommits(states)
	if err != nil {
		return nil, err
	}
	analyzed, err := s.findingsRunCommits(states)
	if err != nil {
		return nil, err
	}
	for ref, repos := range countedBy {
		if found, ok := tallyLineFindings(ref, repos, states, owners, hits, analyzed); ok {
			out[ref] = found
		}
	}
	return out, nil
}

// standingCommits names, for each repository the queue tracks, the analyzed commits whose
// sightings a walk reads: those answering for a commit some reference resolves to. A repository
// absent from the answer is read at every commit it was analyzed at, as the walk reads it.
func standingCommits(live map[string]map[string][]string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for repository, commits := range live {
		out[repository] = map[string]bool{}
		for _, answering := range commits {
			for _, commit := range answering {
				out[repository][commit] = true
			}
		}
	}
	return out
}

// linesNaming reads, for every line in the repositories that named the node at any commit, what
// each commit an edges run analyzed it at says it declared, and which repository holds it.
func (s *Store) linesNaming(node int64, repositories []string, standing map[string]map[string]bool,
) (map[lineAt]lineState, map[int64]string, error) {
	names, err := json.Marshal(repositories)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.db.Query(`
		WITH counted(repository) AS (SELECT value FROM json_each(?2)),
		lines(edge_row_id, repository) AS (
			SELECT DISTINCT e.row_id, LOWER(r.repository_full_name)
			  FROM repository r
			  JOIN abom_edge_regular e ON e.repository_row_id = r.row_id
			  JOIN abom_edge_sighting sg ON sg.edge_row_id = e.row_id AND sg.dst_node_row_id = ?1
			 WHERE LOWER(r.repository_full_name) IN (SELECT repository FROM counted)
			   AND `+dependencyKind("COALESCE(sg.edge_kind, e.edge_kind)")+`
		)
		SELECT l.edge_row_id, l.repository, LOWER(q.repository), LOWER(q.db_commit),
		       sg.dst_node_row_id IS NOT NULL, COALESCE(sg.dst_node_row_id = ?1, 0),
		       COALESCE(sg.ref_value, '')
		  FROM lines l
		  JOIN abom_edge_sighting sg ON sg.edge_row_id = l.edge_row_id
		  JOIN abom_query_run q ON q.row_id = sg.query_run_id AND q.analysis_mode = 'regular'
		 WHERE COALESCE(q.db_commit, '') <> ''`, node, string(names))
	if err != nil {
		return nil, nil, fmt.Errorf("reading the lines naming node %d: %w", node, err)
	}
	defer rows.Close()
	states, owners := map[lineAt]lineState{}, map[int64]string{}
	for rows.Next() {
		var at lineAt
		var owner, ref string
		var known, namesNode bool
		if err := rows.Scan(&at.edge, &owner, &at.repository, &at.commit, &known, &namesNode, &ref); err != nil {
			return nil, nil, fmt.Errorf("scanning a line's sighting: %w", err)
		}
		if commits, tracked := standing[at.repository]; tracked && !commits[at.commit] {
			continue
		}
		owners[at.edge] = owner
		st := states[at]
		st.known = st.known || known
		if namesNode && !slices.Contains(st.names, ref) {
			st.names = append(st.names, ref)
		}
		states[at] = st
	}
	return states, owners, rows.Err()
}

// findingsOnLineCommits reads the findings on each line's file and line number, by the commit of
// every regular run that sighted them.
func (s *Store) findingsOnLineCommits(states map[lineAt]lineState) (map[lineAt][]lineHit, error) {
	edges := []int64{}
	for at := range states {
		if !slices.Contains(edges, at.edge) {
			edges = append(edges, at.edge)
		}
	}
	ids, err := json.Marshal(edges)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT DISTINCT e.row_id, LOWER(fq.repository), LOWER(fq.db_commit), f.row_id, `+
		bandSQL("f.security_severity")+`
		  FROM abom_edge_regular e
		  JOIN abom_finding_regular f ON f.repository_row_id = e.repository_row_id
		   AND f.file_path = e.file_path AND f.start_line = e.start_line
		  JOIN abom_finding_sighting fs ON fs.finding_row_id = f.row_id
		  JOIN abom_query_run fq ON fq.row_id = fs.query_run_id AND fq.analysis_mode = 'regular'
		 WHERE e.row_id IN (SELECT value FROM json_each(?)) AND COALESCE(fq.db_commit, '') <> ''`,
		string(ids))
	if err != nil {
		return nil, fmt.Errorf("reading the findings on lines: %w", err)
	}
	defer rows.Close()
	out := map[lineAt][]lineHit{}
	for rows.Next() {
		var at lineAt
		var hit lineHit
		if err := rows.Scan(&at.edge, &at.repository, &at.commit, &hit.finding, &hit.band); err != nil {
			return nil, fmt.Errorf("scanning a finding on a line: %w", err)
		}
		out[at] = append(out[at], hit)
	}
	return out, rows.Err()
}

// findingsRunCommits names the repository commits among the lines' where a findings run reached.
// A run older than the first sighting on record is no evidence either way, as FindingsAtCommits
// reads it: its sightings, had it made any, were never written.
func (s *Store) findingsRunCommits(states map[lineAt]lineState) (map[[2]string]bool, error) {
	repositories := []string{}
	for at := range states {
		if !slices.Contains(repositories, at.repository) {
			repositories = append(repositories, at.repository)
		}
	}
	names, err := json.Marshal(repositories)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT DISTINCT LOWER(q.repository), LOWER(q.db_commit)
		  FROM abom_query_run q
		 WHERE q.analysis_mode = 'regular' AND COALESCE(q.db_commit, '') <> ''
		   AND LOWER(q.repository) IN (SELECT value FROM json_each(?))
		   AND q.ran_at >= (SELECT MIN(fq.ran_at) FROM abom_finding_sighting fs
		                      JOIN abom_query_run fq ON fq.row_id = fs.query_run_id)
		   AND `+findingsRun("q"), string(names))
	if err != nil {
		return nil, fmt.Errorf("reading which commits a findings run reached: %w", err)
	}
	defer rows.Close()
	out := map[[2]string]bool{}
	for rows.Next() {
		var repository, commit string
		if err := rows.Scan(&repository, &commit); err != nil {
			return nil, fmt.Errorf("scanning a commit a findings run reached: %w", err)
		}
		out[[2]string{repository, commit}] = true
	}
	return out, rows.Err()
}

// tallyLineFindings counts one reference's answer. The lines are those of a counted repository
// that named the reference at some commit; a finding counts where its line named the reference at
// its commit, and is unknown where nothing recorded what the line declared there.
func tallyLineFindings(ref string, repos map[string]bool, states map[lineAt]lineState,
	owners map[int64]string, hits map[lineAt][]lineHit, analyzed map[[2]string]bool,
) (LineFindings, bool) {
	var out LineFindings
	lines := map[int64]bool{}
	for at, st := range states {
		if repos[owners[at.edge]] && slices.Contains(st.names, ref) {
			lines[at.edge] = true
		}
	}
	commits, found, unknown := map[[2]string]bool{}, map[int64]string{}, map[int64]bool{}
	for at, st := range states {
		if !lines[at.edge] {
			continue
		}
		switch {
		case slices.Contains(st.names, ref):
			commits[[2]string{at.repository, at.commit}] = true
			for _, h := range hits[at] {
				found[h.finding] = h.band
			}
		case !st.known:
			for _, h := range hits[at] {
				unknown[h.finding] = true
			}
		}
	}
	for commit := range commits {
		if analyzed[commit] {
			out.Analyzed++
		}
	}
	if out.Analyzed == 0 {
		return out, false
	}
	out.Commits, out.Count = len(commits), len(found)
	for _, band := range found {
		out.Severity.add(band, 1)
	}
	for finding := range unknown {
		if _, ok := found[finding]; !ok {
			out.Unknown++
		}
	}
	return out, true
}
