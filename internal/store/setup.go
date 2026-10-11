package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// setupDDL holds what ABOM read about each owned repository's code scanning setup, the listing facts
// that say whether it holds Actions code, and the view that classifies the two together.
const setupDDL = `
CREATE TABLE IF NOT EXISTS abom_listed_repository (
  repository      TEXT PRIMARY KEY COLLATE NOCASE,
  listed_at       TEXT NOT NULL,
  -- NULL is a listing that did not say, never "no".
  fork            INTEGER,
  visibility      TEXT,
  has_workflows   INTEGER,
  has_actions_dir INTEGER,
  has_root_action INTEGER,
  -- When the tree lookups last answered. The REST route cannot ask them, and keeps what was seen.
  trees_read_at   TEXT
);

CREATE TABLE IF NOT EXISTS abom_setup_reading (
  repository        TEXT PRIMARY KEY COLLATE NOCASE,
  read_at           TEXT NOT NULL,
  -- Empty when GitHub answered; otherwise its refusal, and the setup columns are NULL.
  unreadable        TEXT NOT NULL DEFAULT '',
  state             TEXT,
  -- A JSON array, exactly as GitHub named the languages.
  languages         TEXT,
  query_suite       TEXT,
  threat_model      TEXT,
  runner_type       TEXT,
  runner_label      TEXT,
  schedule          TEXT,
  updated_at        TEXT,
  -- Read only before a write: 'allowed', or why Actions cannot run default setup here.
  actions_policy    TEXT,
  actions_policy_at TEXT
);

CREATE TABLE IF NOT EXISTS abom_setup_analyses (
  repository             TEXT PRIMARY KEY COLLATE NOCASE,
  read_at                TEXT NOT NULL,
  unreadable             TEXT NOT NULL DEFAULT '',
  -- The newest CodeQL analysis default setup did not make: advanced setup, whatever ran it.
  advanced_key           TEXT,
  advanced_at            TEXT,
  actions_default_at     TEXT,
  actions_default_commit TEXT,
  actions_advanced_key   TEXT,
  actions_advanced_at    TEXT
);

CREATE TABLE IF NOT EXISTS abom_security_configuration (
  account          TEXT NOT NULL COLLATE NOCASE,
  configuration_id INTEGER NOT NULL,
  name             TEXT NOT NULL,
  target_type      TEXT NOT NULL DEFAULT '',
  enforcement      TEXT NOT NULL DEFAULT '',
  default_setup    TEXT NOT NULL DEFAULT '',
  allow_advanced   INTEGER,
  runner_type      TEXT NOT NULL DEFAULT '',
  runner_label     TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (account, configuration_id)
);

-- configuration_id 0 with status 'none' is a repository that said nothing is attached. read_at is
-- set where the repository itself answered, which settles its enforcement without its account's read.
CREATE TABLE IF NOT EXISTS abom_configuration_attachment (
  repository       TEXT PRIMARY KEY COLLATE NOCASE,
  account          TEXT NOT NULL COLLATE NOCASE,
  configuration_id INTEGER NOT NULL,
  status           TEXT NOT NULL,
  read_at          TEXT
);

-- An account with no row was never asked; one whose answered_at is NULL never answered. Either
-- leaves its repositories' enforcement unknown, which holds every write there.
CREATE TABLE IF NOT EXISTS abom_configuration_read (
  account     TEXT PRIMARY KEY COLLATE NOCASE,
  read_at     TEXT NOT NULL,
  answered_at TEXT,
  unreadable  TEXT NOT NULL DEFAULT ''
);
`

