package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// DeclaredCommit is a commit the queue stands at, with the analyzed commits whose sightings say what
// it declared.
type DeclaredCommit struct {
	Repository, Commit string
	// Answers are the analyzed commits that stand for it, as a walk reads them: itself, or a commit
	// whose indexed content is identical. None means nothing analyzed it.
	Answers []string
	// Unknown is a commit whose answering runs predate sighting content, in a repository analyzed at
	// more than one commit, so what it declared was not kept.
	Unknown bool
	// Owned is the repository being one the operator declared.
	Owned bool
	// Changed is when the newest run answering for it ran.
	Changed string
}

// DeclaredCommits lists every commit the queue stands at, lowercased, by repository and commit.
func (s *Store) DeclaredCommits() ([]DeclaredCommit, error) {
	ev, err := s.commitEvidence()
	if err != nil {
		return nil, err
	}
	live, err := s.liveCommits(ev)
	if err != nil {
		return nil, err
	}
	owned := map[string]bool{}
	ownedRows, err := queryAll(s, "reading which repositories are owned", func(r *sql.Rows) (struct {
		repository string
		owned      bool
	}, error) {
		var o struct {
			repository string
			owned      bool
		}
		return o, r.Scan(&o.repository, &o.owned)
	}, `SELECT DISTINCT LOWER(f.owner || '/' || f.repo), `+ownedFullName("(f.owner || '/' || f.repo)")+`
	      FROM abom_frontier f
	     WHERE COALESCE(f.owner, '') <> '' AND COALESCE(f.repo, '') <> ''`)
	if err != nil {
		return nil, err
	}
	for _, o := range ownedRows {
		owned[o.repository] = owned[o.repository] || o.owned
	}
	ranRows, err := queryAll(s, "reading when each commit was analyzed", func(r *sql.Rows) ([2]string, error) {
		var at [2]string
		return at, r.Scan(&at[0], &at[1])
	}, `SELECT LOWER(q.repository) || '@' || LOWER(q.db_commit), MAX(q.ran_at)
	      FROM abom_query_run q
	     WHERE q.analysis_mode = 'regular' AND COALESCE(q.db_commit, '') <> '' AND `+edgesRun+`
	     GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	ran := make(map[string]string, len(ranRows))
	for _, r := range ranRows {
		ran[r[0]] = r[1]
	}

	out := []DeclaredCommit{}
	for repository, commits := range live {
		for commit, answers := range commits {
			d := DeclaredCommit{Repository: repository, Commit: commit, Answers: answers,
				Unknown: ev.unknown(repository, commit), Owned: owned[repository]}
			for _, a := range answers {
				d.Changed = max(d.Changed, ran[repository+"@"+a])
			}
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repository != out[j].Repository {
			return out[i].Repository < out[j].Repository
		}
		return out[i].Commit < out[j].Commit
	})
	return out, nil
}

// Declaration is one dependency a location in a commit's files declared, as ABOM recorded it.
type Declaration struct {
	File string
	Line int
	Kind string
	// Raw is the `uses:` value as the file wrote it.
	Raw string
	// Target is the node it names and Ref what it resolves; both empty where nothing was recorded.
	Target, Ref string
}

// CommitDeclarations reads what analyzed commits of a repository declared, as the graph reads it:
// each sighting that kept content, with its own kind, and a row at its own writer's commit
// otherwise. A location two of the commits declared differently is two declarations.
func (s *Store) CommitDeclarations(repository string, commits []string) ([]Declaration, error) {
	if len(commits) == 0 {
		return nil, nil
	}
	args := []any{strings.ToLower(repository)}
	for _, c := range commits {
		args = append(args, strings.ToLower(c))
	}
	return queryAll(s, "reading what "+repository+" declared", func(r *sql.Rows) (Declaration, error) {
		var d Declaration
		return d, r.Scan(&d.File, &d.Line, &d.Kind, &d.Raw, &d.Target, &d.Ref)
	}, `WITH `+observedIn(AnalysisModeRegular)+`
		SELECT DISTINCT COALESCE(e.file_path, ''), COALESCE(x.start_line, 0), e.edge_kind, e.ref_raw,
		       COALESCE(n.node_key, ''), e.ref
		  FROM observed e
		  JOIN repository r ON r.row_id = e.repository_row_id
		  JOIN abom_edge_any_mode x ON x.row_id = e.edge_row_id
		  LEFT JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
		 WHERE LOWER(r.repository_full_name) = ? AND `+DependencyEdgeFilter+`
		   AND e.commit_sha IN (`+strings.TrimSuffix(strings.Repeat("?, ", len(commits)), ", ")+`)
		 ORDER BY 1, 2, 3, 4, 5, 6`, args...)
}

// String is a declaration as a reader names it.
func (d Declaration) String() string {
	return fmt.Sprintf("%s:%d %s", d.File, d.Line, d.Raw)
}
