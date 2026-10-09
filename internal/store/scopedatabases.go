package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// How a scope's database resolved. A reader deciding whether to press something expensive needs
// "already here" and "would have to be extracted" apart, and neither confused with "cannot".
const (
	// DatabaseRetained is on disk now, so a query can run against it without building anything.
	DatabaseRetained = "retained"
	// DatabaseBuildable is a repository and commit ABOM has analyzed before whose database is not
	// held. Running against it costs an extraction, not a question about whether it is possible.
	DatabaseBuildable = "buildable"
)

// ScopedDatabase is one database a scope resolves to.
type ScopedDatabase struct {
	DatabaseIdentity
	Status string `json:"status"`
	// Path and Bytes are the retained database's, and empty for one that would have to be built.
	// A size nobody has measured is absent rather than zero.
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	// CLIVersion is the CodeQL that built what is held, empty where nothing recorded one.
	CLIVersion string `json:"cliVersion"`
	// Reason says why a database that is held resolves as buildable, and is empty otherwise.
	Reason string `json:"reason"`
}

// DatabaseGap is a repository the scope reached that resolves to no database, and why.
type DatabaseGap struct {
	Repository string `json:"repository"`
	Reason     string `json:"reason"`
}

// DatabaseSet is the databases a scope names, and what it could not name.
//
// The gaps are part of the answer rather than an omission from it: a scope naming forty
// repositories of which nine were never analyzed resolves to thirty-one databases and says so.
// The number a reader needs before pressing anything expensive is the one that is missing.
type DatabaseSet struct {
	Databases []ScopedDatabase `json:"databases"`
	Gaps      []DatabaseGap    `json:"gaps"`
	// Mode is the shape that was asked for, echoed so an empty answer says which question it is
	// empty about.
	Mode string `json:"mode"`
	// Unresolved explains an answer holding neither a database nor a gap. Without it, a scope
	// asking for a shape nothing has ever been built in is indistinguishable from a scope that
	// genuinely matched nothing -- and the two call for opposite responses.
	Unresolved string `json:"unresolved"`
}

// BuiltBy re-reads a resolution against the CodeQL that would run it.
//
// The executor rebuilds a database another CodeQL built, so counting one as held before Run
// states a cost that is not the cost spent. A row that records no CodeQL is not known to be this
// one's either. With no CodeQL to compare against, nothing is moved: inventing a mismatch would
// send every run to rebuild everything.
func (d DatabaseSet) BuiltBy(cli string) DatabaseSet {
	if cli == "" {
		return d
	}
	databases := make([]ScopedDatabase, 0, len(d.Databases))
	for _, db := range d.Databases {
		if db.Status == DatabaseRetained && db.CLIVersion != cli {
			db.Status, db.Path, db.Bytes = DatabaseBuildable, "", 0
			db.Reason = "held, but no CodeQL is recorded as having built it"
			if db.CLIVersion != "" {
				db.Reason = "held, but CodeQL " + db.CLIVersion + " built it"
			}
		}
		databases = append(databases, db)
	}
	d.Databases = databases
	return d
}

// DatabasesMatching answers which databases a selector's findings were, or would be, found in.
//
// The sibling of NodesMatching, over the same predicate deliberately: *what am I looking at* and
// *what am I about to run this against* are one question asked in two directions, and a separate
// syntax for the second would let them drift apart.
//
// It builds nothing and it promises nothing about what a build would cost. An empty selector
// resolves to nothing for the reason NodesMatching does: a policy that failed to parse must run
// against nothing rather than the whole organization.
func (s *Store) DatabasesMatching(sel FindingSelector) (DatabaseSet, error) {
	out := noDatabases(sel.Mode)
	mode := out.Mode
	if sel.namesOnlyRepositories() {
		repositories, err := s.canonicalRepositories(sel.Repositories)
		if err != nil {
			return out, err
		}
		return s.databasesFor(out, repositories, mode)
	}
	if !sel.asked() {
		return out, nil
	}

	match, err := s.NodesMatching(sel)
	if err != nil {
		return out, err
	}
	named := map[string]bool{}
	for _, n := range match.Nodes {
		for _, repo := range n.Repositories {
			named[repo] = true
		}
	}
	repositories := make([]string, 0, len(named))
	for repo := range named {
		repositories = append(repositories, repo)
	}
	// Sorted so resolving twice over an unchanged corpus names the same databases in the same
	// order: a reader comparing a resolution against the one they were shown is comparing lists.
	sort.Strings(repositories)

	if len(repositories) == 0 {
		out.Unresolved, err = s.whyNothingMatched(mode)
		return out, err
	}
	return s.databasesFor(out, repositories, mode)
}