// setupClassViewDDL classifies every repository under review. Created last in Migrate, after the live
// and honored-scope views it reads; a table rebuild before then would refuse a view naming them.
var setupClassViewDDL = `
DROP VIEW IF EXISTS abom_setup_class;
CREATE VIEW abom_setup_class AS
WITH facts AS (
  SELECT r.row_id AS repository_row_id, r.repository_full_name AS repository,
         ` + declaredSetupRepository + ` AS declared,
         a.missing_at,
         EXISTS (SELECT 1 FROM abom_node_reach nr
                  WHERE nr.repository_row_id = r.row_id
                    AND nr.node_row_id IN (SELECT n.row_id FROM abom_node_live n WHERE n.node_key IN
                        ('github/codeql-action/init', 'github/codeql-action/analyze'))) AS reaches_codeql,
         (SELECT COUNT(*) FROM abom_node_live n
           WHERE n.repository_row_id = r.row_id AND n.node_kind = 'workflow') AS graph_workflows,
         (SELECT fr.status FROM abom_frontier fr
           WHERE fr.node_key = r.repository_full_name AND fr.ref = '') AS crawl,
         lr.listed_at, lr.fork, lr.visibility, lr.has_workflows, lr.has_actions_dir, lr.has_root_action,
         lr.trees_read_at,
         sr.read_at AS setup_read_at, sr.unreadable AS setup_unreadable, sr.state, sr.languages,
         sr.query_suite, sr.threat_model, sr.runner_type, sr.runner_label, sr.schedule,
         sr.updated_at AS setup_updated_at, sr.actions_policy,
         EXISTS (SELECT 1 FROM json_each(sr.languages) j WHERE j.value = 'actions') AS has_actions_lang,
         EXISTS (SELECT 1 FROM json_each(sr.languages) j
                  WHERE j.value NOT IN (` + writableLanguagesSQL + `)) AS unwritable_language,
         sa.read_at AS analyses_read_at, sa.unreadable AS analyses_unreadable,
         sa.advanced_key, sa.advanced_at, sa.actions_default_at, sa.actions_default_commit,
         sa.actions_advanced_key, sa.actions_advanced_at,
         ca.configuration_id, ca.status AS attachment_status,
         sc.name AS configuration_name, sc.enforcement AS configuration_enforcement,
         sc.default_setup AS configuration_default_setup,
         COALESCE(cr.answered_at, ca.read_at) AS configurations_answered_at,
         cr.unreadable AS configurations_unreadable,
         dc.has_database, dc.commit_oid AS database_commit, dc.updated_at AS database_updated_at,
         dc.checked_at AS database_checked_at,
         (SELECT MIN(p.value) FROM abom_repository_property p
           WHERE p.repository_full_name = r.repository_full_name COLLATE NOCASE
             AND p.property_name = 'github-codeql-config-file') AS config_file
    FROM repository r
    LEFT JOIN abom_repository_absence a ON a.repository = r.repository_full_name COLLATE NOCASE
    LEFT JOIN abom_listed_repository lr ON lr.repository = r.repository_full_name COLLATE NOCASE
    LEFT JOIN abom_setup_reading sr ON sr.repository = r.repository_full_name COLLATE NOCASE
    LEFT JOIN abom_setup_analyses sa ON sa.repository = r.repository_full_name COLLATE NOCASE
    LEFT JOIN abom_configuration_attachment ca ON ca.repository = r.repository_full_name COLLATE NOCASE
    LEFT JOIN abom_security_configuration sc
           ON sc.account = ca.account AND sc.configuration_id = ca.configuration_id
    LEFT JOIN abom_configuration_read cr
           ON cr.account = SUBSTR(r.repository_full_name, 1, INSTR(r.repository_full_name, '/') - 1) COLLATE NOCASE
    LEFT JOIN abom_database_catalog dc
           ON dc.repository = r.repository_full_name AND dc.language = 'actions'
   WHERE r.origin = 'seed'
), judged AS (
  SELECT facts.*,
         (COALESCE(has_workflows, 0) = 1 OR COALESCE(has_actions_dir, 0) = 1
          OR COALESCE(has_root_action, 0) = 1 OR graph_workflows > 0
          OR (COALESCE(state, '') = 'not-configured' AND has_actions_lang)) AS actions_code,
         (trees_read_at IS NOT NULL OR COALESCE(crawl, '') = 'done') AS code_known,
         -- An analysis older than default setup's last change is history: default setup refuses
         -- advanced uploads while it is on, so one cannot still be arriving.
         (reaches_codeql OR (advanced_key IS NOT NULL
           AND (COALESCE(state, '') <> 'configured' OR advanced_at > COALESCE(setup_updated_at, '')))) AS advanced_evidence
    FROM facts
)
SELECT judged.*,
       CASE
         WHEN NOT declared THEN 'not-owned'
         WHEN missing_at IS NOT NULL THEN 'not-listed'
         WHEN advanced_evidence AND state = 'configured' THEN 'conflict'
         WHEN advanced_evidence THEN 'advanced'
         WHEN setup_unreadable <> '' THEN 'unreadable'
         WHEN setup_read_at IS NULL THEN 'not-read'
         WHEN state = 'configured' AND has_actions_lang THEN 'covered'
         WHEN NOT actions_code AND code_known THEN 'no-actions-code'
         WHEN NOT actions_code THEN 'not-read'
         WHEN COALESCE(crawl, '') <> 'done' THEN 'not-crawled'
         WHEN state = 'not-configured' AND NOT has_actions_lang THEN 'not-detected'
         WHEN configurations_answered_at IS NULL THEN 'not-read'
         WHEN attachment_status = 'enforced' AND configuration_default_setup = 'disabled'
              THEN 'blocked-by-configuration'
         WHEN actions_policy IS NOT NULL AND actions_policy <> 'allowed' THEN 'actions-not-allowed'
         WHEN state = 'configured' AND unwritable_language THEN 'language-not-writable'
         WHEN state = 'configured' THEN 'add'
         WHEN state = 'not-configured' THEN 'enable'
         ELSE 'unknown'
       END AS class,
       CASE
         WHEN NOT declared THEN 'not-owned'
         WHEN state = 'configured' AND has_actions_lang AND actions_advanced_at IS NOT NULL THEN 'both'
         WHEN state = 'configured' AND has_actions_lang THEN 'default'
         WHEN actions_advanced_at IS NOT NULL THEN 'advanced'
         WHEN NOT actions_code AND code_known THEN 'no-actions-code'
         WHEN setup_read_at IS NOT NULL AND setup_unreadable = ''
              AND analyses_read_at IS NOT NULL AND analyses_unreadable = '' THEN 'off'
         ELSE 'unknown'
       END AS actions_scanning,
       CASE
         WHEN NOT declared THEN 'not-owned'
         WHEN advanced_evidence AND state = 'configured' THEN 'conflict'
         WHEN advanced_evidence THEN 'advanced'
         WHEN setup_unreadable <> '' THEN 'unreadable'
         WHEN setup_read_at IS NULL THEN 'not-read'
         WHEN state = 'configured' THEN 'default'
         WHEN state = 'not-configured' THEN 'none'
         ELSE 'unknown'
       END AS setup_kind,
       CASE
         WHEN NOT declared THEN 'not-owned'
         WHEN has_database = 1 THEN 'present'
         WHEN has_database = 0 THEN 'absent'
         ELSE 'not-read'
       END AS actions_database
  FROM judged;
`

