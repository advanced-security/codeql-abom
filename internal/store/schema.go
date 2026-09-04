package store

// schemaDDL creates every table. Indexes are deliberately NOT created here; see indexDDL.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS repository (
  row_id                 INTEGER PRIMARY KEY,
  repository_full_name   TEXT NOT NULL UNIQUE,
  repository_url         TEXT,
  analysis_status        TEXT,
  result_count           INTEGER,
  artifact_size_in_bytes INTEGER,
  analysis_id            TEXT,
  -- Seed repositories are the ones under review. Upstream repositories are fetched only to
  -- resolve what the seeds depend on, so their own CI must not be counted as dependency
  -- pressure on the organization.
  origin                 TEXT NOT NULL DEFAULT 'seed'
);

CREATE TABLE IF NOT EXISTS rule (
  row_id            INTEGER PRIMARY KEY,
  id                TEXT NOT NULL UNIQUE,
  rule_name         TEXT NOT NULL,
  rule_description  TEXT,
  property_tags     TEXT,
  kind              TEXT,
  severity_level    TEXT,
  security_severity TEXT
);

CREATE TABLE IF NOT EXISTS alert (
  row_id              INTEGER PRIMARY KEY,
  repository_row_id   INTEGER NOT NULL REFERENCES repository(row_id),
  rule_row_id         INTEGER NOT NULL REFERENCES rule(row_id),
  file_path           TEXT NOT NULL,
  start_line          INTEGER,
  start_column        INTEGER,
  end_line            INTEGER,
  end_column          INTEGER,
  message             TEXT,
  level               TEXT,
  code_snippet        TEXT,
  code_snippet_source TEXT,
  code_snippet_sink   TEXT,
  step_count          INTEGER,
  result_fingerprint  TEXT
);

CREATE TABLE IF NOT EXISTS abom_node (
  row_id            INTEGER PRIMARY KEY,
  node_key          TEXT NOT NULL UNIQUE,
  node_kind         TEXT NOT NULL,
  owner             TEXT,
  repo              TEXT,
  path              TEXT,
  job_id            TEXT,
  step_index        INTEGER,
  display_name      TEXT,
  is_external       INTEGER NOT NULL DEFAULT 0,
  repository_row_id INTEGER REFERENCES repository(row_id),
  -- The workflow or composite action a uses: reference can name. Traversal joins on this
  -- instead of rebuilding keys from repo and path, which diverge once the action manifest
  -- filename is stripped.
  container_node_row_id INTEGER REFERENCES abom_node(row_id)
);

-- Provenance for every stored fact. ABOM holds CodeQL query results, so each row must be
-- traceable to the query and pack version that produced it: that is what makes "amend the
-- query and re-run" auditable.
CREATE TABLE IF NOT EXISTS abom_query_run (
  row_id       INTEGER PRIMARY KEY,
  run_key      TEXT NOT NULL UNIQUE,
  query_id     TEXT NOT NULL,
  query_version TEXT,
  pack_version TEXT,
  repository   TEXT NOT NULL,
  db_commit    TEXT,
  ran_at       TEXT NOT NULL
);