// DatabasesListed resolves a run over what the repositories unit lists for q to the databases it
// covers, narrowed by the selector's names and findings.
//
// The selector narrows and never widens: a listing holding nothing resolves to nothing, because
// handing the set path no repositories would narrow by none.
func (s *Store) DatabasesListed(q RepositoriesQuery, sel FindingSelector) (DatabaseSet, error) {
	out := noDatabases(sel.Mode)
	listed, err := s.listedRepositories(q)
	if err != nil {
		return out, err
	}
	if len(listed) == 0 {
		out.Unresolved = "the repositories unit lists no repository for this scope"
		return out, nil
	}
	if len(sel.Repositories) > 0 {
		if listed = namedAmong(listed, sel.Repositories); len(listed) == 0 {
			out.Unresolved = "no repository named is among those this scope lists"
			return out, nil
		}
	}
	// The listing applied the grouping; applying it again through findings would turn a list of
	// repositories into a question about what was found in them.
	sel.Repositories, sel.Properties, sel.ExcludeProperties = listed, nil, nil
	if sel.namesOnlyRepositories() {
		return s.databasesFor(out, listed, out.Mode)
	}
	return s.DatabasesMatching(sel)
}

// noDatabases is an answer holding nothing yet, in the shape asked for.
func noDatabases(mode string) DatabaseSet {
	if mode == "" {
		mode = AnalysisModeRegular
	}
	return DatabaseSet{Databases: []ScopedDatabase{}, Gaps: []DatabaseGap{}, Mode: mode}
}

// namedAmong keeps the listed repositories a name asks for, in any case it was typed.
func namedAmong(listed, names []string) []string {
	asked := map[string]bool{}
	for _, name := range names {
		asked[strings.ToLower(name)] = true
	}
	out := []string{}
	for _, repo := range listed {
		if asked[strings.ToLower(repo)] {
			out = append(out, repo)
		}
	}
	return out
}

// namesOnlyRepositories reports a bare repository list; findings would never reach a clean one.
func (f FindingSelector) namesOnlyRepositories() bool {
	return len(f.Repositories) > 0 && len(f.Rules) == 0 && len(f.Exclude) == 0 &&
		len(f.Tags) == 0 && len(f.NotTags) == 0 &&
		f.Provider == "" && f.Severity == "" && len(f.SecuritySeverities) == 0 &&
		len(f.Sources) == 0 && len(f.Properties) == 0 &&
		len(f.ExcludeProperties) == 0
}

// canonicalRepositories spells each name the way the corpus does, sorted and once each.
func (s *Store) canonicalRepositories(names []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, name := range names {
		canonical := name
		err := s.db.QueryRow(`
			SELECT repository_full_name FROM repository
			WHERE LOWER(repository_full_name) = LOWER(?) LIMIT 1`, name).Scan(&canonical)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("looking up %s: %w", name, err)
		}
		if !seen[canonical] {
			seen[canonical] = true
			out = append(out, canonical)
		}
	}
	sort.Strings(out)
	return out, nil
}