// declaredSetupRepository is the declaration test against the view's repository row. Absence is
// a class of its own there, so it is not folded in the way ownedFullName folds it.
var declaredSetupRepository = declaredFullName("r.repository_full_name")

// SetupWriteLanguages are the languages default setup's write accepts. A read also names
// javascript and typescript beside javascript-typescript, which a write sends as the latter; it names
// rust too, which the write refuses (422, measured), so a list holding rust cannot be sent back whole.
var SetupWriteLanguages = []string{
	"actions", "c-cpp", "csharp", "go", "java-kotlin", "javascript-typescript", "python", "ruby", "swift",
}

// writableLanguagesSQL is every name a read may return that a write can send back.
var writableLanguagesSQL = "'" + strings.Join(append(slices.Clone(SetupWriteLanguages), "javascript", "typescript"), "', '") + "'"

// The classes, first match wins. Only add and enable are ever written.
const (
	SetupNotOwned          = "not-owned"
	SetupNotListed         = "not-listed"
	SetupConflict          = "conflict"
	SetupAdvanced          = "advanced"
	SetupUnreadable        = "unreadable"
	SetupNotRead           = "not-read"
	SetupCovered           = "covered"
	SetupNoActionsCode     = "no-actions-code"
	SetupNotCrawled        = "not-crawled"
	SetupNotDetected       = "not-detected"
	SetupBlocked           = "blocked-by-configuration"
	SetupActionsNotAllowed = "actions-not-allowed"
	// SetupLanguageNotWritable analyzes a language the write refuses, so its list cannot be sent back.
	SetupLanguageNotWritable = "language-not-writable"
	SetupAdd                 = "add"
	SetupEnable              = "enable"
	SetupUnknown             = "unknown"
)

// SetupClassOrder is every class in the order the classification tries them.
var SetupClassOrder = []string{
	SetupNotOwned, SetupNotListed, SetupConflict, SetupAdvanced, SetupUnreadable, SetupNotRead,
	SetupCovered, SetupNoActionsCode, SetupNotCrawled, SetupNotDetected, SetupBlocked,
	SetupActionsNotAllowed, SetupLanguageNotWritable, SetupAdd, SetupEnable, SetupUnknown,
}