-- Findings from any query, upstream actions/* or our own abom/*, always carrying the rule that
-- produced them. Whether a reference is "unpinned" is answered by selecting the relevant
-- rule_id here, never by an ABOM-owned verdict column.
CREATE TABLE IF NOT EXISTS abom_finding (
  row_id            INTEGER PRIMARY KEY,
  query_run_id      INTEGER REFERENCES abom_query_run(row_id),
  rule_id           TEXT NOT NULL,
  repository_row_id INTEGER NOT NULL REFERENCES repository(row_id),
  file_path         TEXT,
  start_line        INTEGER,
  start_column      INTEGER,
  end_line          INTEGER,
  end_column        INTEGER,
  message           TEXT,
  severity          TEXT,
  security_severity REAL,
  -- Empty rather than NULL: SQLite treats NULLs as distinct in a UNIQUE constraint, so a
  -- nullable fingerprint would let identical findings insert repeatedly.
  fingerprint       TEXT NOT NULL DEFAULT '',
  UNIQUE(rule_id, repository_row_id, file_path, start_line, start_column, fingerprint)
);

CREATE TABLE IF NOT EXISTS abom_finding_node (
  finding_row_id INTEGER NOT NULL REFERENCES abom_finding(row_id),
  node_row_id    INTEGER NOT NULL REFERENCES abom_node(row_id),
  PRIMARY KEY (finding_row_id, node_row_id)
);

CREATE TABLE IF NOT EXISTS abom_edge (
  row_id              INTEGER PRIMARY KEY,
  edge_key            TEXT NOT NULL UNIQUE,
  query_run_id        INTEGER REFERENCES abom_query_run(row_id),
  src_node_row_id     INTEGER NOT NULL REFERENCES abom_node(row_id),
  dst_node_row_id     INTEGER NOT NULL REFERENCES abom_node(row_id),
  edge_kind           TEXT NOT NULL,
  ref_raw             TEXT,
  ref_value           TEXT,
  pin_comment         TEXT,
  file_path           TEXT,
  start_line          INTEGER,
  start_column        INTEGER,
  end_line            INTEGER,
  end_column          INTEGER,
  repository_row_id   INTEGER REFERENCES repository(row_id),
  provenance          TEXT
);

CREATE TABLE IF NOT EXISTS abom_surface (
  row_id                INTEGER PRIMARY KEY,
  node_row_id           INTEGER NOT NULL REFERENCES abom_node(row_id),
  permissions           TEXT,
  permissions_scope     TEXT,
  triggers              TEXT,
  runs_on               TEXT,
  uses_secrets_inherit  INTEGER NOT NULL DEFAULT 0,
  is_privileged         INTEGER NOT NULL DEFAULT 0,
  is_externally_trigger INTEGER NOT NULL DEFAULT 0,
  UNIQUE(node_row_id)
);

CREATE TABLE IF NOT EXISTS abom_alert_link (
  alert_row_id INTEGER NOT NULL REFERENCES alert(row_id),
  node_row_id  INTEGER NOT NULL REFERENCES abom_node(row_id),
  match_kind   TEXT NOT NULL,
  PRIMARY KEY (alert_row_id, node_row_id)
);

CREATE TABLE IF NOT EXISTS abom_ingest_ledger (
  unit_key          TEXT PRIMARY KEY,
  repository        TEXT NOT NULL,
  db_commit         TEXT,
  query_id          TEXT NOT NULL,
  query_version     TEXT,
  extractor_version TEXT,
  status            TEXT NOT NULL,
  rows_ingested     INTEGER NOT NULL DEFAULT 0,
  error             TEXT,
  started_at        TEXT,
  finished_at       TEXT
);

-- Discovery results. Rows with has_database = 0 are retained deliberately: they are how
-- CodeQL coverage across the organization is measured, and they stop the next discovery pass
-- from re-probing a repository that has nothing to offer.
CREATE TABLE IF NOT EXISTS abom_database_catalog (
  repository   TEXT PRIMARY KEY,
  language     TEXT NOT NULL,
  commit_oid   TEXT,
  size_bytes   INTEGER,
  created_at   TEXT,
  updated_at   TEXT,
  has_database INTEGER NOT NULL,
  checked_at   TEXT NOT NULL
);

-- Aggregate reach per referenceable node. Only counts are materialized; the full pairwise
-- dependent set is quadratic in the worst case and is computed on demand instead.
CREATE TABLE IF NOT EXISTS abom_blast_radius (
  node_row_id         INTEGER PRIMARY KEY REFERENCES abom_node(row_id),
  dependent_repos     INTEGER NOT NULL,
  dependent_workflows INTEGER NOT NULL,
  direct_dependents   INTEGER NOT NULL,
  max_depth           INTEGER NOT NULL
);

-- Accounts the operator is verified to belong to, and can therefore plausibly remediate.
--
-- Every row is evidence rather than assertion: it exists because an authenticated gh call said
-- the account in verified_as belongs to this owner, and it records when and against which host.
-- That is the same standard the rest of ABOM holds itself to, where a finding carries the rule
-- that produced it.
--
-- It is kept apart from the other two ownership notions. repository.origin records how a
-- repository entered the corpus; an account holding a seed repository is "in scope". Neither
-- means the operator can change anything: the actions account owns a seed repository and nobody
-- running this tool controls it.
CREATE TABLE IF NOT EXISTS abom_account (
  owner       TEXT PRIMARY KEY,
  kind        TEXT NOT NULL,
  role        TEXT NOT NULL,
  host        TEXT NOT NULL,
  verified_as TEXT NOT NULL,
  verified_at TEXT NOT NULL
);

-- The upstream discovery queue. Expanding a dependency reveals further dependencies, so this
-- is a persisted queue rather than a batch: consumers are also producers, and expansion may
-- span weeks across process restarts.
--
-- depth bounds how far exploration may run; it is deliberately NOT a priority signal, because
-- depth is path-dependent while commonality is a property of the dependency itself.
CREATE TABLE IF NOT EXISTS abom_frontier (
  node_key        TEXT PRIMARY KEY,
  owner           TEXT,
  repo            TEXT,
  subpath         TEXT,
  depth           INTEGER NOT NULL,
  status          TEXT NOT NULL,
  attempts        INTEGER NOT NULL DEFAULT 0,
  score           REAL NOT NULL DEFAULT 0,
  cost_estimate   REAL NOT NULL DEFAULT 1,
  reason          TEXT,
  discovered_from TEXT,
  enqueued_at     TEXT NOT NULL,
  next_attempt_at TEXT,
  completed_at    TEXT,
  -- What was analyzed, and what it pointed at when it was analyzed. Mutable refs move, so a
  -- recorded SHA is the only way to tell whether the graph still describes the current code.
  -- resolved_sha always names the commit the stored graph came FROM, never a newer one.
  analyzed_ref    TEXT,
  resolved_sha    TEXT,
  analyzed_at     TEXT,
  -- checked_at tracks the cheap ref probe, which runs far more often than a full re-analysis.
  checked_at      TEXT,
  analysis_count  INTEGER NOT NULL DEFAULT 0
);

-- Advisories are recorded as attributed external statements, keyed by GHSA id, exactly as a
-- CodeQL result is keyed by its rule id. ABOM derives no verdict of its own from them.
CREATE TABLE IF NOT EXISTS abom_advisory (
  row_id          INTEGER PRIMARY KEY,
  ghsa_id         TEXT NOT NULL UNIQUE,
  cve_id          TEXT,
  severity        TEXT,
  summary         TEXT,
  html_url        TEXT,
  cvss_v3_score   REAL,
  cvss_v3_vector  TEXT,
  cvss_v4_score   REAL,
  cvss_v4_vector  TEXT,
  -- Null EPSS means the model has no output yet, which is not the same as a score of zero.
  epss_percentage REAL,
  epss_percentile REAL,
  cwes            TEXT,
  published_at    TEXT,
  updated_at      TEXT,
  withdrawn_at    TEXT,
  fetched_at      TEXT NOT NULL
);

-- Package names are repository-scoped because that is the only granularity the advisory API
-- has; one row can therefore cover several subpath actions in the graph.
CREATE TABLE IF NOT EXISTS abom_advisory_package (
  row_id                   INTEGER PRIMARY KEY,
  advisory_row_id          INTEGER NOT NULL REFERENCES abom_advisory(row_id),
  ecosystem                TEXT,
  package_name             TEXT NOT NULL,
  -- Empty rather than null: SQLite treats nulls as distinct in a UNIQUE constraint, so a
  -- nullable column here would let duplicates through.
  vulnerable_version_range TEXT NOT NULL DEFAULT '',
  first_patched_version    TEXT,
  UNIQUE(advisory_row_id, package_name, vulnerable_version_range)
);

-- How each repository under review reaches an advisory-affected action.
--
-- The path is the point: a direct use can be bumped in the repository that has it, while a
-- transitive one has to be fixed in an intermediary that the organization may not own. Only a
-- cross-repository graph can tell those apart.
CREATE TABLE IF NOT EXISTS abom_exposure (
  row_id            INTEGER PRIMARY KEY,
  repository_row_id INTEGER NOT NULL REFERENCES repository(row_id),
  node_row_id       INTEGER NOT NULL REFERENCES abom_node(row_id),
  ghsa_id           TEXT NOT NULL,
  -- hops of 1 means the repository's own workflow uses the affected action.
  hops              INTEGER NOT NULL,
  path              TEXT NOT NULL,
  UNIQUE(repository_row_id, node_row_id, ghsa_id)
);
`

// addedColumns are applied to databases created before the column existed. The frontier is
// built to survive weeks-long expansions, so gaining a column must not require rebuilding a
// graph that took hours to produce.
var addedColumns = []struct{ table, column, decl string }{
	{"abom_frontier", "analyzed_ref", "TEXT"},
	{"abom_frontier", "resolved_sha", "TEXT"},
	{"abom_frontier", "analyzed_at", "TEXT"},
	{"abom_frontier", "checked_at", "TEXT"},
	{"abom_frontier", "analysis_count", "INTEGER NOT NULL DEFAULT 0"},
	{"repository", "origin", "TEXT NOT NULL DEFAULT 'seed'"},
}

// indexDDL is applied only after bulk loading. Maintaining these indexes during a multi-million
// row insert costs far more than building them once at the end.
const indexDDL = `
CREATE INDEX IF NOT EXISTS idx_edge_src        ON abom_edge(src_node_row_id);
CREATE INDEX IF NOT EXISTS idx_edge_dst        ON abom_edge(dst_node_row_id);
CREATE INDEX IF NOT EXISTS idx_edge_repo       ON abom_edge(repository_row_id);
CREATE INDEX IF NOT EXISTS idx_finding_rule    ON abom_finding(rule_id);
CREATE INDEX IF NOT EXISTS idx_finding_loc     ON abom_finding(repository_row_id, file_path, start_line);
CREATE INDEX IF NOT EXISTS idx_finding_node    ON abom_finding_node(node_row_id);
CREATE INDEX IF NOT EXISTS idx_frontier_claim  ON abom_frontier(status, score);
CREATE INDEX IF NOT EXISTS idx_frontier_check  ON abom_frontier(status, checked_at);
CREATE INDEX IF NOT EXISTS idx_advisory_pkg    ON abom_advisory_package(package_name);
CREATE INDEX IF NOT EXISTS idx_exposure_repo   ON abom_exposure(repository_row_id);
CREATE INDEX IF NOT EXISTS idx_exposure_ghsa   ON abom_exposure(ghsa_id, hops);
CREATE INDEX IF NOT EXISTS idx_node_owner_repo ON abom_node(owner, repo);
CREATE INDEX IF NOT EXISTS idx_node_kind       ON abom_node(node_kind);
CREATE INDEX IF NOT EXISTS idx_node_external   ON abom_node(is_external);
CREATE INDEX IF NOT EXISTS idx_node_repo       ON abom_node(repository_row_id);
CREATE INDEX IF NOT EXISTS idx_node_container  ON abom_node(container_node_row_id);
CREATE INDEX IF NOT EXISTS idx_alert_rule_repo ON alert(rule_row_id, repository_row_id);
CREATE INDEX IF NOT EXISTS idx_alert_location  ON alert(repository_row_id, file_path, start_line);
CREATE INDEX IF NOT EXISTS idx_link_node       ON abom_alert_link(node_row_id);
CREATE INDEX IF NOT EXISTS idx_ledger_status   ON abom_ingest_ledger(status);
CREATE INDEX IF NOT EXISTS idx_catalog_has_db   ON abom_database_catalog(has_database);
`
