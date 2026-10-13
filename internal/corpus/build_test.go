package corpus

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The corpus exists to make before/after claims about real rows possible at all, so what it must
// contain is not "some data" but the specific shapes the ranked work needs. Each assertion below
// names one of them.
func builtCorpus(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "abom.db")
	if err := build(path, filepath.Join("..", "..", "testdata", "corpus", "fixtures"), "", nil); err != nil {
		t.Fatalf("build: %v", err)
	}
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("opening the corpus: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func count(t *testing.T, s *store.Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(query, args...).Scan(&n); err != nil && err != sql.ErrNoRows {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// The shape the crawled organization never had: every candidate there turned out to be a
// repository using its own composite action, so the rule about excluding a repository grouping
// had no data behind it.
func TestTheCorpusHoldsADependencyBetweenTwoDistinctOwnedRepositories(t *testing.T) {
	s := builtCorpus(t)

	n := count(t, s, `
		SELECT COUNT(*)
		FROM abom_edge e
		JOIN repository src ON src.row_id = e.repository_row_id
		JOIN abom_node dst  ON dst.row_id = e.dst_node_row_id
		JOIN repository dr  ON dr.repository_full_name = dst.owner || '/' || dst.repo
		WHERE src.origin = 'seed' AND dr.origin = 'seed'
		  AND LOWER(src.repository_full_name) <> LOWER(dr.repository_full_name)`)
	if n == 0 {
		t.Error("no edge runs between two distinct owned repositories")
	}
}

// All four, because `DependencyEdgeFilter` excludes one of them from every traversal and nothing
// can exercise that rule against a corpus holding only the other three.
func TestTheCorpusHoldsEveryEdgeKind(t *testing.T) {
	s := builtCorpus(t)

	for _, kind := range []string{
		"step-uses-action", "job-needs-job",
		"job-uses-reusable-workflow", "composite-step-uses-action",
	} {
		if count(t, s, `SELECT COUNT(*) FROM abom_edge WHERE edge_kind = ?`, kind) == 0 {
			t.Errorf("no %s edge", kind)
		}
	}
}

// Both readings, so a filter on how something is referenced has something to separate.
func TestTheCorpusHoldsBothReferenceStyles(t *testing.T) {
	s := builtCorpus(t)

	pinned := count(t, s,
		`SELECT COUNT(*) FROM abom_edge WHERE ref_value GLOB '[0-9a-f]*' AND LENGTH(ref_value) = 40`)
	floating := count(t, s,
		`SELECT COUNT(*) FROM abom_edge WHERE ref_value LIKE 'v%'`)
	if pinned == 0 || floating == 0 {
		t.Errorf("%d commit-pinned and %d floating references, want both", pinned, floating)
	}
}

// Both suites run, and they answer different questions: the experimental marker is what makes a
// clean repository distinguishable from an unanalyzed one, and the default suite is what gives the
// set real security findings to carry.
func TestTheDataSetCarriesBothAbomsOwnFindingsAndUpstreamOnes(t *testing.T) {
	s := builtCorpus(t)

	total := count(t, s, `SELECT COUNT(*) FROM abom_finding`)
	if total == 0 {
		t.Fatal("no findings, so neither suite ran")
	}
	if own := count(t, s,
		`SELECT COUNT(*) FROM abom_finding WHERE rule_id LIKE 'abom/%'`); own == 0 {
		t.Error("no marker findings, so a clean repository is indistinguishable from an unanalyzed one")
	}
	if upstream := count(t, s,
		`SELECT COUNT(*) FROM abom_finding WHERE rule_id NOT LIKE 'abom/%'`); upstream == 0 {
		t.Error("no upstream findings, so the set exercises no real rule")
	}

	// A finding that reaches no node is a real class rather than a fault: a rule reporting on a
	// file that names no `uses:` line has nothing in the graph to attach to. It is bounded here
	// because the coverage strip discloses it, and a set where most findings were unattributable
	// would make that disclosure the headline rather than the footnote.
	orphaned := count(t, s, `
		SELECT COUNT(*) FROM abom_finding f
		WHERE NOT EXISTS (SELECT 1 FROM abom_finding_node fn WHERE fn.finding_row_id = f.row_id)`)
	if orphaned == 0 {
		t.Error("every finding attaches, so nothing exercises the unattributable class")
	}
	if orphaned*2 > total {
		t.Errorf("%d of %d findings reach no node; attribution is the exception, not the rule",
			orphaned, total)
	}
}

func TestTheCorpusRetainsRecordedQueryDocumentationAndResultEvidence(t *testing.T) {
	database := builtCorpus(t)
	if count(t, database, `SELECT COUNT(*) FROM abom_query_run_rule
		WHERE COALESCE(json_extract(documentation_json, '$.helpMarkdown'), '') <> ''`) == 0 {
		t.Error("no recorded query help is available for review")
	}
	if missing := count(t, database, `SELECT COUNT(*) FROM abom_finding_regular finding
		WHERE NOT EXISTS (SELECT 1 FROM abom_finding_sighting sight
		WHERE sight.finding_row_id = finding.row_id AND sight.evidence_json IS NOT NULL)`); missing != 0 {
		t.Errorf("%d fixture findings lost their recorded result evidence", missing)
	}
}

// A replay says it is one. Its evaluations carry no CodeQL version on purpose, and without this
// they could not be told from one that failed to record it.
func TestTheCorpusSaysItRecordedEveryEvaluationItReplays(t *testing.T) {
	s := builtCorpus(t)
	if count(t, s, `SELECT COUNT(*) FROM abom_query_run`) == 0 {
		t.Fatal("the corpus records no evaluation")
	}
	if n := count(t, s, `SELECT COUNT(*) FROM abom_query_run WHERE COALESCE(recorded_by, '') <> ?`,
		store.RecordedByCorpus); n != 0 {
		t.Errorf("%d of the corpus's evaluations do not say the corpus recorded them", n)
	}
}

// A rebuild replaces rather than accumulates, or a second run and a fresh checkout disagree.
func TestBuildingTwiceProducesTheSameCorpus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	fixtures := filepath.Join("..", "..", "testdata", "corpus", "fixtures")

	read := func() [2]int {
		if err := build(path, fixtures, "", nil); err != nil {
			t.Fatalf("build: %v", err)
		}
		s, err := store.Open(path, store.Options{})
		if err != nil {
			t.Fatalf("opening: %v", err)
		}
		defer s.Close()
		return [2]int{
			count(t, s, `SELECT COUNT(*) FROM abom_node_live`),
			count(t, s, `SELECT COUNT(*) FROM abom_edge_live`),
		}
	}
	if first, second := read(), read(); first != second {
		t.Errorf("rebuild changed the corpus: %v then %v", first, second)
	}
}