// ListedFacts is what a listing said about one repository beyond its name. A nil field is a
// listing that did not say.
type ListedFacts struct {
	Repository    string    `json:"repository"`
	Fork          *bool     `json:"fork"`
	Visibility    string    `json:"visibility,omitempty"`
	HasWorkflows  *bool     `json:"hasWorkflows"`
	HasActionsDir *bool     `json:"hasActionsDir"`
	HasRootAction *bool     `json:"hasRootAction"`
	ListedAt      time.Time `json:"listedAt"`
	TreesReadAt   time.Time `json:"treesReadAt"`
}

// RecordListedFacts keeps what each listing said, and what an earlier one saw that this one could not.
func (s *Store) RecordListedFacts(facts []ListedFacts, at time.Time) error {
	if len(facts) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("recording listing facts: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
		INSERT INTO abom_listed_repository
		       (repository, listed_at, fork, visibility, has_workflows, has_actions_dir, has_root_action, trees_read_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repository) DO UPDATE SET
		       listed_at       = excluded.listed_at,
		       fork            = COALESCE(excluded.fork, abom_listed_repository.fork),
		       visibility      = COALESCE(excluded.visibility, abom_listed_repository.visibility),
		       has_workflows   = COALESCE(excluded.has_workflows, abom_listed_repository.has_workflows),
		       has_actions_dir = COALESCE(excluded.has_actions_dir, abom_listed_repository.has_actions_dir),
		       has_root_action = COALESCE(excluded.has_root_action, abom_listed_repository.has_root_action),
		       trees_read_at   = COALESCE(excluded.trees_read_at, abom_listed_repository.trees_read_at)`)
	if err != nil {
		return fmt.Errorf("recording listing facts: %w", err)
	}
	defer stmt.Close()
	when := at.UTC().Format(time.RFC3339Nano)
	for _, f := range facts {
		if f.Repository == "" {
			continue
		}
		var trees any
		if f.HasWorkflows != nil && f.HasActionsDir != nil && f.HasRootAction != nil {
			trees = when
		}
		if _, err := stmt.Exec(f.Repository, when, nullableBool(f.Fork), nullable(strings.ToLower(f.Visibility)),
			nullableBool(f.HasWorkflows), nullableBool(f.HasActionsDir), nullableBool(f.HasRootAction), trees); err != nil {
			return fmt.Errorf("recording listing facts of %s: %w", f.Repository, err)
		}
	}
	return tx.Commit()
}

// ListedFactsOf reads back what the listings said about one repository.
func (s *Store) ListedFactsOf(repository string) (ListedFacts, bool, error) {
	f := ListedFacts{Repository: repository}
	var fork, workflows, actionsDir, rootAction sql.NullBool
	var visibility, listed, trees sql.NullString
	err := s.db.QueryRow(`SELECT fork, visibility, has_workflows, has_actions_dir, has_root_action,
	                             listed_at, trees_read_at
	                        FROM abom_listed_repository WHERE repository = ?`, repository).
		Scan(&fork, &visibility, &workflows, &actionsDir, &rootAction, &listed, &trees)
	if err == sql.ErrNoRows {
		return f, false, nil
	}
	if err != nil {
		return f, false, fmt.Errorf("reading listing facts of %s: %w", repository, err)
	}
	f.Fork, f.HasWorkflows = boolOf(fork), boolOf(workflows)
	f.HasActionsDir, f.HasRootAction = boolOf(actionsDir), boolOf(rootAction)
	f.Visibility = visibility.String
	f.ListedAt, f.TreesReadAt = parseStoredTime(listed.String), parseStoredTime(trees.String)
	return f, true, nil
}

// SetupReading is what code scanning's default setup endpoint said about one repository.
type SetupReading struct {
	Repository string
	ReadAt     time.Time
	// Unreadable is GitHub's refusal; the rest is then unknown, not empty.
	Unreadable  string
	State       string
	Languages   []string
	QuerySuite  string
	ThreatModel string
	RunnerType  string
	RunnerLabel string
	Schedule    string
	UpdatedAt   string
}

// RecordSetupReading replaces what was last read about one repository's default setup.
func (s *Store) RecordSetupReading(r SetupReading) error {
	if r.Repository == "" {
		return fmt.Errorf("store: RecordSetupReading needs a repository")
	}
	var languages any
	if r.Unreadable == "" {
		list := r.Languages
		if list == nil {
			list = []string{}
		}
		raw, err := json.Marshal(list)
		if err != nil {
			return err
		}
		languages = string(raw)
	}
	_, err := s.db.Exec(`
		INSERT INTO abom_setup_reading (repository, read_at, unreadable, state, languages, query_suite,
		       threat_model, runner_type, runner_label, schedule, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repository) DO UPDATE SET
		       read_at = excluded.read_at, unreadable = excluded.unreadable, state = excluded.state,
		       languages = excluded.languages, query_suite = excluded.query_suite,
		       threat_model = excluded.threat_model, runner_type = excluded.runner_type,
		       runner_label = excluded.runner_label, schedule = excluded.schedule,
		       updated_at = excluded.updated_at`,
		r.Repository, r.ReadAt.UTC().Format(time.RFC3339Nano), r.Unreadable, nullable(r.State), languages,
		nullable(r.QuerySuite), nullable(r.ThreatModel), nullable(r.RunnerType), nullable(r.RunnerLabel),
		nullable(r.Schedule), nullable(r.UpdatedAt))
	if err != nil {
		return fmt.Errorf("recording the default setup of %s: %w", r.Repository, err)
	}
	return nil
}

// RecordActionsPolicy records whether Actions can run default setup in a repository, as read just
// before a write: 'allowed', or the reason it cannot.
func (s *Store) RecordActionsPolicy(repository, policy string, at time.Time) error {
	res, err := s.db.Exec(`UPDATE abom_setup_reading SET actions_policy = ?, actions_policy_at = ?
	                        WHERE repository = ?`, nullable(policy), at.UTC().Format(time.RFC3339Nano), repository)
	if err != nil {
		return fmt.Errorf("recording the Actions policy of %s: %w", repository, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: %s has no default setup reading to attach an Actions policy to", repository)
	}
	return nil
}

// AnalysesSummary is what the newest page of a repository's CodeQL analyses says about who runs them.
type AnalysesSummary struct {
	Repository string
	ReadAt     time.Time
	Unreadable string
	// AdvancedKey is the newest CodeQL analysis key default setup did not make.
	AdvancedKey          string
	AdvancedAt           string
	ActionsDefaultAt     string
	ActionsDefaultCommit string
	ActionsAdvancedKey   string
	ActionsAdvancedAt    string
}

// RecordAnalysesSummary replaces what was last summarized of one repository's analyses.
func (s *Store) RecordAnalysesSummary(a AnalysesSummary) error {
	if a.Repository == "" {
		return fmt.Errorf("store: RecordAnalysesSummary needs a repository")
	}
	_, err := s.db.Exec(`
		INSERT INTO abom_setup_analyses (repository, read_at, unreadable, advanced_key, advanced_at,
		       actions_default_at, actions_default_commit, actions_advanced_key, actions_advanced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repository) DO UPDATE SET
		       read_at = excluded.read_at, unreadable = excluded.unreadable,
		       advanced_key = excluded.advanced_key, advanced_at = excluded.advanced_at,
		       actions_default_at = excluded.actions_default_at,
		       actions_default_commit = excluded.actions_default_commit,
		       actions_advanced_key = excluded.actions_advanced_key,
		       actions_advanced_at = excluded.actions_advanced_at`,
		a.Repository, a.ReadAt.UTC().Format(time.RFC3339Nano), a.Unreadable, nullable(a.AdvancedKey),
		nullable(a.AdvancedAt), nullable(a.ActionsDefaultAt), nullable(a.ActionsDefaultCommit),
		nullable(a.ActionsAdvancedKey), nullable(a.ActionsAdvancedAt))
	if err != nil {
		return fmt.Errorf("recording the analyses of %s: %w", a.Repository, err)
	}
	return nil
}

// SecurityConfiguration is one code security configuration an account offers.
type SecurityConfiguration struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	TargetType    string `json:"targetType"`
	Enforcement   string `json:"enforcement"`
	DefaultSetup  string `json:"defaultSetup"`
	AllowAdvanced *bool  `json:"allowAdvanced"`
	RunnerType    string `json:"runnerType"`
	RunnerLabel   string `json:"runnerLabel"`
}

// ConfigurationAttachment is a configuration applied to one repository, with GitHub's status for it.
type ConfigurationAttachment struct {
	Repository      string `json:"repository"`
	ConfigurationID int64  `json:"configurationId"`
	Status          string `json:"status"`
}

// ReplaceSecurityConfigurations records an account's whole answer: a repository it no longer names
// has no configuration, which only an answer about the whole account can say.
func (s *Store) ReplaceSecurityConfigurations(account string, at time.Time,
	configs []SecurityConfiguration, attachments []ConfigurationAttachment) error {
	if account == "" {
		return fmt.Errorf("store: ReplaceSecurityConfigurations needs an account")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	when := at.UTC().Format(time.RFC3339Nano)
	for _, q := range []string{
		`DELETE FROM abom_security_configuration WHERE account = ?`,
		`DELETE FROM abom_configuration_attachment WHERE account = ?`,
	} {
		if _, err := tx.Exec(q, account); err != nil {
			return fmt.Errorf("replacing the configurations of %s: %w", account, err)
		}
	}
	for _, c := range configs {
		if _, err := tx.Exec(`INSERT INTO abom_security_configuration (account, configuration_id, name,
		         target_type, enforcement, default_setup, allow_advanced, runner_type, runner_label)
		         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			account, c.ID, c.Name, c.TargetType, c.Enforcement, c.DefaultSetup, nullableBool(c.AllowAdvanced),
			c.RunnerType, c.RunnerLabel); err != nil {
			return fmt.Errorf("recording configuration %d of %s: %w", c.ID, account, err)
		}
	}
	for _, a := range attachments {
		if _, err := tx.Exec(`INSERT INTO abom_configuration_attachment (repository, account, configuration_id, status)
		         VALUES (?, ?, ?, ?)
		         ON CONFLICT(repository) DO UPDATE SET account = excluded.account,
		            configuration_id = excluded.configuration_id, status = excluded.status`,
			a.Repository, account, a.ConfigurationID, a.Status); err != nil {
			return fmt.Errorf("recording the configuration of %s: %w", a.Repository, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO abom_configuration_read (account, read_at, answered_at, unreadable)
	         VALUES (?, ?, ?, '')
	         ON CONFLICT(account) DO UPDATE SET read_at = excluded.read_at,
	            answered_at = excluded.answered_at, unreadable = ''`, account, when, when); err != nil {
		return fmt.Errorf("recording the configuration read of %s: %w", account, err)
	}
	return tx.Commit()
}

// RecordConfigurationsUnread records a failed read and leaves the last answer standing: a refusal
// is not an account whose repositories carry no configuration.
func (s *Store) RecordConfigurationsUnread(account string, at time.Time, reason string) error {
	_, err := s.db.Exec(`INSERT INTO abom_configuration_read (account, read_at, unreadable)
	         VALUES (?, ?, ?)
	         ON CONFLICT(account) DO UPDATE SET read_at = excluded.read_at, unreadable = excluded.unreadable`,
		account, at.UTC().Format(time.RFC3339Nano), reason)
	if err != nil {
		return fmt.Errorf("recording the failed configuration read of %s: %w", account, err)
	}
	return nil
}

// SetupClass is one repository's place in the Actions code scanning classification, with every fact
// that put it there.
type SetupClass struct {
	Repository      string `json:"repository"`
	Class           string `json:"class"`
	ActionsScanning string `json:"actionsScanning"`
	Setup           string `json:"setup"`
	ActionsDatabase string `json:"actionsDatabase"`
	// Evidence is the sentence saying what put the repository in its class.
	Evidence string `json:"evidence"`

	MissingAt     string   `json:"missingAt,omitempty"`
	ReachesCodeQL bool     `json:"reachesCodeQL"`
	Crawl         string   `json:"crawl,omitempty"`
	ActionsCode   bool     `json:"actionsCode"`
	CodeKnown     bool     `json:"codeKnown"`
	Listed        bool     `json:"listed"`
	Fork          *bool    `json:"fork"`
	Visibility    string   `json:"visibility,omitempty"`
	HasWorkflows  *bool    `json:"hasWorkflows"`
	HasActionsDir *bool    `json:"hasActionsDir"`
	HasRootAction *bool    `json:"hasRootAction"`
	SetupReadAt   string   `json:"setupReadAt,omitempty"`
	Unreadable    string   `json:"unreadable,omitempty"`
	State         string   `json:"state,omitempty"`
	Languages     []string `json:"languages"`
	QuerySuite    string   `json:"querySuite,omitempty"`
	ThreatModel   string   `json:"threatModel,omitempty"`
	RunnerType    string   `json:"runnerType,omitempty"`
	RunnerLabel   string   `json:"runnerLabel,omitempty"`
	Schedule      string   `json:"schedule,omitempty"`
	UpdatedAt     string   `json:"updatedAt,omitempty"`
	ActionsPolicy string   `json:"actionsPolicy,omitempty"`

	AnalysesReadAt     string `json:"analysesReadAt,omitempty"`
	AnalysesUnreadable string `json:"analysesUnreadable,omitempty"`
	AdvancedKey        string `json:"advancedKey,omitempty"`
	AdvancedAt         string `json:"advancedAt,omitempty"`
	ActionsDefaultAt   string `json:"actionsDefaultAt,omitempty"`
	ActionsAdvancedKey string `json:"actionsAdvancedKey,omitempty"`
	ActionsAdvancedAt  string `json:"actionsAdvancedAt,omitempty"`

	ConfigurationName        string `json:"configurationName,omitempty"`
	ConfigurationEnforcement string `json:"configurationEnforcement,omitempty"`
	ConfigurationDefaultSet  string `json:"configurationDefaultSetup,omitempty"`
	AttachmentStatus         string `json:"attachmentStatus,omitempty"`
	ConfigurationsAnswered   string `json:"configurationsAnsweredAt,omitempty"`
	ConfigurationsUnread     string `json:"configurationsUnread,omitempty"`

	DatabaseCommit    string `json:"databaseCommit,omitempty"`
	DatabaseUpdatedAt string `json:"databaseUpdatedAt,omitempty"`
	DatabaseCheckedAt string `json:"databaseCheckedAt,omitempty"`
	ConfigFile        string `json:"configFile,omitempty"`
}

// SetupClasses classifies the named repositories, or every repository under review when none are named.
func (s *Store) SetupClasses(names ...string) ([]SetupClass, error) {
	where, args := "", []any{}
	if len(names) > 0 {
		list, bound := inList(names)
		where, args = ` WHERE LOWER(repository) IN `+list, bound
	}
	return queryAll(s, "classifying code scanning setup", scanSetupClass, `
		SELECT repository, class, actions_scanning, setup_kind, actions_database,
		       COALESCE(missing_at, ''), reaches_codeql, COALESCE(crawl, ''), actions_code, code_known,
		       listed_at IS NOT NULL, fork, COALESCE(visibility, ''), has_workflows, has_actions_dir,
		       has_root_action, COALESCE(setup_read_at, ''), COALESCE(setup_unreadable, ''),
		       COALESCE(state, ''), COALESCE(languages, '[]'), COALESCE(query_suite, ''),
		       COALESCE(threat_model, ''), COALESCE(runner_type, ''), COALESCE(runner_label, ''),
		       COALESCE(schedule, ''), COALESCE(setup_updated_at, ''), COALESCE(actions_policy, ''),
		       COALESCE(analyses_read_at, ''), COALESCE(analyses_unreadable, ''),
		       COALESCE(advanced_key, ''), COALESCE(advanced_at, ''), COALESCE(actions_default_at, ''),
		       COALESCE(actions_advanced_key, ''), COALESCE(actions_advanced_at, ''),
		       COALESCE(configuration_name, ''), COALESCE(configuration_enforcement, ''),
		       COALESCE(configuration_default_setup, ''), COALESCE(attachment_status, ''),
		       COALESCE(configurations_answered_at, ''), COALESCE(configurations_unreadable, ''),
		       COALESCE(database_commit, ''), COALESCE(database_updated_at, ''),
		       COALESCE(database_checked_at, ''), COALESCE(config_file, '')
		  FROM abom_setup_class`+where+` ORDER BY repository`, args...)
}

func scanSetupClass(row *sql.Rows) (SetupClass, error) {
	var c SetupClass
	var fork, workflows, actionsDir, rootAction sql.NullBool
	var languages string
	err := row.Scan(&c.Repository, &c.Class, &c.ActionsScanning, &c.Setup, &c.ActionsDatabase,
		&c.MissingAt, &c.ReachesCodeQL, &c.Crawl, &c.ActionsCode, &c.CodeKnown,
		&c.Listed, &fork, &c.Visibility, &workflows, &actionsDir,
		&rootAction, &c.SetupReadAt, &c.Unreadable,
		&c.State, &languages, &c.QuerySuite,
		&c.ThreatModel, &c.RunnerType, &c.RunnerLabel,
		&c.Schedule, &c.UpdatedAt, &c.ActionsPolicy,
		&c.AnalysesReadAt, &c.AnalysesUnreadable,
		&c.AdvancedKey, &c.AdvancedAt, &c.ActionsDefaultAt,
		&c.ActionsAdvancedKey, &c.ActionsAdvancedAt,
		&c.ConfigurationName, &c.ConfigurationEnforcement,
		&c.ConfigurationDefaultSet, &c.AttachmentStatus,
		&c.ConfigurationsAnswered, &c.ConfigurationsUnread,
		&c.DatabaseCommit, &c.DatabaseUpdatedAt,
		&c.DatabaseCheckedAt, &c.ConfigFile)
	if err != nil {
		return c, err
	}
	c.Fork, c.HasWorkflows = boolOf(fork), boolOf(workflows)
	c.HasActionsDir, c.HasRootAction = boolOf(actionsDir), boolOf(rootAction)
	c.Languages = []string{}
	if err := json.Unmarshal([]byte(languages), &c.Languages); err != nil {
		return c, fmt.Errorf("the languages of %s are not a list: %w", c.Repository, err)
	}
	c.Evidence = setupEvidence(c)
	return c, nil
}

// RepositoryCodeScanning is the repositories unit's three Actions code scanning columns, the class
// they add up to and the sentence saying why. Known is false where no classification answered.
type RepositoryCodeScanning struct {
	Known             bool     `json:"known"`
	Class             string   `json:"class"`
	ActionsScanning   string   `json:"actionsScanning"`
	Setup             string   `json:"setup"`
	ActionsDatabase   string   `json:"actionsDatabase"`
	Evidence          string   `json:"evidence"`
	ReadAt            string   `json:"readAt,omitempty"`
	Languages         []string `json:"languages"`
	DatabaseCommit    string   `json:"databaseCommit,omitempty"`
	DatabaseUpdatedAt string   `json:"databaseUpdatedAt,omitempty"`
	ConfigurationName string   `json:"configurationName,omitempty"`
	AttachmentStatus  string   `json:"attachmentStatus,omitempty"`
	ConfigFile        string   `json:"configFile,omitempty"`
}

// codeScanningByRepository reads the columns for one page of repositories, keyed lowercase.
func (s *Store) codeScanningByRepository(names []string) (map[string]RepositoryCodeScanning, error) {
	out := map[string]RepositoryCodeScanning{}
	if len(names) == 0 {
		return out, nil
	}
	classes, err := s.SetupClasses(names...)
	if err != nil {
		return nil, err
	}
	for _, c := range classes {
		out[strings.ToLower(c.Repository)] = RepositoryCodeScanning{Known: true, Class: c.Class,
			ActionsScanning: c.ActionsScanning, Setup: c.Setup, ActionsDatabase: c.ActionsDatabase,
			Evidence: c.Evidence, ReadAt: c.SetupReadAt, Languages: c.Languages,
			DatabaseCommit: c.DatabaseCommit, DatabaseUpdatedAt: c.DatabaseUpdatedAt,
			ConfigurationName: c.ConfigurationName, AttachmentStatus: c.AttachmentStatus,
			ConfigFile: c.ConfigFile}
	}
	return out, nil
}

// setupSortSQL orders a column along its scale, gaps first: what most needs somebody's attention
// leads. A value off the scale is NULL, which the ordering keeps after every value on it.
func setupSortSQL(column string, scale []string, outside ...string) string {
	expr := `CASE (SELECT sc.` + column + ` FROM abom_setup_class sc WHERE sc.repository_row_id = r.row_id)`
	for i, value := range scale {
		expr += fmt.Sprintf(" WHEN '%s' THEN %d", value, i)
	}
	for _, value := range outside {
		expr += fmt.Sprintf(" WHEN '%s' THEN NULL", value)
	}
	return expr + fmt.Sprintf(" ELSE %d END", len(scale))
}

func nullableBool(v *bool) any {
	if v == nil {
		return nil
	}
	if *v {
		return 1
	}
	return 0
}

func boolOf(v sql.NullBool) *bool {
	if !v.Valid {
		return nil
	}
	b := v.Bool
	return &b
}

func parseStoredTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}
	}
	return t
}
