// Package setup reads each owned repository's code scanning setup, classifies it, and adds
// `actions` to default setup only where an approved preview listed the repository.
package setup

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Catalog is the reads a Reader makes, all of them through the App's read-only grant.
type Catalog interface {
	DefaultSetup(ctx context.Context, repository string) (catalog.DefaultSetup, error)
	CodeQLAnalyses(ctx context.Context, repository string) ([]catalog.Analysis, error)
	ListDatabases(ctx context.Context, repository string) ([]catalog.Database, error)
	SecurityConfigurations(ctx context.Context, org string) ([]catalog.SecurityConfiguration, error)
	ConfigurationRepositories(ctx context.Context, org string, id int64) ([]catalog.ConfigurationRepository, error)
	RepositoryConfiguration(ctx context.Context, repository string) (catalog.RepositoryConfiguration, error)
	ActionsPermissions(ctx context.Context, repository string) (catalog.ActionsPermissions, error)
}

// Reader reads and records what GitHub says about code scanning setup.
type Reader struct {
	Store   *store.Store
	Catalog Catalog
	Now     func() time.Time
}

func (r Reader) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now()
}

// Read records one repository's default setup, its Actions database and who runs its CodeQL
// analyses. A refusal is recorded as the answer it is; any other failure leaves the last reading.
func (r Reader) Read(ctx context.Context, repository string) error {
	at := r.now()
	ds, err := r.Catalog.DefaultSetup(ctx, repository)
	if reason, refused := Refusal(err); refused {
		return r.Store.RecordSetupReading(store.SetupReading{Repository: repository, ReadAt: at, Unreadable: reason})
	}
	if err != nil {
		return err
	}
	if err := r.Store.RecordSetupReading(store.SetupReading{Repository: repository, ReadAt: at,
		State: ds.State, Languages: ds.Languages, QuerySuite: ds.QuerySuite, ThreatModel: ds.ThreatModel,
		RunnerType: ds.RunnerType, RunnerLabel: ds.RunnerLabel, Schedule: ds.Schedule,
		UpdatedAt: ds.UpdatedAt}); err != nil {
		return err
	}
	failed := []error{r.readDatabase(ctx, repository)}
	needed, err := r.needsAnalyses(repository)
	if err != nil {
		return err
	}
	if needed {
		failed = append(failed, r.readAnalyses(ctx, repository, at))
	}
	unsettled, err := r.enforcementUnknown(repository)
	if err != nil {
		return errors.Join(append(failed, err)...)
	}
	if unsettled {
		failed = append(failed, r.ReadConfiguration(ctx, repository))
	}
	return errors.Join(failed...)
}

// enforcementUnknown reports a candidate held only because its account's configurations could not
// be read, which the repository's own answer settles for one request.
func (r Reader) enforcementUnknown(repository string) (bool, error) {
	classes, err := r.Store.SetupClasses(repository)
	if err != nil || len(classes) != 1 {
		return false, err
	}
	c := classes[0]
	return c.Class == store.SetupNotRead && c.SetupReadAt != "" && c.ActionsCode && c.ConfigurationsAnswered == "", nil
}

// ReadConfiguration records the configuration one repository has attached, from its own answer.
func (r Reader) ReadConfiguration(ctx context.Context, repository string) error {
	got, err := r.Catalog.RepositoryConfiguration(ctx, repository)
	if err != nil {
		return err
	}
	var cfg *store.SecurityConfiguration
	if c := got.Configuration; c != nil {
		cfg = &store.SecurityConfiguration{ID: c.ID, Name: c.Name, TargetType: c.TargetType,
			Enforcement: c.Enforcement, DefaultSetup: c.DefaultSetup}
	}
	return r.Store.RecordRepositoryConfiguration(repository, cfg, got.Status, r.now())
}

// ReadActionsPolicy records whether Actions can run default setup in a repository. Only a write
// asks: default setup runs github/codeql-action, so a repository that cannot run it cannot be enabled.
func (r Reader) ReadActionsPolicy(ctx context.Context, repository string) error {
	perms, err := r.Catalog.ActionsPermissions(ctx, repository)
	if err != nil {
		return err
	}
	return r.Store.RecordActionsPolicy(repository, ActionsPolicy(perms), r.now())
}

// ActionsPolicy is "allowed", or why Actions cannot run github/codeql-action in the repository.
func ActionsPolicy(p catalog.ActionsPermissions) string {
	switch {
	case !p.Enabled:
		return "Actions is disabled in this repository, and default setup runs as an Actions workflow"
	case p.AllowedActions == "local_only":
		return "this repository allows only its own actions, and default setup runs github/codeql-action"
	case p.AllowedActions != "selected":
		return "allowed"
	case p.Selected == nil:
		return "this repository allows selected actions, and which ones could not be read"
	case p.Selected.GitHubOwnedAllowed:
		return "allowed"
	}
	for _, pattern := range p.Selected.PatternsAllowed {
		if matched, _ := path.Match(pattern, "github/codeql-action/init"); matched || pattern == "github/codeql-action/*" ||
			strings.HasPrefix(pattern, "github/codeql-action@") || pattern == "github/*" {
			return "allowed"
		}
	}
	return "this repository's allowed actions do not include github/codeql-action, which default setup runs"
}

// needsAnalyses spends the analyses request only where it can change an answer: default setup that
// already analyzes Actions, or a repository with no Actions code, is settled without it.
func (r Reader) needsAnalyses(repository string) (bool, error) {
	classes, err := r.Store.SetupClasses(repository)
	if err != nil || len(classes) != 1 {
		return false, err
	}
	switch classes[0].Class {
	case store.SetupCovered, store.SetupNoActionsCode, store.SetupNotOwned, store.SetupNotListed:
		return false, nil
	}
	return true, nil
}