// The path comes from a flag and is echoed back, so a newline in it would forge a line of output
// that reads like the tool's own. Quoting is what stops that, and `%q` also keeps a path with a
// space in it readable.
func TestAPathCannotForgeALineOfOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abom.db")
	if err := build(path, filepath.Join("..", "..", "testdata", "corpus", "fixtures"), "", nil); err != nil {
		t.Fatalf("build: %v", err)
	}
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var out bytes.Buffer
	forged := path + "\ncorpus: /etc/shadow"
	if err := report(&out, s, forged); err != nil {
		t.Fatalf("report: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "corpus: ") {
		t.Fatalf("last line is %q", last)
	}
	if strings.Contains(last, "/etc/shadow") && !strings.Contains(last, `\n`) {
		t.Errorf("the newline was not escaped, so the path forged a line: %q", last)
	}
}

// The one fact `make corpus` could not produce, and the reason the populated browser run could
// never be green: an advisory is not CodeQL output, so no number of added repositories creates
// one. It comes from GHSA, and the corpus records a real response beside the recorded queries.
func TestTheCorpusHoldsAnAdvisoryThatReachesSomethingItUses(t *testing.T) {
	s := builtCorpus(t)

	if n := count(t, s, `SELECT COUNT(*) FROM abom_advisory`); n == 0 {
		t.Fatal("no advisory recorded, so the advisories unit has nothing to draw")
	}

	// Reaching matters more than existing. An advisory naming a package nothing uses leaves the
	// unit as empty as no advisory at all, which is the state this exists to leave behind.
	reaching := count(t, s, `
		SELECT COUNT(DISTINCT a.ghsa_id)
		FROM abom_advisory a
		JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		JOIN abom_node n ON n.owner || '/' || n.repo = p.package_name
		WHERE a.withdrawn_at IS NULL`)
	if reaching == 0 {
		t.Error("no advisory names anything the corpus uses, so the unit stays empty")
	}
}

