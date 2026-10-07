package store

import (
	"slices"
	"strings"
	"testing"
	"time"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

func setupYes() *bool { v := true; return &v }
func setupNo() *bool  { v := false; return &v }

// setupFixture declares octo and solo, and gives each named repository the evidence its name says.
func setupFixture(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo", Kind: AccountOrganization, Enabled: true},
		{Owner: "solo", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	ids := map[string]int64{}
	for _, name := range []string{
		"other/app", "octo/gone", "octo/conflict", "octo/advanced", "octo/history", "octo/locked",
		"octo/unread", "octo/covered", "octo/plain", "octo/unlisted", "octo/uncrawled",
		"octo/blocked", "octo/nopolicy", "octo/held", "octo/add", "octo/enable", "octo/undetected",
		"solo/enable", "octo/rusty",
	} {
		id, err := s.InsertOwnedRepository(name)
		if err != nil {
			t.Fatalf("InsertOwnedRepository(%s): %v", name, err)
		}
		ids[name] = id
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB().Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO abom_repository_absence (repository, missing_at) VALUES ('octo/gone', '2026-10-01T00:00:00Z')`)
	exec(`INSERT INTO abom_node (node_key, node_kind) VALUES ('github/codeql-action/init', 'action')`)
	exec(`INSERT INTO abom_node_reach (node_row_id, repository_row_id, hops)
	      SELECT row_id, ?, 1 FROM abom_node WHERE node_key = 'github/codeql-action/init'`, ids["octo/conflict"])
	for _, name := range []string{"octo/covered", "octo/plain", "octo/blocked", "octo/nopolicy", "octo/held",
		"octo/add", "octo/enable", "octo/undetected", "solo/enable", "octo/history", "octo/rusty"} {
		exec(`INSERT INTO abom_frontier (node_key, ref, depth, status, enqueued_at) VALUES (?, '', 0, 'done', ?)`,
			name, at.Format(time.RFC3339))
	}
	exec(`INSERT INTO abom_frontier (node_key, ref, depth, status, enqueued_at) VALUES ('octo/uncrawled', '', 0, 'pending', ?)`,
		at.Format(time.RFC3339))

	facts := []ListedFacts{}
	for _, name := range []string{"octo/conflict", "octo/advanced", "octo/history", "octo/covered", "octo/uncrawled",
		"octo/blocked", "octo/nopolicy", "octo/held", "octo/add", "octo/enable", "octo/undetected", "solo/enable",
		"octo/rusty"} {
		facts = append(facts, ListedFacts{Repository: name, Fork: setupNo(), Visibility: "private",
			HasWorkflows: setupYes(), HasActionsDir: setupNo(), HasRootAction: setupNo()})
	}
	facts = append(facts, ListedFacts{Repository: "octo/plain", Fork: setupNo(), Visibility: "public",
		HasWorkflows: setupNo(), HasActionsDir: setupNo(), HasRootAction: setupNo()})
	facts = append(facts, ListedFacts{Repository: "octo/unlisted", Fork: setupNo(), Visibility: "public"})
	if err := s.RecordListedFacts(facts, at); err != nil {
		t.Fatalf("RecordListedFacts: %v", err)
	}

	read := func(name, state string, languages ...string) {
		t.Helper()
		if err := s.RecordSetupReading(SetupReading{Repository: name, ReadAt: at, State: state,
			Languages: languages, QuerySuite: "default", UpdatedAt: "2026-09-01T00:00:00Z"}); err != nil {
			t.Fatalf("RecordSetupReading(%s): %v", name, err)
		}
	}
	read("octo/conflict", "configured", "javascript-typescript")
	read("octo/advanced", "not-configured", "actions")
	read("octo/history", "configured", "python")
	read("octo/covered", "configured", "actions", "python")
	read("octo/plain", "not-configured")
	read("octo/unlisted", "configured", "python")
	read("octo/uncrawled", "configured", "python")
	read("octo/blocked", "not-configured", "actions")
	read("octo/nopolicy", "not-configured", "actions")
	read("octo/held", "configured", "python")
	read("octo/add", "configured", "python")
	read("octo/enable", "not-configured", "actions", "python")
	read("octo/undetected", "not-configured", "python")
	read("octo/rusty", "configured", "python", "rust")
	read("solo/enable", "not-configured", "actions")
	if err := s.RecordSetupReading(SetupReading{Repository: "octo/locked", ReadAt: at,
		Unreadable: "Advanced Security must be enabled for this repository"}); err != nil {
		t.Fatalf("RecordSetupReading(locked): %v", err)
	}
	if err := s.RecordActionsPolicy("octo/nopolicy", "Actions are disabled for this repository", at); err != nil {
		t.Fatalf("RecordActionsPolicy: %v", err)
	}

	for _, a := range []AnalysesSummary{
		{Repository: "octo/advanced", ReadAt: at, AdvancedKey: ".github/workflows/codeql.yml:analyze",
			AdvancedAt: "2026-09-30T00:00:00Z", ActionsAdvancedKey: ".github/workflows/codeql.yml:analyze",
			ActionsAdvancedAt: "2026-09-30T00:00:00Z"},
		// Older than its default setup, so it is history rather than a workflow still uploading.
		{Repository: "octo/history", ReadAt: at, AdvancedKey: ".github/workflows/codeql.yml:analyze",
			AdvancedAt: "2026-01-01T00:00:00Z"},
		{Repository: "octo/enable", ReadAt: at},
	} {
		if err := s.RecordAnalysesSummary(a); err != nil {
			t.Fatalf("RecordAnalysesSummary(%s): %v", a.Repository, err)
		}
	}

	if err := s.ReplaceSecurityConfigurations("octo", at, []SecurityConfiguration{
		{ID: 1, Name: "GitHub recommended", Enforcement: "enforced", DefaultSetup: "enabled"},
		{ID: 2, Name: "Off", Enforcement: "enforced", DefaultSetup: "disabled"},
		{ID: 3, Name: "Legacy", Enforcement: "unenforced", DefaultSetup: "not_set"},
	}, []ConfigurationAttachment{
		{Repository: "octo/held", ConfigurationID: 1, Status: "enforced"},
		{Repository: "octo/blocked", ConfigurationID: 2, Status: "enforced"},
		{Repository: "octo/add", ConfigurationID: 3, Status: "attached"},
	}); err != nil {
		t.Fatalf("ReplaceSecurityConfigurations: %v", err)
	}
	if err := s.UpsertCatalogEntry(CatalogEntry{Repository: "octo/covered", Language: "actions",
		CommitOID: "abc", HasDatabase: true}); err != nil {
		t.Fatalf("UpsertCatalogEntry: %v", err)
	}
	if err := s.UpsertCatalogEntry(CatalogEntry{Repository: "octo/enable", Language: "actions"}); err != nil {
		t.Fatalf("UpsertCatalogEntry: %v", err)
	}
	return s
}

func TestEveryRepositoryGetsTheFirstClassItsEvidenceEarns(t *testing.T) {
	s := setupFixture(t)
	classes, err := s.SetupClasses()
	if err != nil {
		t.Fatalf("SetupClasses: %v", err)
	}
	got := map[string]string{}
	for _, c := range classes {
		got[c.Repository] = c.Class
	}
	want := map[string]string{
		"other/app":       SetupNotOwned,
		"octo/gone":       SetupNotListed,
		"octo/conflict":   SetupConflict,
		"octo/advanced":   SetupAdvanced,
		"octo/history":    SetupAdd,
		"octo/locked":     SetupUnreadable,
		"octo/unread":     SetupNotRead,
		"octo/covered":    SetupCovered,
		"octo/plain":      SetupNoActionsCode,
		"octo/unlisted":   SetupNotRead,
		"octo/uncrawled":  SetupNotCrawled,
		"octo/blocked":    SetupBlocked,
		"octo/nopolicy":   SetupActionsNotAllowed,
		"octo/held":       SetupAdd,
		"octo/add":        SetupAdd,
		"octo/enable":     SetupEnable,
		"octo/undetected": SetupNotDetected,
		"octo/rusty":      SetupLanguageNotWritable,
		"solo/enable":     SetupNotRead,
	}
	for repo, class := range want {
		if got[repo] != class {
			t.Errorf("%s: class %q, want %q", repo, got[repo], class)
		}
	}
	if len(got) != len(want) {
		t.Errorf("classified %d repositories, want %d: %v", len(got), len(want), got)
	}
}

func TestTheColumnsSayWhatWasReadAndNeverGuessTheRest(t *testing.T) {
	s := setupFixture(t)
	classes, err := s.SetupClasses("octo/covered", "octo/advanced", "octo/enable", "octo/plain",
		"octo/unread", "octo/conflict", "other/app")
	if err != nil {
		t.Fatalf("SetupClasses: %v", err)
	}
	type cols struct{ scanning, setup, database string }
	got := map[string]cols{}
	for _, c := range classes {
		got[c.Repository] = cols{c.ActionsScanning, c.Setup, c.ActionsDatabase}
	}
	want := map[string]cols{
		"octo/covered":  {"default", "default", "present"},
		"octo/advanced": {"advanced", "advanced", "not-read"},
		"octo/enable":   {"off", "none", "absent"},
		"octo/plain":    {"no-actions-code", "none", "not-read"},
		"octo/unread":   {"unknown", "not-read", "not-read"},
		"octo/conflict": {"unknown", "conflict", "not-read"},
		"other/app":     {"not-owned", "not-owned", "not-owned"},
	}
	for repo, w := range want {
		if got[repo] != w {
			t.Errorf("%s: columns %+v, want %+v", repo, got[repo], w)
		}
	}
	for _, c := range classes {
		if c.Repository == "octo/covered" && !slices.Equal(c.Languages, []string{"actions", "python"}) {
			t.Errorf("covered languages %v, want them as read", c.Languages)
		}
	}
}

func TestAListingThatCouldNotLookKeepsWhatAnEarlierOneSaw(t *testing.T) {
	s := newTestStore(t)
	first := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := s.RecordListedFacts([]ListedFacts{{Repository: "octo/app", Fork: setupNo(), Visibility: "PRIVATE",
		HasWorkflows: setupYes(), HasActionsDir: setupNo(), HasRootAction: setupYes()}}, first); err != nil {
		t.Fatalf("RecordListedFacts: %v", err)
	}
	if err := s.RecordListedFacts([]ListedFacts{{Repository: "octo/app", Fork: setupYes(), Visibility: "internal"}},
		first.Add(time.Hour)); err != nil {
		t.Fatalf("RecordListedFacts: %v", err)
	}
	f, ok, err := s.ListedFactsOf("octo/app")
	if err != nil || !ok {
		t.Fatalf("ListedFactsOf: %v %v", ok, err)
	}
	if f.HasWorkflows == nil || !*f.HasWorkflows || f.HasRootAction == nil || !*f.HasRootAction {
		t.Errorf("a REST listing erased what the tree lookups saw: %+v", f)
	}
	if f.Fork == nil || !*f.Fork || f.Visibility != "internal" {
		t.Errorf("fork and visibility are what the newest listing said, got %+v", f)
	}
	if !f.TreesReadAt.Equal(first) || !f.ListedAt.Equal(first.Add(time.Hour)) {
		t.Errorf("trees read %v and listed %v, want %v and %v", f.TreesReadAt, f.ListedAt, first, first.Add(time.Hour))
	}
}

func TestAConfigurationReadThatFailsLeavesTheLastAnswerStanding(t *testing.T) {
	s := setupFixture(t)
	later := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	if err := s.RecordConfigurationsUnread("octo", later, "403 Forbidden"); err != nil {
		t.Fatalf("RecordConfigurationsUnread: %v", err)
	}
	classes, err := s.SetupClasses("octo/held")
	if err != nil || len(classes) != 1 {
		t.Fatalf("SetupClasses: %v %v", classes, err)
	}
	c := classes[0]
	if c.Class != SetupAdd || c.ConfigurationName != "GitHub recommended" || c.ConfigurationsUnread != "403 Forbidden" {
		t.Errorf("after a failed read: %+v", c)
	}

	if err := s.ReplaceSecurityConfigurations("octo", later, nil, nil); err != nil {
		t.Fatalf("ReplaceSecurityConfigurations: %v", err)
	}
	classes, err = s.SetupClasses("octo/held")
	if err != nil || len(classes) != 1 {
		t.Fatalf("SetupClasses: %v %v", classes, err)
	}
	if classes[0].Class != SetupAdd || classes[0].ConfigurationName != "" {
		t.Errorf("an answer naming no attachment replaces the last one: %+v", classes[0])
	}
}

func TestTheRepositoriesUnitCarriesEachRepositorysCodeScanningColumns(t *testing.T) {
	s := setupFixture(t)
	page, err := s.RepositoriesPage(RepositoriesQuery{Limit: 100})
	if err != nil {
		t.Fatalf("RepositoriesPage: %v", err)
	}
	byName := map[string]RepositoryCodeScanning{}
	for _, item := range page.Items {
		byName[item.FullName] = item.CodeScanning
	}
	covered := byName["octo/covered"]
	if !covered.Known || covered.Class != SetupCovered || covered.ActionsScanning != "default" ||
		covered.Setup != "default" || covered.ActionsDatabase != "present" || covered.DatabaseCommit != "abc" ||
		covered.Evidence == "" || covered.ReadAt == "" {
		t.Errorf("octo/covered carries %+v", covered)
	}
	if held := byName["octo/held"]; held.ConfigurationName != "GitHub recommended" || held.AttachmentStatus != "enforced" {
		t.Errorf("octo/held carries %+v, want its enforced configuration named", held)
	}
	if unread := byName["octo/unread"]; !unread.Known || unread.Setup != "not-read" || unread.ReadAt != "" {
		t.Errorf("octo/unread carries %+v, want a known row that says it was not read", unread)
	}

	sorted, err := s.RepositoriesPage(RepositoriesQuery{Limit: 100, Sort: "actionsScanning"})
	if err != nil {
		t.Fatalf("sorting by actionsScanning: %v", err)
	}
	if first := sorted.Items[0].CodeScanning.ActionsScanning; first != "off" {
		t.Errorf("ascending Actions scanning starts with %q, want the gaps (off) first", first)
	}
	for _, sort := range []string{"setup", "actionsDatabase"} {
		if _, err := s.RepositoriesPage(RepositoriesQuery{Limit: 100, Sort: sort}); err != nil {
			t.Errorf("sorting by %s: %v", sort, err)
		}
	}
}

func TestTheRepositoriesUnitNarrowsToTheClassesAsked(t *testing.T) {
	s := setupFixture(t)
	page, err := s.RepositoriesPage(RepositoriesQuery{Limit: 100, CodeScanning: []string{SetupAdd, SetupEnable}})
	if err != nil {
		t.Fatalf("RepositoriesPage: %v", err)
	}
	got := []string{}
	for _, item := range page.Items {
		got = append(got, item.FullName)
	}
	slices.Sort(got)
	if want := []string{"octo/add", "octo/enable", "octo/held", "octo/history"}; !slices.Equal(got, want) || page.Total != 4 {
		t.Errorf("add and enable listed %v (total %d), want %v", got, page.Total, want)
	}
}

// The filter language and the classification must name the same classes, or a saved view could
// ask for one the store never answers.
func TestTheFilterVocabularyIsTheClassification(t *testing.T) {
	if got := viewspec.Vocabulary("codeScanning"); !slices.Equal(got, SetupClassOrder) {
		t.Errorf("filter vocabulary %v, classification %v", got, SetupClassOrder)
	}
}

// A setup column sorts the states it measures. Not owned, and no Actions code under Actions scanning,
// are not on its scale, so they follow every state that is in both directions: 204 rows reading "not
// owned" opened the descending sort, ahead of every repository the column says anything about.
func TestASetupSortKeepsWhatItDoesNotMeasureLastBothWays(t *testing.T) {
	s := setupFixture(t)
	value := func(cs RepositoryCodeScanning, sort string) string {
		switch sort {
		case "actionsScanning":
			return cs.ActionsScanning
		case "setup":
			return cs.Setup
		}
		return cs.ActionsDatabase
	}
	for sort, outside := range map[string][]string{
		"actionsScanning": {"not-owned", "no-actions-code"},
		"setup":           {"not-owned"},
		"actionsDatabase": {"not-owned"},
	} {
		for _, desc := range []bool{false, true} {
			page, err := s.RepositoriesPage(RepositoriesQuery{Limit: 100, Sort: sort, Desc: desc})
			if err != nil {
				t.Fatalf("sorting by %s: %v", sort, err)
			}
			measured, unmeasured := 0, 0
			for _, item := range page.Items {
				if slices.Contains(outside, value(item.CodeScanning, sort)) {
					unmeasured++
					continue
				}
				measured++
				if unmeasured > 0 {
					t.Errorf("sorting by %s (desc=%v) lists %s (%s) after a row the column does not measure",
						sort, desc, item.FullName, value(item.CodeScanning, sort))
				}
			}
			if measured == 0 || unmeasured == 0 {
				t.Fatalf("sorting by %s listed %d measured and %d unmeasured rows; the fixture proves nothing",
					sort, measured, unmeasured)
			}
		}
	}
}

// Adding a language under an enforced configuration applies and keeps it enforced, measured on
// has-ghas/test-repo-2, so such a repository is written like any other; its evidence still names
// the configuration, because an approver should know whose policy governs it.
func TestAnEnforcedConfigurationDoesNotHoldAnAddButIsNamed(t *testing.T) {
	s := setupFixture(t)
	classes, err := s.SetupClasses("octo/held")
	if err != nil || len(classes) != 1 {
		t.Fatalf("SetupClasses: %v %v", classes, err)
	}
	if c := classes[0]; c.Class != SetupAdd || !strings.Contains(c.Evidence, "enforced configuration GitHub recommended") {
		t.Errorf("octo/held is %s: %q", c.Class, c.Evidence)
	}
}

// GitHub configures rust itself and its write refuses it (422, measured on has-ghas/test-repo-2), and a
// write has to send the whole list back, so actions cannot be added without removing rust.
func TestALanguageTheWriteRefusesMakesTheRepositoryUnwritable(t *testing.T) {
	s := setupFixture(t)
	classes, err := s.SetupClasses("octo/rusty")
	if err != nil || len(classes) != 1 {
		t.Fatalf("SetupClasses: %v %v", classes, err)
	}
	if c := classes[0]; c.Class != SetupLanguageNotWritable || !strings.Contains(c.Evidence, "rust") {
		t.Errorf("octo/rusty is %s: %q", c.Class, c.Evidence)
	}
}