func (r Reader) readDatabase(ctx context.Context, repository string) error {
	dbs, err := r.Catalog.ListDatabases(ctx, repository)
	if errors.Is(err, catalog.ErrNoDatabase) {
		return r.Store.UpsertCatalogEntry(store.CatalogEntry{Repository: repository, Language: actionsLanguage})
	}
	if _, refused := Refusal(err); refused {
		return nil
	}
	if err != nil {
		return err
	}
	entry := store.CatalogEntry{Repository: repository, Language: actionsLanguage}
	for _, db := range dbs {
		if strings.EqualFold(db.Language, actionsLanguage) {
			entry = store.CatalogEntry{Repository: repository, Language: actionsLanguage, CommitOID: db.CommitOID,
				SizeBytes: db.Size, CreatedAt: stamp(db.CreatedAt), UpdatedAt: stamp(db.UpdatedAt),
				HasDatabase: true}
			break
		}
	}
	return r.Store.UpsertCatalogEntry(entry)
}

func (r Reader) readAnalyses(ctx context.Context, repository string, at time.Time) error {
	analyses, err := r.Catalog.CodeQLAnalyses(ctx, repository)
	summary := store.AnalysesSummary{Repository: repository, ReadAt: at}
	switch reason, refused := Refusal(err); {
	case errors.Is(err, catalog.ErrNoAnalysis):
	case refused:
		summary.Unreadable = reason
	case err != nil:
		return err
	default:
		summary = Summarize(repository, at, analyses)
	}
	return r.Store.RecordAnalysesSummary(summary)
}

// Summarize reads a newest-first page of CodeQL analyses for who runs them. An analysis whose key
// default setup did not make is advanced setup, whatever ran it: a workflow, or a CLI elsewhere.
func Summarize(repository string, at time.Time, analyses []catalog.Analysis) store.AnalysesSummary {
	s := store.AnalysesSummary{Repository: repository, ReadAt: at}
	for _, a := range analyses {
		dynamic := strings.HasPrefix(a.AnalysisKey, catalog.DefaultSetupKeyPrefix)
		actions := strings.Contains(a.Category, "language:actions")
		switch {
		case !dynamic && s.AdvancedKey == "":
			s.AdvancedKey, s.AdvancedAt = a.AnalysisKey, a.CreatedAt
		}
		switch {
		case actions && dynamic && s.ActionsDefaultAt == "" && !strings.HasPrefix(a.Ref, "refs/pull/"):
			s.ActionsDefaultAt, s.ActionsDefaultCommit = a.CreatedAt, a.CommitSHA
		case actions && !dynamic && s.ActionsAdvancedAt == "":
			s.ActionsAdvancedKey, s.ActionsAdvancedAt = a.AnalysisKey, a.CreatedAt
		}
	}
	return s
}

// ReadConfigurations records an account's code security configurations and where each applies.
// A refusal is recorded and reported, and leaves the last answer standing.
func (r Reader) ReadConfigurations(ctx context.Context, account string) error {
	at := r.now()
	configs, err := r.Catalog.SecurityConfigurations(ctx, account)
	if errors.Is(err, catalog.ErrNoDatabase) {
		// Only an organization offers configurations; a person's account answers 404 and has none.
		return r.Store.ReplaceSecurityConfigurations(account, at, nil, nil)
	}
	if err != nil {
		return r.unread(account, at, err)
	}
	stored := make([]store.SecurityConfiguration, 0, len(configs))
	attachments := []store.ConfigurationAttachment{}
	for _, c := range configs {
		stored = append(stored, store.SecurityConfiguration{ID: c.ID, Name: c.Name, TargetType: c.TargetType,
			Enforcement: c.Enforcement, DefaultSetup: c.DefaultSetup, AllowAdvanced: c.AllowAdvanced,
			RunnerType: c.RunnerType, RunnerLabel: c.RunnerLabel})
		repos, err := r.Catalog.ConfigurationRepositories(ctx, account, c.ID)
		if err != nil {
			return r.unread(account, at, err)
		}
		for _, repo := range repos {
			owner, _, _ := strings.Cut(repo.Repository, "/")
			if !strings.EqualFold(owner, account) {
				continue
			}
			attachments = append(attachments, store.ConfigurationAttachment{Repository: repo.Repository,
				ConfigurationID: c.ID, Status: repo.Status})
		}
	}
	return r.Store.ReplaceSecurityConfigurations(account, at, stored, attachments)
}

func (r Reader) unread(account string, at time.Time, err error) error {
	reason, _ := Refusal(err)
	if reason == "" {
		reason = err.Error()
	}
	if recordErr := r.Store.RecordConfigurationsUnread(account, at, reason); recordErr != nil {
		return recordErr
	}
	return errors.New(reason)
}

// Refusal reports whether err is GitHub answering about the repository rather than failing to
// answer, and in its own words: a 403 or 404, or an App installation the account does not have.
func Refusal(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	if errors.Is(err, ghapp.ErrNotInstalled) {
		return err.Error(), true
	}
	var status *catalog.StatusError
	if errors.As(err, &status) && (status.Code == http.StatusForbidden || status.Code == http.StatusNotFound) {
		if status.Message != "" {
			return status.Message, true
		}
		return http.StatusText(status.Code), true
	}
	return "", false
}

const actionsLanguage = "actions"

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
