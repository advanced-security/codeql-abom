// Command abom-corpus builds a populated ABOM database from the fixtures under testdata/corpus.
//
// Separate from the shipped binary because it is a development tool, and offline by construction:
// the expensive, non-deterministic half of a corpus is CodeQL, and its output is recorded beside
// the source trees by scripts/regenerate-corpus-fixtures.sh. Rebuilding is then pure Go, so CI can
// hold a populated database rather than gating only the empty half.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/advisory"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/lockfile"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func main() {
	dbPath := flag.String("db", os.Getenv("ABOM_DB"), "SQLite path or empty PostgreSQL URL (required; defaults to ABOM_DB)")
	fixtures := flag.String("fixtures", "testdata/corpus/fixtures", "recorded CodeQL output")
	// The states a component catalog would otherwise exist to draw. A database nobody can build is
	// the only reason to keep a second rendering of every component alive.
	without := flag.String("without", "",
		"comma-separated facts to leave out: advisories, findings")
	// The operator's query pack a server is given with --custom-ql-dir, laid out beside the
	// database: without one no custom query, mark or session can be drawn.
	queries := flag.String("queries", "", "directory to lay the corpus query pack out in, replacing it")
	record := flag.String("record-queries", "",
		"run the corpus query pack's tests with the CodeQL CLI and write what it said to this file")
	flag.Parse()
	if *record != "" {
		if err := recordQueries(*record); err != nil {
			fmt.Fprintf(os.Stderr, "abom-corpus: recording the query pack: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "abom-corpus: --db is required")
		os.Exit(2)
	}
	if err := build(*dbPath, *fixtures, *queries, omitted(*without)); err != nil {
		fmt.Fprintf(os.Stderr, "abom-corpus: %v\n", err)
		os.Exit(1)
	}
}

// omitted reads the --without list into a set.
func omitted(list string) map[string]bool {
	out := map[string]bool{}
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out[name] = true
		}
	}
	return out
}

func build(dbPath, fixtures, queries string, without map[string]bool) error {
	if err := resetCorpusDatabase(dbPath); err != nil {
		return err
	}

	s, err := openCorpusStore(dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	if store.IsPostgresTarget(dbPath) {
		summary, err := s.Summary()
		if err != nil {
			return err
		}
		owned, err := s.OwnedScope()
		if err != nil {
			return err
		}
		if summary.Nodes != 0 || summary.Edges != 0 || summary.Findings != 0 ||
			summary.SeedRepos != 0 || summary.UpstreamRepos != 0 || len(owned) != 0 {
			return fmt.Errorf("refusing to replay fixtures into a populated PostgreSQL database")
		}
	}

	repos, err := recorded(fixtures)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return fmt.Errorf("no fixtures under %q; run scripts/regenerate-corpus-fixtures.sh", fixtures)
	}

	if err := s.SetOwnedScope(declaredAccounts(repos), store.DeclaredByCLI); err != nil {
		return fmt.Errorf("declaring the fixture accounts: %w", err)
	}
	if err := ingestFixtureRepositories(s, fixtures, repos, without); err != nil {
		return err
	}
	if err := recordPropertyScopeFacts(s); err != nil {
		return fmt.Errorf("recording what the property scopes judge: %w", err)
	}
	if err := recordCorpusAdvisories(s, fixtures, without); err != nil {
		return err
	}
	if err := rebuildCorpusGraph(s); err != nil {
		return err
	}
	if queries != "" {
		if err := seedQueries(s, fixtures, queries); err != nil {
			return fmt.Errorf("laying out the query pack: %w", err)
		}
	}

	return report(os.Stdout, s, dbPath)
}

func resetCorpusDatabase(dbPath string) error {
	if store.IsPostgresTarget(dbPath) {
		return nil
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.MkdirAll(filepath.Dir(dbPath), 0o755)
}

func openCorpusStore(dbPath string) (store.Backend, error) {
	s, err := store.OpenBackend(dbPath, store.Options{})
	if err != nil {
		return nil, fmt.Errorf("opening %q: %w", store.DatabaseLabel(dbPath), err)
	}
	if err := s.Migrate(); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("migrating: %w", err)
	}
	return s, nil
}

func ingestFixtureRepositories(
	s store.Backend, fixtures string, repos []string, without map[string]bool,
) error {
	for _, r := range repos {
		if err := ingestRepository(s, fixtures, r, without); err != nil {
			return fmt.Errorf("%q: %w", r, err)
		}
	}
	return nil
}