// databasesFor resolves each repository to a held database, a buildable one, or a gap saying why.
func (s *Store) databasesFor(out DatabaseSet, repositories []string, mode string) (DatabaseSet, error) {
	for _, repo := range repositories {
		held, err := s.retainedFor(repo, mode)
		if err != nil {
			return out, err
		}
		if held != nil {
			out.Databases = append(out.Databases, *held)
			continue
		}
		commit, filter, err := s.lastAnalyzedAt(repo, mode)
		if err != nil {
			return out, err
		}
		if commit == "" {
			out.Gaps = append(out.Gaps, DatabaseGap{
				Repository: repo,
				Reason: fmt.Sprintf(
					"no %s analysis on record, so nothing names a commit to build from", mode),
			})
			continue
		}
		out.Databases = append(out.Databases, ScopedDatabase{
			DatabaseIdentity: DatabaseIdentity{
				Repository: repo, Commit: commit, SourceFilter: filter, Mode: mode,
			},
			Status: DatabaseBuildable,
		})
	}
	return out, nil
}

// whyNothingMatched separates a shape nothing has been built in from a predicate that genuinely
// matched nothing.
//
// The distinction is the whole point of refusing to fall back: a scope asking for `extended`
// against a corpus of `regular` databases has not found zero problems, it has asked a question no
// evidence can answer, and handing it the regular set would answer a different one silently.
func (s *Store) whyNothingMatched(mode string) (string, error) {
	var runs int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM abom_query_run WHERE analysis_mode = ?`, mode).Scan(&runs); err != nil {
		return "", fmt.Errorf("counting %s runs: %w", mode, err)
	}
	if runs == 0 {
		return fmt.Sprintf(
			"nothing has been analyzed in %s mode, so no database can answer this scope", mode), nil
	}
	return fmt.Sprintf("no node in %s mode carries a finding this scope names", mode), nil
}

// retainedFor reports the held database for a repository in one shape, or nil.
// Most recently used rather than most recent commit: the cache holds one row per identity, and
// where a repository has several the one a run just touched is the one a run is working through.
func (s *Store) retainedFor(repository, mode string) (*ScopedDatabase, error) {
	rows, err := s.db.Query(`
		SELECT `+retainedDatabaseColumns+` FROM abom_local_database
		WHERE repository = ? AND analysis_mode = ?
		ORDER BY used_seq DESC, row_id DESC LIMIT 1`, repository, mode)
	if err != nil {
		return nil, fmt.Errorf("reading the retained database for %s: %w", repository, err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, rows.Err()
	}
	d, err := scanRetainedDatabase(rows)
	if err != nil {
		return nil, fmt.Errorf("scanning the retained database for %s: %w", repository, err)
	}
	return &ScopedDatabase{
		DatabaseIdentity: d.DatabaseIdentity,
		Status:           DatabaseRetained,
		Path:             d.Path,
		Bytes:            d.Bytes,
		CLIVersion:       d.CLIVersion,
	}, rows.Err()
}

// lastAnalyzedAt reports the commit and checkout filter a repository was most recently analyzed
// at in one shape, which is what a database would be rebuilt from.
//
// Runs with no commit are skipped rather than returned empty: a row that cannot name a revision
// cannot name a database either, and returning one would resolve a scope to something unbuildable
// while reporting no gap.
func (s *Store) lastAnalyzedAt(repository, mode string) (string, string, error) {
	var commit, filter string
	rows, err := s.db.Query(`
		SELECT db_commit, COALESCE(source_filter, '')
		FROM abom_query_run
		WHERE repository = ? AND analysis_mode = ? AND COALESCE(db_commit, '') <> ''
		ORDER BY ran_at DESC, row_id DESC LIMIT 1`, repository, mode)
	if err != nil {
		return "", "", fmt.Errorf("reading the analysis history for %s: %w", repository, err)
	}
	defer rows.Close()

	if rows.Next() {
		if err := rows.Scan(&commit, &filter); err != nil {
			return "", "", fmt.Errorf("scanning the analysis history for %s: %w", repository, err)
		}
	}
	return commit, filter, rows.Err()
}
