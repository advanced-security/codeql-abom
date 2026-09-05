package store

import (
	"strings"
	"testing"
)

// ABOM holds CodeQL query results; it never forms its own opinion about what those results
// mean. A verdict column is how that boundary erodes in practice, so the schema is checked
// directly rather than relying on review to catch it.
func TestSchemaHasNoVerdictColumns(t *testing.T) {
	s := newTestStore(t)

	forbidden := []string{
		"is_pinned", "pin_status", "ref_kind", "is_stale_comment",
		"is_vulnerable", "severity_class", "risk_level", "is_exempt",
	}

	rows, err := s.DB().Query(
		`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'abom_%'`)
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scanning table name: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("no abom_ tables found")
	}

	for _, table := range tables {
		cols, err := s.DB().Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatalf("reading columns of %s: %v", table, err)
		}
		for cols.Next() {
			var col string
			if err := cols.Scan(&col); err != nil {
				cols.Close()
				t.Fatalf("scanning column: %v", err)
			}
			for _, bad := range forbidden {
				if strings.EqualFold(col, bad) {
					t.Errorf("%s.%s is an ABOM-owned verdict; findings must be keyed by rule_id instead",
						table, col)
				}
			}
		}
		if err := cols.Err(); err != nil {
			cols.Close()
			t.Fatalf("reading columns of %s: %v", table, err)
		}
		cols.Close()
	}
}

// Findings must always name the rule that produced them, or results become unattributable.
func TestFindingRequiresRuleID(t *testing.T) {
	s := newTestStore(t)
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	if _, err := s.db.Exec(
		`INSERT INTO abom_finding (rule_id, repository_row_id) VALUES (NULL, ?)`, repoID); err == nil {
		t.Error("expected a NOT NULL violation for a finding without a rule id")
	}
}

func TestRecordQueryRunIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	run := QueryRun{
		QueryID: "abom/edges", QueryVersion: "0.1.0", PackVersion: "0.1.0",
		Repository: "octo-org/app", DBCommit: "abc123",
	}

	first, err := s.RecordQueryRun(run)
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	second, err := s.RecordQueryRun(run)
	if err != nil {
		t.Fatalf("RecordQueryRun (repeat): %v", err)
	}
	if first != second {
		t.Errorf("same run produced ids %d and %d", first, second)
	}
}

// A different query version is a different run, because its results may differ.
func TestQueryRunVersionDistinguishesRuns(t *testing.T) {
	s := newTestStore(t)
	base := QueryRun{QueryID: "abom/edges", Repository: "octo-org/app", QueryVersion: "1"}
	bumped := base
	bumped.QueryVersion = "2"

	a, err := s.RecordQueryRun(base)
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	b, err := s.RecordQueryRun(bumped)
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if a == b {
		t.Error("a query version change must produce a distinct run")
	}
}

func TestRecordQueryRunRejectsIncompleteInput(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.RecordQueryRun(QueryRun{Repository: "octo-org/app"}); err == nil {
		t.Error("expected an error when QueryID is missing")
	}
	if _, err := s.RecordQueryRun(QueryRun{QueryID: "abom/edges"}); err == nil {
		t.Error("expected an error when Repository is missing")
	}
}

