package setup

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// fakeCatalog answers each read from its fields and counts what was asked.
type fakeCatalog struct {
	setup        catalog.DefaultSetup
	setupErr     error
	analyses     []catalog.Analysis
	analysesErr  error
	databases    []catalog.Database
	databasesErr error
	configs      []catalog.SecurityConfiguration
	configsErr   error
	attached     map[int64][]catalog.ConfigurationRepository
	repoConfig   catalog.RepositoryConfiguration
	repoErr      error
	actions      catalog.ActionsPermissions
	asked        []string
}

func (f *fakeCatalog) RepositoryConfiguration(_ context.Context, _ string) (catalog.RepositoryConfiguration, error) {
	f.asked = append(f.asked, "repository configuration")
	return f.repoConfig, f.repoErr
}

func (f *fakeCatalog) ActionsPermissions(_ context.Context, _ string) (catalog.ActionsPermissions, error) {
	f.asked = append(f.asked, "actions permissions")
	return f.actions, nil
}

func (f *fakeCatalog) DefaultSetup(_ context.Context, _ string) (catalog.DefaultSetup, error) {
	f.asked = append(f.asked, "setup")
	return f.setup, f.setupErr
}

func (f *fakeCatalog) CodeQLAnalyses(_ context.Context, _ string) ([]catalog.Analysis, error) {
	f.asked = append(f.asked, "analyses")
	return f.analyses, f.analysesErr
}

func (f *fakeCatalog) ListDatabases(_ context.Context, _ string) ([]catalog.Database, error) {
	f.asked = append(f.asked, "databases")
	return f.databases, f.databasesErr
}

func (f *fakeCatalog) SecurityConfigurations(_ context.Context, _ string) ([]catalog.SecurityConfiguration, error) {
	f.asked = append(f.asked, "configurations")
	return f.configs, f.configsErr
}

func (f *fakeCatalog) ConfigurationRepositories(_ context.Context, _ string, id int64) ([]catalog.ConfigurationRepository, error) {
	f.asked = append(f.asked, "attachments")
	return f.attached[id], nil
}