// Direct and transitive are different work — a version bump here, or waiting on somebody else —
// and a unit whose two columns hold the same number for every row teaches a reader nothing.
func TestAnAdvisoryIsReachedBothDirectlyAndThroughAnotherRepository(t *testing.T) {
	s := builtCorpus(t)

	// The composite action is the only genuine transitive hop Actions has, so an advisory
	// reached through it is the only way these two columns can differ at all.
	through := count(t, s, `
		SELECT COUNT(*)
		FROM abom_advisory_package p
		JOIN abom_node dst ON dst.owner || '/' || dst.repo = p.package_name
		JOIN abom_edge e   ON e.dst_node_row_id = dst.row_id
		WHERE e.edge_kind = 'composite-step-uses-action'`)
	if through == 0 {
		t.Error("no advisory is reached through a composite action, so nothing is transitive")
	}

	direct := count(t, s, `
		SELECT COUNT(*)
		FROM abom_advisory_package p
		JOIN abom_node dst ON dst.owner || '/' || dst.repo = p.package_name
		JOIN abom_edge e   ON e.dst_node_row_id = dst.row_id
		WHERE e.edge_kind = 'step-uses-action'`)
	if direct == 0 {
		t.Error("no advisory is named directly by a workflow step")
	}
}

// Job-level facts come from a second query, and recording only the edges left every column the
// interface draws about a job — permissions, triggers, runner, privilege — zero for every row.
// A column that is zero everywhere reads as a measurement, not as a shape nobody recorded.
func TestTheDataSetCarriesWhatAJobCanDo(t *testing.T) {
	s := builtCorpus(t)

	if n := count(t, s, `SELECT COUNT(*) FROM abom_observation`); n == 0 {
		t.Fatal("no job facts recorded, so every job-level column reads zero")
	}
	privileged := count(t, s,
		`SELECT COUNT(*) FROM abom_observation WHERE name = 'privileged' AND value = 'true'`)
	if privileged == 0 {
		t.Error("no job can write, so the privilege column is zero for every row it lists")
	}
	// And not every job, or the column is a constant in the other direction.
	if unprivileged := count(t, s,
		`SELECT COUNT(*) FROM abom_observation WHERE name = 'privileged' AND value = 'false'`); unprivileged == 0 {
		t.Error("every job is privileged, so the column distinguishes nothing")
	}
}

// notRepresented are suite rules the data set cannot yet produce, each with the reason.
var notRepresented = map[string]string{
	// The `/medium` variants need a lower-trust trigger than the imported trees carry.
	"actions/argument-injection/medium": "no lower-trust trigger context for argument injection",
	"actions/artifact-poisoning/medium": "no lower-trust trigger context for artifact poisoning",
	"actions/command-injection/medium":  "no lower-trust trigger context for command injection",
	"actions/envpath-injection/medium":  "no lower-trust trigger context for envpath injection",
	"actions/envvar-injection/medium":   "no lower-trust trigger context for envvar injection",
	// Needs a reference to an action GitHub publishes as immutable, which none of the imported
	// trees names and which cannot be invented: the rule reads a specific set.
	"actions/unversioned-immutable-action": "no immutable action is referenced",
}