func TestAddFindingsDeduplicates(t *testing.T) {
	s := newTestStore(t)
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	finding := Finding{
		RuleID: "actions/unpinned-tag", RepositoryID: repoID,
		FilePath: ".github/workflows/ci.yml", StartLine: 12, StartColumn: 9,
		Message: "unpinned", Severity: "warning", SecuritySeverity: 5.0,
	}
	for i := 0; i < 2; i++ {
		if _, err := s.AddFindings([]Finding{finding}); err != nil {
			t.Fatalf("AddFindings: %v", err)
		}
	}

	var count int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_finding`).Scan(&count)
	if count != 1 {
		t.Errorf("got %d findings after ingesting the same one twice, want 1", count)
	}
}

// The location join is what lets graph traversal answer security questions.
func TestLinkFindingsToNodes(t *testing.T) {
	s := newTestStore(t)
	repoID := seedGraph(t, s, NewWriter(s, 0))

	if _, err := s.AddFindings([]Finding{{
		RuleID: "actions/unpinned-tag", RepositoryID: repoID,
		FilePath: ".github/workflows/main.yml", StartLine: 16, StartColumn: 15,
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	linked, err := s.LinkFindingsToNodes()
	if err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}
	if linked != 1 {
		t.Errorf("linked %d findings, want 1", linked)
	}

	var nodeKind string
	if err := s.DB().QueryRow(`
		SELECT n.node_kind FROM abom_finding_node fn
		JOIN abom_node n ON n.row_id = fn.node_row_id`).Scan(&nodeKind); err != nil {
		t.Fatalf("reading link: %v", err)
	}
	if nodeKind != "step" {
		t.Errorf("finding attached to a %q node, want step", nodeKind)
	}
}

// Many rules report on a workflow or a job rather than on a uses: line. Leaving those findings
// unattached would hide most of a suite's output from every view built on the graph.
func TestLinkFindingsAttachesWorkflowLevelResultsToTheirWorkflow(t *testing.T) {
	s := newTestStore(t)
	repoID := seedGraph(t, s, NewWriter(s, 0))

	// Line 1 is the workflow header, which no step occupies.
	if _, err := s.AddFindings([]Finding{{
		RuleID: "actions/missing-workflow-permissions", RepositoryID: repoID,
		FilePath: ".github/workflows/main.yml", StartLine: 1, StartColumn: 1,
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}

	var nodeKind string
	if err := s.DB().QueryRow(`
		SELECT n.node_kind FROM abom_finding_node fn
		JOIN abom_node n ON n.row_id = fn.node_row_id
		JOIN abom_finding f ON f.row_id = fn.finding_row_id
		WHERE f.rule_id = 'actions/missing-workflow-permissions'`).Scan(&nodeKind); err != nil {
		t.Fatalf("reading link: %v", err)
	}
	if nodeKind != "workflow" {
		t.Errorf("workflow-level finding attached to a %q node, want workflow", nodeKind)
	}
}

// A finding that does land on a uses: line belongs to that step, not to the workflow around it.
func TestLinkFindingsPrefersTheStepOverItsWorkflow(t *testing.T) {
	s := newTestStore(t)
	repoID := seedGraph(t, s, NewWriter(s, 0))

	if _, err := s.AddFindings([]Finding{{
		RuleID: "actions/unpinned-tag", RepositoryID: repoID,
		FilePath: ".github/workflows/main.yml", StartLine: 16, StartColumn: 15,
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}

	var kinds int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_finding_node fn
		JOIN abom_finding f ON f.row_id = fn.finding_row_id
		WHERE f.rule_id = 'actions/unpinned-tag'`).Scan(&kinds); err != nil {
		t.Fatalf("counting links: %v", err)
	}
	if kinds != 1 {
		t.Errorf("attached to %d nodes, want only the step", kinds)
	}
}

// A reusable workflow's file is represented by the container that holds its jobs, so nothing at
// that path is a workflow node. Attribution has to walk out of the jobs and steps sitting there,
// or every result a rule reports against such a file stays invisible to the graph.
func TestFindingsAttachToTheContainerHoldingTheFile(t *testing.T) {
	s := newTestStore(t)
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	const path = ".github/workflows/reusable.yml"
	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "octo-org/app/" + path, Kind: "reusable_workflow", RepositoryID: repoID, Path: path},
		{Key: "job:octo-org/app:" + path + ":build", Kind: "job", RepositoryID: repoID,
			Path: path, ContainerKey: "octo-org/app/" + path},
		{Key: "step:octo-org/app:" + path + ":build:0", Kind: "step", RepositoryID: repoID,
			Path: path, ContainerKey: "job:octo-org/app:" + path + ":build"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Line 1 is the file header, which no step occupies.
	if _, err := s.AddFindings([]Finding{{
		RuleID: "actions/missing-workflow-permissions", RepositoryID: repoID,
		FilePath: path, StartLine: 1,
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}

	var kind string
	if err := s.DB().QueryRow(`
		SELECT n.node_kind FROM abom_finding_node fn
		JOIN abom_node n ON n.row_id = fn.node_row_id`).Scan(&kind); err != nil {
		t.Fatalf("reading link: %v", err)
	}
	if kind != "reusable_workflow" {
		t.Errorf("finding attached to a %q node, want the reusable_workflow holding the file", kind)
	}
}

// A file that names no dependency has no node in a graph built from dependencies. Inventing one
// so the count looks better would be reporting something that was never extracted.
func TestFindingsInUnmodeledFilesStayUnattached(t *testing.T) {
	s := newTestStore(t)
	repoID := seedGraph(t, s, NewWriter(s, 0))

	if _, err := s.AddFindings([]Finding{{
		RuleID: "actions/code-injection/medium", RepositoryID: repoID,
		FilePath: ".github/actions/no-dependencies/action.yml", StartLine: 4,
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}

	var links int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_finding_node fn
		JOIN abom_finding f ON f.row_id = fn.finding_row_id
		WHERE f.rule_id = 'actions/code-injection/medium'`).Scan(&links); err != nil {
		t.Fatalf("counting links: %v", err)
	}
	if links != 0 {
		t.Errorf("attached a finding to %d nodes for a file the graph never modeled", links)
	}
}

func TestLinkFindingsToNodesIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	repoID := seedGraph(t, s, NewWriter(s, 0))

	if _, err := s.AddFindings([]Finding{{
		RuleID: "actions/unpinned-tag", RepositoryID: repoID,
		FilePath: ".github/workflows/main.yml", StartLine: 16, StartColumn: 15,
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes (repeat): %v", err)
	}

	var links int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_finding_node`).Scan(&links)
	if links != 1 {
		t.Errorf("got %d links after linking twice, want 1", links)
	}
}