func ownedStore(t *testing.T, repositories ...string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "octo", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	for _, r := range repositories {
		if _, err := s.InsertOwnedRepository(r); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func classOf(t *testing.T, s *store.Store, repository string) store.SetupClass {
	t.Helper()
	classes, err := s.SetupClasses(repository)
	if err != nil || len(classes) != 1 {
		t.Fatalf("SetupClasses(%s): %v %v", repository, classes, err)
	}
	return classes[0]
}

var readAt = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func TestAReadingRecordsSetupDatabaseAndWhoRunsTheAnalyses(t *testing.T) {
	s := ownedStore(t, "octo/app")
	f := &fakeCatalog{
		setup: catalog.DefaultSetup{State: "not-configured", Languages: []string{"actions", "python"}},
		analyses: []catalog.Analysis{
			{Ref: "refs/pull/3/merge", Category: "/language:actions", AnalysisKey: ".github/workflows/codeql.yml:analyze",
				CreatedAt: "2026-10-04T00:00:00Z", CommitSHA: "p"},
			{Ref: "refs/heads/main", Category: "/language:actions", AnalysisKey: "dynamic/github-code-scanning/codeql:analyze",
				CreatedAt: "2026-10-03T00:00:00Z", CommitSHA: "d"},
			{Ref: "refs/heads/main", Category: "/language:python", AnalysisKey: ".github/workflows/codeql.yml:analyze",
				CreatedAt: "2026-10-02T00:00:00Z", CommitSHA: "a"},
		},
		databases: []catalog.Database{{Language: "python", CommitOID: "py"}, {Language: "actions", CommitOID: "act", Size: 9}},
	}
	r := Reader{Store: s, Catalog: f, Now: func() time.Time { return readAt }}
	if err := r.Read(context.Background(), "octo/app"); err != nil {
		t.Fatalf("Read: %v", err)
	}
	c := classOf(t, s, "octo/app")
	if c.State != "not-configured" || !slices.Equal(c.Languages, []string{"actions", "python"}) {
		t.Errorf("setup recorded as %s %v", c.State, c.Languages)
	}
	if c.AdvancedKey != ".github/workflows/codeql.yml:analyze" || c.AdvancedAt != "2026-10-04T00:00:00Z" {
		t.Errorf("advanced evidence %q at %q, want the newest analysis default setup did not make",
			c.AdvancedKey, c.AdvancedAt)
	}
	if c.ActionsDefaultAt != "2026-10-03T00:00:00Z" || c.ActionsAdvancedAt != "2026-10-04T00:00:00Z" {
		t.Errorf("Actions analyses: default at %q, advanced at %q", c.ActionsDefaultAt, c.ActionsAdvancedAt)
	}
	if c.ActionsDatabase != "present" || c.DatabaseCommit != "act" {
		t.Errorf("Actions database %s at %q, want present at act", c.ActionsDatabase, c.DatabaseCommit)
	}
}

// A refusal is GitHub answering about the repository, so it is recorded in GitHub's own words and
// nothing else is asked: every other read would be refused for the same reason.
func TestARefusedSetupIsRecordedInGitHubsWordsAndAsksNothingElse(t *testing.T) {
	s := ownedStore(t, "octo/app")
	f := &fakeCatalog{setupErr: &catalog.StatusError{Code: 403,
		Message: "Advanced Security must be enabled for this repository to use code scanning."}}
	r := Reader{Store: s, Catalog: f, Now: func() time.Time { return readAt }}
	if err := r.Read(context.Background(), "octo/app"); err != nil {
		t.Fatalf("a refusal is an answer, not a failure: %v", err)
	}
	c := classOf(t, s, "octo/app")
	if c.Class != store.SetupUnreadable || c.Unreadable != "Advanced Security must be enabled for this repository to use code scanning." {
		t.Errorf("recorded %s %q", c.Class, c.Unreadable)
	}
	if !slices.Equal(f.asked, []string{"setup"}) {
		t.Errorf("asked %v after a refusal, want the setup read alone", f.asked)
	}
}

func TestAReadThatFailsLeavesTheLastReadingStanding(t *testing.T) {
	s := ownedStore(t, "octo/app")
	f := &fakeCatalog{setup: catalog.DefaultSetup{State: "configured", Languages: []string{"actions"}}}
	r := Reader{Store: s, Catalog: f, Now: func() time.Time { return readAt }}
	if err := r.Read(context.Background(), "octo/app"); err != nil {
		t.Fatal(err)
	}
	f.setupErr = &catalog.StatusError{Code: 502, Body: "Bad Gateway"}
	r.Now = func() time.Time { return readAt.Add(time.Hour) }
	if err := r.Read(context.Background(), "octo/app"); err == nil {
		t.Fatal("a 502 is not an answer about the repository and must be reported")
	}
	c := classOf(t, s, "octo/app")
	if c.State != "configured" || c.SetupReadAt != readAt.Format(time.RFC3339Nano) {
		t.Errorf("after a failed read the reading is %s at %s, want the earlier one", c.State, c.SetupReadAt)
	}
}

// Default setup already analyzes Actions there, so who else does is not worth a request.
func TestACoveredRepositoryIsNotAskedForItsAnalyses(t *testing.T) {
	s := ownedStore(t, "octo/app")
	f := &fakeCatalog{setup: catalog.DefaultSetup{State: "configured", Languages: []string{"actions"}}}
	r := Reader{Store: s, Catalog: f, Now: func() time.Time { return readAt }}
	if err := r.Read(context.Background(), "octo/app"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.asked, "analyses") {
		t.Errorf("asked %v, want no analyses read for a covered repository", f.asked)
	}
}

func TestNoAnalysisAtAllIsAnAnswer(t *testing.T) {
	s := ownedStore(t, "octo/app")
	f := &fakeCatalog{setup: catalog.DefaultSetup{State: "not-configured", Languages: []string{"actions"}},
		analysesErr:  &catalog.NoAnalysisError{Repository: "octo/app", Reason: "no analysis found"},
		databasesErr: &catalog.StatusError{Code: 404}}
	r := Reader{Store: s, Catalog: f, Now: func() time.Time { return readAt }}
	if err := r.Read(context.Background(), "octo/app"); err != nil {
		t.Fatal(err)
	}
	c := classOf(t, s, "octo/app")
	if c.AnalysesReadAt == "" || c.AnalysesUnreadable != "" || c.AdvancedKey != "" {
		t.Errorf("no analysis recorded as read %q unreadable %q advanced %q", c.AnalysesReadAt, c.AnalysesUnreadable, c.AdvancedKey)
	}
	if c.ActionsDatabase != "absent" {
		t.Errorf("a database list GitHub answered 404 for is %s, want absent", c.ActionsDatabase)
	}
}

func TestConfigurationsAreRecordedAsTheAccountsWholeAnswer(t *testing.T) {
	s := ownedStore(t, "octo/app", "octo/other")
	f := &fakeCatalog{
		configs: []catalog.SecurityConfiguration{{ID: 17, Name: "GitHub recommended", Enforcement: "enforced", DefaultSetup: "enabled"}},
		attached: map[int64][]catalog.ConfigurationRepository{
			17: {{Repository: "octo/app", Status: "enforced"}, {Repository: "elsewhere/app", Status: "enforced"}}},
	}
	r := Reader{Store: s, Catalog: f, Now: func() time.Time { return readAt }}
	if err := r.ReadConfigurations(context.Background(), "octo"); err != nil {
		t.Fatal(err)
	}
	c := classOf(t, s, "octo/app")
	if c.ConfigurationName != "GitHub recommended" || c.AttachmentStatus != "enforced" || c.ConfigurationsAnswered == "" {
		t.Errorf("octo/app configuration %q status %q answered %q", c.ConfigurationName, c.AttachmentStatus, c.ConfigurationsAnswered)
	}

	f.configsErr = &catalog.StatusError{Code: 403, Message: "Resource not accessible by integration"}
	if err := r.ReadConfigurations(context.Background(), "octo"); err == nil {
		t.Fatal("a refused configuration read must be reported")
	}
	if c := classOf(t, s, "octo/app"); c.ConfigurationName != "GitHub recommended" ||
		c.ConfigurationsUnread != "Resource not accessible by integration" {
		t.Errorf("after a refusal: configuration %q unread %q", c.ConfigurationName, c.ConfigurationsUnread)
	}
}

func TestAPersonalAccountHoldsNoConfigurations(t *testing.T) {
	s := ownedStore(t, "octo/app")
	f := &fakeCatalog{configsErr: &catalog.StatusError{Code: 404}}
	r := Reader{Store: s, Catalog: f, Now: func() time.Time { return readAt }}
	if err := r.ReadConfigurations(context.Background(), "octo"); err != nil {
		t.Fatalf("a 404 is an account with no configurations to offer: %v", err)
	}
	if c := classOf(t, s, "octo/app"); c.ConfigurationsAnswered == "" {
		t.Error("an account that answered with none is still an answer")
	}
}

// An account whose configurations the App cannot read whole leaves enforcement unknown; the
// repository's own answer settles it for one request, and only for a candidate.
func TestACandidateWhoseAccountCannotBeReadAsksItsOwnConfiguration(t *testing.T) {
	s := ownedStore(t, "octo/app", "octo/covered")
	if _, err := s.DB().Exec(`INSERT INTO abom_frontier (node_key, ref, depth, status, enqueued_at)
	                           VALUES ('octo/app', '', 0, 'done', '2026-10-05T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	f := &fakeCatalog{setup: catalog.DefaultSetup{State: "not-configured", Languages: []string{"actions"}}}
	r := Reader{Store: s, Catalog: f, Now: func() time.Time { return readAt }}
	if err := r.Read(context.Background(), "octo/app"); err != nil {
		t.Fatal(err)
	}
	if c := classOf(t, s, "octo/app"); c.Class != store.SetupEnable || !slices.Contains(f.asked, "repository configuration") {
		t.Errorf("octo/app is %s after asking %v, want enable from its own answer", c.Class, f.asked)
	}

	f.asked, f.setup = nil, catalog.DefaultSetup{State: "configured", Languages: []string{"actions"}}
	if err := r.Read(context.Background(), "octo/covered"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.asked, "repository configuration") {
		t.Errorf("asked %v for a covered repository, which no configuration can change", f.asked)
	}
}

func TestActionsMustBeAbleToRunCodeQLForDefaultSetupToRun(t *testing.T) {
	for _, c := range []struct {
		perms catalog.ActionsPermissions
		want  bool
	}{
		{catalog.ActionsPermissions{Enabled: true, AllowedActions: "all"}, true},
		{catalog.ActionsPermissions{Enabled: false, AllowedActions: "all"}, false},
		{catalog.ActionsPermissions{Enabled: true, AllowedActions: "local_only"}, false},
		{catalog.ActionsPermissions{Enabled: true, AllowedActions: "selected",
			Selected: &catalog.SelectedActions{GitHubOwnedAllowed: true}}, true},
		{catalog.ActionsPermissions{Enabled: true, AllowedActions: "selected",
			Selected: &catalog.SelectedActions{PatternsAllowed: []string{"github/codeql-action/*"}}}, true},
		{catalog.ActionsPermissions{Enabled: true, AllowedActions: "selected",
			Selected: &catalog.SelectedActions{PatternsAllowed: []string{"actions/*"}}}, false},
	} {
		if got := ActionsPolicy(c.perms) == "allowed"; got != c.want {
			t.Errorf("%+v allowed = %v, want %v (%s)", c.perms, got, c.want, ActionsPolicy(c.perms))
		}
	}
}