// The shared data set is ABOM's org-level answer to a per-query unit test: rather than an
// `.expected` file for each query, the whole set of findings is mapped onto one fixed graph. That
// only means something if the set can produce each finding, so this names the ones it cannot.
func TestTheDataSetProducesEveryRuleTheSuiteCanReport(t *testing.T) {
	expected, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpus", "suite-rules.txt"))
	if err != nil {
		t.Fatalf("reading the recorded suite rules: %v", err)
	}

	s := builtCorpus(t)
	produced := map[string]bool{}
	rows, err := s.DB().Query(`SELECT DISTINCT rule_id FROM abom_finding`)
	if err != nil {
		t.Fatalf("reading rules: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning a rule: %v", err)
		}
		produced[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading rules: %v", err)
	}

	for _, rule := range strings.Split(strings.TrimSpace(string(expected)), "\n") {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		reason, excused := notRepresented[rule]
		switch {
		case produced[rule] && excused:
			t.Errorf("%s is produced now but still listed as unrepresented (%q); remove it",
				rule, reason)
		case !produced[rule] && !excused:
			t.Errorf("no repository in the data set produces %s, and nothing says why", rule)
		}
	}
}

// A finding-generating example teaches nothing on its own: a rule that fires everywhere is
// indistinguishable from a rule that is always on. The set carries clean repositories too.
func TestTheDataSetHoldsRepositoriesWithNoSecurityFindingAtAll(t *testing.T) {
	s := builtCorpus(t)

	// The coverage marker is excluded: it reports on every job by design, so counting it would
	// make every repository look flagged and no repository could ever read as clean.
	clean := count(t, s, `
		SELECT COUNT(*) FROM repository r
		WHERE NOT EXISTS (
			SELECT 1 FROM abom_finding f
			WHERE f.repository_row_id = r.row_id AND f.rule_id NOT LIKE 'abom/%')`)
	if clean == 0 {
		t.Error("every repository carries a finding, so nothing shows what clean looks like")
	}
	flagged := count(t, s,
		`SELECT COUNT(DISTINCT repository_row_id) FROM abom_finding WHERE rule_id NOT LIKE 'abom/%'`)
	if flagged == 0 {
		t.Error("no repository carries a finding")
	}
	t.Logf("%d repositories clean, %d carrying at least one security finding", clean, flagged)
}

// The browser gate can draw a property scope only if the corpus declares one: an organization
// restricted to the repositories carrying chosen values, one scope naming the value every
// repository nobody set carries, and one held because repository administrators can set it.
func TestTheCorpusDeclaresAnOrganizationByProperty(t *testing.T) {
	s := builtCorpus(t)
	declared, err := s.OwnedScope()
	if err != nil {
		t.Fatal(err)
	}
	var acme *store.DeclaredAccount
	for i := range declared {
		if declared[i].Owner == "acme-corp" {
			acme = &declared[i]
		}
	}
	if acme == nil || !acme.IncludesOnly || len(acme.Properties) != 3 {
		t.Fatalf("acme-corp declared as %+v, want three property scopes", acme)
	}
	var held, defaults, honored int
	for _, c := range acme.Properties {
		switch {
		case c.Held:
			held++
			if !strings.Contains(c.HeldReason, "repository administrators") {
				t.Errorf("held for %q", c.HeldReason)
			}
		case len(c.Defaults) > 0:
			defaults++
		default:
			honored++
		}
	}
	if held != 1 || defaults != 1 || honored != 1 {
		t.Errorf("%d held, %d naming a default, %d otherwise; want one of each", held, defaults, honored)
	}
	// The scopes cover every acme-corp repository between them, so no owned figure moves.
	for _, repo := range []string{"acme-corp/payments", "acme-corp/shared-ci", "acme-corp/storefront"} {
		if owned, err := s.RepositoryIsOwned(repo); err != nil || !owned {
			t.Errorf("%s owned %v (%v), want owned", repo, owned, err)
		}
	}
	for terms, want := range map[store.PropertyTerm]int{
		{Name: "team", Value: "payments"}: 1, {Name: "review-tier", Value: "standard"}: 2,
	} {
		if got, err := s.PropertyScopeMatches("acme-corp", []store.PropertyTerm{terms}); err != nil || got != want {
			t.Errorf("%s = %s matches %d (%v), want %d", terms.Name, terms.Value, got, err, want)
		}
	}
	if got := count(t, s, `SELECT COUNT(*) FROM abom_enumeration_state
		WHERE owner = 'acme-corp' AND properties_judged_at IS NOT NULL`); got != 1 {
		t.Error("the scopes carry no judgement, so the interface would say they were never judged")
	}
}