func recordCorpusAdvisories(s store.Backend, fixtures string, without map[string]bool) error {
	if without["advisories"] {
		return nil
	}
	return recordAdvisories(s, filepath.Join(filepath.Dir(fixtures), "advisories.json"))
}

func rebuildCorpusGraph(s store.Backend) error {
	if _, err := s.ClassifyEdgeBoundaries(); err != nil {
		return fmt.Errorf("classifying boundaries: %w", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		return fmt.Errorf("linking findings: %w", err)
	}
	if err := (&graph.Builder{Store: s}).RebuildAggregates(); err != nil {
		return fmt.Errorf("rebuilding aggregates: %w", err)
	}
	return nil
}

// declaredAccounts names every account the fixtures describe.
//
// Read from the trees rather than fixed in code, so adding an account is adding a directory.
// Declared rather than inferred, because nothing in ABOM happens until an operator claims
// ownership, and a data set that skipped that step would exercise a path no install takes.
// More than one, because an estate of a single account cannot exercise anything that compares
// two — and the interface groups by account throughout.
func declaredAccounts(repos []string) []store.DeclaredAccount {
	seen := map[string]bool{}
	owners := []string{}
	for _, r := range repos {
		owner, _, ok := strings.Cut(r, "/")
		if !ok || seen[owner] {
			continue
		}
		seen[owner] = true
		owners = append(owners, owner)
	}
	sort.Strings(owners)

	out := make([]store.DeclaredAccount, 0, len(owners))
	for _, owner := range owners {
		a := store.DeclaredAccount{Owner: owner, Kind: "organization", Enabled: true}
		if owner == propertyScopedAccount {
			a.IncludesOnly, a.Properties = true, propertyScopes
		}
		out = append(out, a)
	}
	return out
}

// propertyScopedAccount is declared by property rather than whole. Its scopes cover every one of
// its repositories between them, so no owned figure differs from declaring it whole.
const propertyScopedAccount = "acme-corp"

// The value acme-corp gives every repository nobody set otherwise.
const corpusDefaultTier = "standard"

var propertyScopes = []store.PropertyClause{
	{Terms: []store.PropertyTerm{{Name: "team", Value: "payments"}}},
	{Terms: []store.PropertyTerm{{Name: "review-tier", Value: corpusDefaultTier}}},
	// Declared before the schema said repository administrators can set it, so it is held.
	{Terms: []store.PropertyTerm{{Name: "cost-center", Value: "7"}}},
}

// recordPropertyScopeFacts writes what a listing and a schema read would have, after the
// declaration, which is the order they arrive in.
func recordPropertyScopeFacts(s store.Backend) error {
	if err := s.ReplacePropertySchema(propertyScopedAccount, []store.PropertyDefinition{
		{Name: "team", ValueType: "single_select", ValuesEditableBy: store.PropertyEditableByOrganization},
		{Name: "review-tier", ValueType: "single_select", ValuesEditableBy: store.PropertyEditableByOrganization,
			HasDefault: true, DefaultValue: corpusDefaultTier},
		{Name: "cost-center", ValueType: "string", ValuesEditableBy: "org_and_repo_actors"},
	}); err != nil {
		return err
	}
	for repo, values := range map[string][]store.PropertyValue{
		"acme-corp/payments":   {{Name: "team", Value: "payments"}, {Name: "review-tier", Value: "elevated"}, {Name: "cost-center", Value: "7"}},
		"acme-corp/storefront": {{Name: "team", Value: "storefront"}, {Name: "review-tier", Value: corpusDefaultTier}, {Name: "cost-center", Value: "7"}},
		"acme-corp/shared-ci":  {{Name: "review-tier", Value: corpusDefaultTier}},
	} {
		if err := s.ReplacePropertyValues(repo, values); err != nil {
			return err
		}
	}
	return s.RecordPropertyJudgement(propertyScopedAccount, 0)
}

func ingestRepository(s store.Backend, fixtures, full string, without map[string]bool) error {
	if _, err := s.InsertOwnedRepository(full); err != nil {
		return fmt.Errorf("recording ownership: %w", err)
	}

	// The lockfile is not CodeQL output, so no recording carries it; it is read from the source
	// tree beside them. Keyed by the fixture provenance rather than a commit, because a corpus
	// tree has no revision and a synthesized SHA would read as one.
	lock, workflows, err := lockfile.Survey(filepath.Join(filepath.Dir(fixtures), full))
	if err != nil {
		return fmt.Errorf("reading the lockfile: %w", err)
	}
	if err := s.RecordLockReading(full, "corpus-fixture", lockfile.Reader, store.LockCoverageRows(
		string(lock.Coverage), lock.Schema, lock.Digest, lock.Reason, workflows),
		store.LockGraphOf(lock)); err != nil {
		return fmt.Errorf("recording lock coverage: %w", err)
	}

	dir := filepath.Join(fixtures, strings.ReplaceAll(full, "/", "__"))
	w := s.NewWriter(0)
	ing := ingest.New(s, w)

	edges, err := os.Open(filepath.Join(dir, "edges.json"))
	if err != nil {
		return err
	}
	defer edges.Close()
	if _, err := ing.EdgesFromBQRS(edges, ingest.Options{
		Repository:    full,
		RepositoryURL: "https://github.com/" + full,
		Provenance:    "corpus-fixture",
	}); err != nil {
		return fmt.Errorf("edges: %w", err)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flushing edges: %w", err)
	}

	// Skipped rather than ingested and hidden: --without=findings exists to render the state of
	// a corpus that has none, and a database that held them anyway would render the other one.
	if !without["findings"] {
		sarif, err := os.Open(filepath.Join(dir, "findings.sarif"))
		if err != nil {
			return err
		}
		defer sarif.Close()
		runID, err := s.RecordQueryRun(store.QueryRun{
			QueryID: "corpus-fixture/findings", QueryVersion: "corpus-fixture", Repository: full,
			AnalysisMode: store.AnalysisModeRegular, RecordedBy: store.RecordedByCorpus,
		})
		if err != nil {
			return fmt.Errorf("recording the findings replay: %w", err)
		}
		if _, err := ing.FindingsFromSARIF(sarif, ingest.Options{Repository: full, QueryRunID: runID}); err != nil {
			return fmt.Errorf("findings: %w", err)
		}
	}

	// Recorded later than the rest, so a fixture written before it stays loadable rather than
	// failing the whole build on a file nobody regenerated yet.
	surface, err := os.Open(filepath.Join(dir, "surface.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer surface.Close()

	// Every observation names the run that produced it. This one replays a recording, so it says
	// so and leaves the CLI version empty: nothing here witnessed which CodeQL wrote the file,
	// and naming one would attest to something nobody checked.
	runID, err := s.RecordQueryRun(store.QueryRun{
		QueryID:      "abom/surface",
		QueryVersion: "corpus-fixture",
		Repository:   full,
		// The recording replays one repository's own checkout, which is the shape it was taken
		// from.
		AnalysisMode: store.AnalysisModeRegular,
		RecordedBy:   store.RecordedByCorpus,
	})
	if err != nil {
		return fmt.Errorf("recording the fixture run: %w", err)
	}
	if _, err := ing.ObservationsFromBQRS(surface, ingest.Options{
		Repository: full, QueryRunID: runID,
	}); err != nil {
		return fmt.Errorf("surface: %w", err)
	}
	return nil
}

// recordAdvisories stores a recorded GHSA response, which is the one fact the recorded CodeQL
// output cannot carry.
//
// Absent rather than empty is a legitimate corpus: an operator who has never refreshed advisories
// has none, and that has to stay buildable. A malformed one is not, so it is reported.
func recordAdvisories(s store.Backend, path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading advisories: %w", err)
	}
	defer f.Close()

	advisories, err := advisory.Parse(f)
	if err != nil {
		return fmt.Errorf("parsing %q: %w", path, err)
	}
	if _, err := s.AddAdvisories(ingest.Advisories(advisories)); err != nil {
		return fmt.Errorf("recording advisories: %w", err)
	}
	return nil
}

// recorded lists the repositories the fixture directory holds, as `owner/name`.
func recorded(fixtures string) ([]string, error) {
	entries, err := os.ReadDir(fixtures)
	if err != nil {
		return nil, fmt.Errorf("reading %q: %w", fixtures, err)
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, strings.Replace(e.Name(), "__", "/", 1))
	}
	sort.Strings(out)
	return out, nil
}

// report prints what was built, because a builder that says only "done" is one nobody notices has
// started producing an empty database.
//
// The path is quoted rather than interpolated: it arrives from a flag, and a newline in it would
// otherwise forge a line that reads like the tool's own.
func report(w io.Writer, s store.Backend, dbPath string) error {
	counts, err := s.CorpusCounts()
	if err != nil {
		return err
	}
	for _, c := range counts {
		fmt.Fprintf(w, "%-20s %5d\n", c.Label, c.Count)
	}
	fmt.Fprintf(w, "corpus: %q\n", store.DatabaseLabel(dbPath))
	return nil
}
