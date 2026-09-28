package store

// schemaDDL creates every table. Indexes are deliberately NOT created here; see indexDDL.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS repository (
  row_id                 INTEGER PRIMARY KEY,
  repository_full_name   TEXT NOT NULL UNIQUE,
  repository_url         TEXT,
  analysis_status        TEXT,
  -- Seed repositories are the ones under review. Upstream repositories are fetched only to
  -- resolve what the seeds depend on, so their own CI must not be counted as dependency
  -- pressure on the organization.
  origin                 TEXT NOT NULL DEFAULT 'seed'
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
  pack         TEXT,
  pack_version TEXT,
  -- Content hash of the pack that supplied the query. The version cannot serve as identity:
  -- ABOM's own packs track the CodeQL CLI and do not move when a query is edited.
  pack_digest  TEXT,
  -- The CodeQL that ran, not the one pinned in .codeql-version.
  cli_version  TEXT,
  repository   TEXT NOT NULL,
  db_commit    TEXT,
  -- The checkout filter the database was built from. A commit alone does not name a tree: only
  -- the paths that were fetched decide what the extractor saw, and content equivalence joins
  -- runs to commits, so without this a run under a narrow filter answers for a wider one.
  source_filter TEXT,
  -- How the database was built: 'regular' is one repository's own checkout, 'extended' is an
  -- owned repository plus its staged closure. The same rule id means opposite things in the two,
  -- so every aggregate over edges or findings names the mode it is reading.
  analysis_mode TEXT NOT NULL DEFAULT 'regular',
  -- What produced the evaluation: 'crawl', an on-demand 'run', or the 'corpus' replay. NULL is
  -- not recorded, which every row written before the column existed is.
  recorded_by  TEXT,
  -- 1 for an evaluation whose results a run held rather than stored. It answers no skip, and only
  -- what somebody converted to findings is sighted under it.
  held         INTEGER NOT NULL DEFAULT 0,
  ran_at       TEXT NOT NULL
);

-- Which references an evaluation was reached through. A relation rather than a column on the run,
-- because the run key hashes the commit: two analyses of one repository at one commit are one
-- ledger row by design, and on the seeded corpus 392 repository/commit pairs are reached by more
-- than one ref. A column would name one of them and drop the rest.
--
-- A missing row means nobody recorded a reference, which is not the same as the default branch --
-- the frontier already spells that as the empty ref.
CREATE TABLE IF NOT EXISTS abom_query_run_ref (
  query_run_id INTEGER NOT NULL REFERENCES abom_query_run(row_id),
  -- The reference as asked for. Empty means the default branch, matching the frontier.
  ref          TEXT NOT NULL,
  -- The reference as checked out, which is 'HEAD' when nothing was asked for. Kept apart from
  -- ref for the reason checkout.go states: a row for v4 that reported HEAD could never be told
  -- apart from one analyzed at the default branch.
  analyzed_ref TEXT NOT NULL,
  PRIMARY KEY (query_run_id, ref, analyzed_ref)
);

-- The rules one evaluation ran, and the query behind each. A lane runs a whole suite in one
-- invocation, so the evaluation's own version and packs cannot say which ones a finding's rule was.
-- NULLs are not recorded: a rule two packs supplied at once has no single version, and a pack
-- ABOM could not name has no source.
CREATE TABLE IF NOT EXISTS abom_query_run_rule (
  query_run_id  INTEGER NOT NULL REFERENCES abom_query_run(row_id),
  rule_id       TEXT NOT NULL,
  query_version TEXT,
  pack          TEXT,
  pack_version  TEXT,
  pack_digest   TEXT,
  source        TEXT,
  PRIMARY KEY (query_run_id, rule_id)
);

-- Which branches a repository protects. A relation because a repository has many, and whether a
-- branch is protected is what decides if a pull request check can block a merge or only comment.
--
-- Emptiness here is only meaningful next to repository.branches_checked_at: no rows and no check
-- is "nobody asked", no rows and a check is "this repository protects nothing".
CREATE TABLE IF NOT EXISTS abom_protected_branch (
  repository_row_id INTEGER NOT NULL REFERENCES repository(row_id),
  branch_name       TEXT NOT NULL,
  PRIMARY KEY (repository_row_id, branch_name)
);

-- Findings from any query, upstream actions/* or our own abom/*, always carrying the rule that
-- produced them. Whether a reference is "unpinned" is answered by selecting the relevant
-- rule_id here, never by an ABOM-owned verdict column.
CREATE TABLE IF NOT EXISTS abom_finding (
  row_id            INTEGER PRIMARY KEY,
  -- Which producer made the claim, as the SARIF tool driver name spells it. NULL is unknown, never
  -- a stand-in for CodeQL: a finding written before this column existed was never attributed, and
  -- naming a producer on its behalf would fabricate the provenance the column is for.
  provider          TEXT,
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
  -- Content identity of the finding, excluding the repository so that one defect reached through
  -- many repositories is a group-by rather than a traversal.
  result_hash       TEXT,
  UNIQUE(rule_id, repository_row_id, file_path, start_line, start_column, fingerprint)
);

CREATE TABLE IF NOT EXISTS abom_finding_node (
  finding_row_id INTEGER NOT NULL REFERENCES abom_finding(row_id),
  node_row_id    INTEGER NOT NULL REFERENCES abom_node(row_id),
  PRIMARY KEY (finding_row_id, node_row_id)
);

-- Which run reported which finding. A finding row is unique on a key that spans commits, so it
-- represents something seen at possibly many references while naming exactly one run: "is this
-- still reported" is a missing relation, not a missing WHERE clause. Priced against the corpus,
-- the rule that consulted the single run condemned 2,420 findings other references still report.
CREATE TABLE IF NOT EXISTS abom_finding_sighting (
  finding_row_id INTEGER NOT NULL REFERENCES abom_finding(row_id),
  query_run_id   INTEGER NOT NULL REFERENCES abom_query_run(row_id),
  PRIMARY KEY (finding_row_id, query_run_id)
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
  provenance          TEXT,
  -- Which boundary the reference crosses: same repository, same account, or another account.
  -- NULL while nothing could name both ends, which is not the same as crossing an account.
  boundary            TEXT,
  -- Facts about the relationship itself, filled by DeriveEdgeFacts from rows already stored.
  -- NULL throughout means nobody has computed it yet, never a measured absence.
  ref_style           TEXT,
  advisory_route      INTEGER,
  finding_route       INTEGER,
  -- Fewest hops from a repository the operator declared. One means that repository declares the
  -- reference itself, which is the meaning exposure gives hops.
  owned_depth         INTEGER
);

CREATE TABLE IF NOT EXISTS abom_edge_sighting (
  edge_row_id  INTEGER NOT NULL REFERENCES abom_edge(row_id) ON DELETE CASCADE,
  query_run_id INTEGER NOT NULL REFERENCES abom_query_run(row_id),
  PRIMARY KEY (edge_row_id, query_run_id)
);

-- What holds what, which the canvas draws and nothing declares.
--
-- A table of its own rather than rows in abom_edge: an edge's identity is the source location
-- that declares it and containment has none, and abom_edge is joined by reach, exposure, blast
-- radius, the bill of materials, pinning and pruning — every one of which counts dependencies,
-- none of which may move because a drawing aid became storable.
--
-- Keys rather than node row ids. Containment reaches things abom_node does not hold: an account
-- holds a repository the operator declared and nothing has analyzed, and a dependency split per
-- version is a node the projection makes. A key is what both of those have.
CREATE TABLE IF NOT EXISTS abom_containment (
  row_id    INTEGER PRIMARY KEY,
  owner_key TEXT NOT NULL,
  held_key  TEXT NOT NULL,
  edge_kind TEXT NOT NULL,
  UNIQUE(owner_key, held_key, edge_kind)
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

-- The CodeQL databases ABOM built and kept, so a query can be run across a corpus that already
-- exists rather than rebuilding one repository at a time.
--
-- This is not abom_database_catalog and may not be merged with it. That table answers whether
-- somebody enabled code scanning on a repository -- a fact about how it is configured, held for
-- repositories ABOM has never built anything for. This one answers what is on this disk.
--
-- Identity is the repository, the commit, the checkout filter and the analysis mode: the same
-- tuple abom_query_run hashes into its run key, because a second definition of "which database"
-- is how two engines start disagreeing about what has already been analyzed. The filter is part
-- of it for the reason the checkout cache keys on it too -- a commit alone does not name a tree,
-- and a database built from narrower paths cannot answer a wider question.
--
-- The row indexes a directory rather than being one. The disk half promotes a finished build
-- atomically and this records the result, so an interrupted run leaves an unreferenced directory
-- to reclaim rather than a row pointing at nothing.
CREATE TABLE IF NOT EXISTS abom_local_database (
  row_id        INTEGER PRIMARY KEY,
  repository    TEXT NOT NULL,
  db_commit     TEXT NOT NULL DEFAULT '',
  source_filter TEXT NOT NULL DEFAULT '',
  analysis_mode TEXT NOT NULL DEFAULT 'regular',
  path          TEXT NOT NULL,
  -- Always measured before the row is written, so zero means an empty database rather than one
  -- nobody sized. Eviction has to be able to trust it.
  bytes         INTEGER NOT NULL,
  cli_version   TEXT,
  built_at      TEXT NOT NULL,
  last_used_at  TEXT NOT NULL,
  -- What eviction actually orders by. last_used_at is for a reader: RFC3339 is second-resolution,
  -- so two databases used in one second tie, and the nanosecond form trims trailing zeros, which
  -- makes its string order differ from its time order. A counter has neither problem.
  used_seq      INTEGER NOT NULL,
  UNIQUE (repository, db_commit, source_filter, analysis_mode)
);

-- Aggregate reach per referenceable node.
CREATE TABLE IF NOT EXISTS abom_blast_radius (
  node_row_id         INTEGER PRIMARY KEY REFERENCES abom_node(row_id),
  dependent_repos     INTEGER NOT NULL,
  dependent_workflows INTEGER NOT NULL,
  direct_dependents   INTEGER NOT NULL,
  max_depth           INTEGER NOT NULL
);

-- Which repositories reach each node, behind the count above.
--
-- A count cannot be filtered: bounding an inventory to a repository grouping means recounting
-- reach within that grouping, and dependent_repos alone offers nothing to recount from. Both are
-- written from the same walk, so the count is always this table's cardinality.
--
-- Only seed repositories appear, for the same reason they are the only ones counted: an upstream
-- repository is not one the operator can act on. Growth is bounded by referenceable nodes times
-- seed repositories, which is quadratic in the worst case; measured on a crawled organization of
-- 25 repositories it is 155 rows against 448 counts, because most nodes are reached by few.
CREATE TABLE IF NOT EXISTS abom_node_reach (
  node_row_id       INTEGER NOT NULL REFERENCES abom_node(row_id),
  repository_row_id INTEGER NOT NULL REFERENCES repository(row_id),
  -- Fewest hops from the repository to the node. One means the repository names the dependency
  -- itself; more means it inherits it, and the two are different remediation work.
  hops              INTEGER NOT NULL,
  PRIMARY KEY (node_row_id, repository_row_id)
) WITHOUT ROWID;

-- The filtering direction: the nodes a named set of repositories reaches.
CREATE INDEX IF NOT EXISTS idx_node_reach_repository
  ON abom_node_reach(repository_row_id);

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
-- depth bounds how far exploration may run, and since 2026-09-07 it is also the outer key of the
-- claim order. It was deliberately not a priority signal before, on the grounds that depth is
-- path-dependent while commonality is a property of the dependency itself. That reasoning ranked
-- individual items well and gave the crawl no objective: ordering by commonality first meant it
-- could work depth 3 while depth 1 was still incomplete, and "complete at depth 1" is the only
-- claim of the two that the queue can prove. Commonality still decides within a depth.
` + frontierDDL + `

-- Anything a query says that is neither a dependency edge nor a security finding.
--
-- The set of queries ABOM runs is open-ended by design: the inspection engine is reprogrammable,
-- so a query written next year must be able to contribute facts without a schema change and
-- without a line of Go. What it may never do is contribute an *unattributed* fact, which is why
-- query_run_id is NOT NULL — the same rule that makes a finding name its rule id.
--
-- Subjects are named by key rather than row id: a query emits the node key it saw, and the
-- observation must survive being recorded before the subject it names.
` + observationDDL + `

-- Advisories are recorded as attributed external statements, keyed by GHSA id, exactly as a-- CodeQL result is keyed by its rule id. ABOM derives no verdict of its own from them.
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

-- A run somebody asked for: an explicit list of queries against the databases a scope resolved to.
--
-- Apart from abom_query_run, which is the ledger of what has been evaluated. This is the request
-- and its progress, and it outlives the evaluation: a member that failed is a fact about this run,
-- not about the query.
CREATE TABLE IF NOT EXISTS abom_analysis_run (
  row_id        INTEGER PRIMARY KEY,
  -- The predicate the database set was resolved from, verbatim. What a run was asked for and what
  -- it resolved to are different facts, and keeping only the second leaves a run unable to say
  -- which question it was answering.
  predicate     TEXT NOT NULL DEFAULT '',
  analysis_mode TEXT NOT NULL DEFAULT 'regular',
  requested_by  TEXT NOT NULL,
  state         TEXT NOT NULL,
  created_at    TEXT NOT NULL,
  finished_at   TEXT,
  -- The CodeQL that carried the run out, set when it starts: the same queries over the same scope
  -- can answer differently under the next release. NULL until then.
  cli_version   TEXT,
  -- Why the run could not start the last time it was asked to, in the executor's words. NULL for
  -- a run that started, or that nobody has asked to start.
  refusal       TEXT,
  -- 1 for a run with findings off: its results are held in abom_held_result until somebody
  -- converts them, because a query still being refined should not write findings everything reads.
  held          INTEGER NOT NULL DEFAULT 0,
  -- When the start under way, or the last one, began. A run started again keeps each member's
  -- state until it is reached, so this is what tells a member still to do from one this start
  -- finished. NULL for a run never started, or started before starts were stamped.
  started_at    TEXT,
  -- The named scope the run was chosen by, beside the predicate it resolved to, which is what
  -- the run covers: keeping another scope under the name later changes neither. NULL for a run
  -- chosen any other way.
  scope_name    TEXT
);

-- The explicit queries a run names. A list rather than a suite: the point of the run is to be more
-- specific than a suite can be, and a suite resolved at request time would record what it happened
-- to hold then rather than what was chosen.
CREATE TABLE IF NOT EXISTS abom_analysis_query (
  run_id        INTEGER NOT NULL REFERENCES abom_analysis_run(row_id),
  query_id      TEXT NOT NULL,
  query_version TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (run_id, query_id, query_version)
);

-- One row per repository the scope named, whatever became of it.
--
-- Every member is kept, including the ones nothing can analyze: a run holding only what it could
-- reach means something narrower than it was asked for, and the narrowing is invisible precisely
-- where a reader most needs telling. A repository with no database is a member whose status says
-- so, never an absent row.
CREATE TABLE IF NOT EXISTS abom_analysis_member (
  run_id        INTEGER NOT NULL REFERENCES abom_analysis_run(row_id),
  repository    TEXT NOT NULL,
  db_commit     TEXT NOT NULL DEFAULT '',
  source_filter TEXT NOT NULL DEFAULT '',
  analysis_mode TEXT NOT NULL DEFAULT 'regular',
  status        TEXT NOT NULL,
  -- What went wrong, or why nothing could run. A member that is only "failed" says a repository
  -- produced nothing and nothing about whether that is worth acting on.
  detail        TEXT NOT NULL DEFAULT '',
  -- Where the database was when the run started, so a member can be re-run without resolving the
  -- scope again. Empty for a member nothing can analyze.
  database_path TEXT NOT NULL DEFAULT '',
  -- Where this member's raw results were written. Kept rather than parsed and thrown away: the
  -- evidence a tuning decision rests on is the output itself, and re-running to see it again
  -- costs what the run cost.
  result_path   TEXT NOT NULL DEFAULT '',
  -- How many findings this member reported. NULL where nothing has counted it: a member that
  -- found nothing and one that never ran both show no findings, and only the first is a zero.
  result_count  INTEGER,
  -- The ledger row this member's results were stored under, which is how its findings are found
  -- again. NULL where nothing was stored, so a member nothing ran against lists no findings
  -- without reading as one that ran and found none.
  query_run_id  INTEGER REFERENCES abom_query_run(row_id),
  started_at    TEXT,
  finished_at   TEXT,
  PRIMARY KEY (run_id, repository, db_commit, source_filter, analysis_mode)
);

-- What each query found in one member. A row only for a member that was counted, so a query with
-- no row there was never asked rather than asked and silent.
CREATE TABLE IF NOT EXISTS abom_analysis_result (
  run_id        INTEGER NOT NULL REFERENCES abom_analysis_run(row_id),
  repository    TEXT NOT NULL,
  db_commit     TEXT NOT NULL DEFAULT '',
  source_filter TEXT NOT NULL DEFAULT '',
  analysis_mode TEXT NOT NULL DEFAULT 'regular',
  query_id      TEXT NOT NULL,
  result_count  INTEGER NOT NULL,
  PRIMARY KEY (run_id, repository, db_commit, source_filter, analysis_mode, query_id)
);

-- What a held run's queries reported in one member, one row per result, carrying every column a
-- finding does so converting one writes the finding adopting the query would have. Apart from
-- abom_finding on purpose: merged, every count on every surface would include results nobody
-- chose to look for. Replaced whole when the member is analyzed again.
CREATE TABLE IF NOT EXISTS abom_held_result (
  row_id        INTEGER PRIMARY KEY,
  run_id        INTEGER NOT NULL REFERENCES abom_analysis_run(row_id),
  repository    TEXT NOT NULL,
  db_commit     TEXT NOT NULL DEFAULT '',
  source_filter TEXT NOT NULL DEFAULT '',
  analysis_mode TEXT NOT NULL DEFAULT 'regular',
  rule_id       TEXT NOT NULL,
  file_path     TEXT NOT NULL DEFAULT '',
  start_line    INTEGER NOT NULL DEFAULT 0,
  start_column  INTEGER NOT NULL DEFAULT 0,
  end_line      INTEGER NOT NULL DEFAULT 0,
  end_column    INTEGER NOT NULL DEFAULT 0,
  message       TEXT NOT NULL DEFAULT '',
  severity      TEXT NOT NULL DEFAULT '',
  fingerprint   TEXT NOT NULL DEFAULT '',
  result_hash   TEXT NOT NULL DEFAULT '',
  provider      TEXT NOT NULL DEFAULT '',
  -- NULL where the rule declares no score, as on abom_finding.
  security_severity REAL
);

-- Who made a finding of what a held run kept, and when. Keyed by the finding rather than by the
-- held row, which analyzing the member again replaces: what was adopted is the finding.
CREATE TABLE IF NOT EXISTS abom_held_conversion (
  run_id         INTEGER NOT NULL REFERENCES abom_analysis_run(row_id),
  finding_row_id INTEGER NOT NULL,
  converted_by   TEXT NOT NULL,
  converted_at   TEXT NOT NULL,
  PRIMARY KEY (run_id, finding_row_id)
);

-- Which lifecycle stage somebody put a query version in.
--
-- Only the act is stored. Everything else the catalog reports -- which versions exist, how many
-- runs and repositories each has behind it -- is derived from abom_query_run, because the ledger
-- already records what ran with what identity and a second table maintained beside it would be a
-- second writer of one fact.
--
-- advanced_by and advanced_at are not decoration. ABOM computes readiness and a person advances
-- the stage, so a row that could not say who moved it would be indistinguishable from a stage the
-- tool awarded itself. A version nobody has staged has no row, which is deliberately different
-- from one staged at dev.
CREATE TABLE IF NOT EXISTS abom_query_stage (
  query_id      TEXT NOT NULL,
  -- Empty for runs recorded before query versions were written. In a primary key, so it cannot
  -- be NULL: NULL never equals NULL and the upsert would never converge.
  query_version TEXT NOT NULL DEFAULT '',
  stage         TEXT NOT NULL,
  advanced_by   TEXT NOT NULL,
  advanced_at   TEXT NOT NULL,
  PRIMARY KEY (query_id, query_version)
);

-- What each dependency tier runs, as configuration rather than a command-line flag.
--
-- A tier binds to a *set* of suites, so no combined per-tier suite artifact ever has to be
-- generated. The companion table records that a tier was configured at all, which is what makes
-- "analyze nothing in this tier" expressible: without it, an empty binding is indistinguishable
-- from one nobody has set, and the defaults would be seeded back over a deliberate choice.
CREATE TABLE IF NOT EXISTS abom_tier_suite (
  tier  TEXT NOT NULL,
  suite TEXT NOT NULL,
  PRIMARY KEY (tier, suite)
);

CREATE TABLE IF NOT EXISTS abom_tier_configured (
  tier TEXT PRIMARY KEY
);

-- What each suite last resolved to, and which pack supplied each query.
--
-- A suite is a program over packs — locate, filter, compose — so its contents are only knowable
-- by asking the CLI to resolve it. Resolving costs one process per query, which is why this
-- records what the crawl already resolved rather than being recomputed on a read: the interface
-- polls, and every codeql invocation shares one compilation cache with the crawl.
--
-- The pack is the level that carries a version, since a suite has none of its own. A suite
-- nobody has resolved has no rows here, which is deliberately different from resolving to none.
CREATE TABLE IF NOT EXISTS abom_suite_query (
  suite        TEXT NOT NULL,
  pack         TEXT NOT NULL,
  pack_version TEXT,
  pack_digest  TEXT,
  query_id     TEXT NOT NULL,
  -- Content hash of the .ql and the libraries it imports. Finer than the pack digest beside it:
  -- that moves when any file in the pack moves, and this moves when this query's meaning does.
  query_version TEXT NOT NULL DEFAULT '',
  kind         TEXT,
  resolved_at  TEXT NOT NULL,
  PRIMARY KEY (suite, query_id)
);

-- Every metadata pair a query version declares, as codeql resolve metadata read it: severity,
-- precision, tags, and whatever else the query says about itself. Keyed by version, because a new
-- version may declare a different score. A version nobody resolved has no rows, which is not one
-- that declares nothing. Replaced whole when the version is resolved again.
CREATE TABLE IF NOT EXISTS abom_query_metadata (
  query_id      TEXT NOT NULL,
  query_version TEXT NOT NULL DEFAULT '',
  name          TEXT NOT NULL,
  value         TEXT NOT NULL DEFAULT '',
  resolved_at   TEXT NOT NULL,
  PRIMARY KEY (query_id, query_version, name)
);

-- A marker the interface polls to learn the graph changed, carrying the row ids the change is
-- measured from. Sound only because abom_node and abom_edge are never deleted from, which
-- TestGraphTablesAreAppendOnly pins.
--
-- rebuild_ms and the counts beside it record what recomputing reach actually cost on this
-- corpus, so the schedule deciding how often to pay it can be tuned against measurement.
CREATE TABLE IF NOT EXISTS abom_graph_generation (
  generation     INTEGER PRIMARY KEY AUTOINCREMENT,
  node_watermark INTEGER NOT NULL,
  edge_watermark INTEGER NOT NULL,
  -- Containment keeps a cursor of its own. It was once cut by the edge watermark against a node
  -- row id, which are independent sequences, so a delta carried an arbitrary slice of the tree.
  containment_watermark INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL,
  reach_rebuilt  INTEGER NOT NULL DEFAULT 0,
  rebuild_ms     INTEGER,
  rebuilt_nodes  INTEGER,
  rebuilt_edges  INTEGER
);

-- Every snapshot ABOM has written to GitHub. Append-only, because superseding a submission is a
-- second write rather than an edit, and the earlier row is the only record of what was on the
-- repository before it.
--
-- This exists because a submission cannot be withdrawn: there is no delete endpoint, and the only
-- repair is another write. An artifact nobody wrote down is an artifact nobody can find.
-- released marks a submission of nothing, which hands the path back to GitHub's static analysis
-- and is the one mechanism that does. It must be distinguishable from never having submitted.
CREATE TABLE IF NOT EXISTS abom_submission (
  row_id       INTEGER PRIMARY KEY,
  repository   TEXT NOT NULL,
  sha          TEXT,
  correlator   TEXT NOT NULL,
  detector     TEXT NOT NULL,
  manifest     TEXT,
  package_urls INTEGER NOT NULL DEFAULT 0,
  -- Content identity of what was sent. The count above cannot answer "has this changed": a
  -- version bump keeps it identical while changing what every consumer is told.
  package_digest TEXT,
  released     INTEGER NOT NULL DEFAULT 0,
  result       TEXT,
  message      TEXT,
  submitted_at TEXT NOT NULL
);
` + refResolutionDDL + `
`

// addedColumns are applied to databases created before the column existed. The frontier is
// built to survive weeks-long expansions, so gaining a column must not require rebuilding a
// graph that took hours to produce.
// droppedColumns are removed from a database that still has them.
//
// The only non-additive migration here. These three are a `gh mrva` era's leavings: nothing in
// this repository reads or writes them, and they describe an analysis model ABOM does not use.
// Dropping is safe for the reason additive migrations are not usually needed to prove — SQLite
// rewrites the table, so the guard is `TestDroppingVestigialColumnsKeepsEveryRepositoryRow`
// rather than the absence of risk.
var droppedColumns = []struct{ table, column string }{
	{"repository", "result_count"},
	{"repository", "artifact_size_in_bytes"},
	{"repository", "analysis_id"},
}

var addedColumns = []struct{ table, column, decl string }{
	{"abom_saved_view", "presentation", "TEXT"},
	// A run recorded before results were retained keeps an empty path, which reads as "nothing was
	// written" rather than as a file nobody can find.
	{"abom_analysis_member", "result_path", "TEXT NOT NULL DEFAULT ''"},
	{"abom_analysis_member", "result_count", "INTEGER"},
	// A suite resolved before query versions were computed keeps an empty one, which reads as
	// "nobody hashed this" rather than as a version somebody chose.
	{"abom_suite_query", "query_version", "TEXT NOT NULL DEFAULT ''"},
	{"abom_frontier", "analyzed_ref", "TEXT"},
	{"abom_frontier", "resolved_sha", "TEXT"},
	{"abom_frontier", "analyzed_at", "TEXT"},
	{"abom_frontier", "checked_at", "TEXT"},
	{"abom_frontier", "analysis_count", "INTEGER NOT NULL DEFAULT 0"},
	{"abom_frontier", "claimed_at", "TEXT"},
	// Existing rows predate the distinction and were all enqueued under a rule that required an
	// external reference, so runtime is the right answer for them rather than a guess.
	{"abom_frontier", "supply_chain", "TEXT NOT NULL DEFAULT 'runtime'"},
	{"repository", "origin", "TEXT NOT NULL DEFAULT 'seed'"},
	// Nullable on purpose. NULL is "nobody enumerated this repository", which a gate has to be
	// able to tell apart from a branch it knows the name of.
	{"repository", "default_branch", "TEXT"},
	// When the branch listing was last read. NULL means nobody asked, which is not the same as a
	// repository that protects nothing.
	{"repository", "branches_checked_at", "TEXT"},
	// Zero on a reading made before the lock's graph was kept, so a covered lock is read again
	// once rather than keeping coverage with none of its entries.
	{"abom_lock_coverage", "graph_recorded", "INTEGER NOT NULL DEFAULT 0"},
	// Empty on a reading made before the reader was recorded, which no current reader matches,
	// so each such lock is read once more by a reader that says what it is.
	{"abom_lock_coverage", "reader", "TEXT NOT NULL DEFAULT ''"},
	// Rows written before the identity chain existed keep a run key computed without these, so
	// they will not match a current key and their pairs re-run once. That is the correct
	// outcome: nothing verified them.
	{"abom_query_run", "pack", "TEXT"},
	{"abom_query_run", "pack_digest", "TEXT"},
	{"abom_query_run", "cli_version", "TEXT"},
	// Same argument one level down, for the tree rather than the query. NULL says the filter was
	// never recorded, which must not be read as "the one in force now": those rows re-run once.
	{"abom_query_run", "source_filter", "TEXT"},
	// The deliberate exception to not back-filling provenance. `provider` was correctly left NULL
	// because the tool that produced an old finding genuinely was not recorded; here the opposite
	// holds — only one writer has ever existed and it builds one repository's own sparse checkout,
	// so `regular` is a fact about the code that produced every existing row rather than a guess
	// about it. It stays a fact only while no second database shape can precede this column, which
	// is what TestNoWriterRecordsAnExtendedRunYet protects.
	{"abom_query_run", "analysis_mode", "TEXT NOT NULL DEFAULT 'regular'"},
	// Nullable, and earlier rows stay NULL: which process wrote them was never recorded.
	{"abom_query_run", "recorded_by", "TEXT"},
	{"abom_suite_query", "pack_digest", "TEXT"},
	// Nullable, and rows written before it stay NULL. Defaulting them to a source would invent the
	// provenance the column exists to establish.
	{"abom_owned_scope", "declared_by", "TEXT"},
	// What the token could see when the accounts were read. NULL means nobody asked, which must
	// not be read as "carried none": one is an unanswered question, the other an answer.
	{"abom_account", "scopes", "TEXT"},
	{"abom_finding", "result_hash", "TEXT"},
	// Which producer made the claim. Nullable on purpose: rows recorded before this column say
	// nothing about their producer, and back-filling one would attest to things nobody checked.
	{"abom_finding", "provider", "TEXT"},
	{"abom_frontier", "expedited_at", "TEXT"},
	// Generations written before containment was stored carry 0, so the first delta after the
	// upgrade re-sends the tree once. That is the right answer: no client has seen it yet.
	{"abom_graph_generation", "containment_watermark", "INTEGER NOT NULL DEFAULT 0"},
	// Added here as well as in the DDL so an old table has the column before the rebuild that
	// makes it part of the key copies rows forward.
	{"abom_frontier", "ref", "TEXT NOT NULL DEFAULT ''"},
	// Tombstones. A DELETE would invalidate every delta cursor, which is a row-id watermark; a
	// mark keeps the ids monotonic and lets the delta say what disappeared. The generation is
	// what a cursor is expressed in, so a removal carries one or a client cannot be told about it
	// exactly once.
	{"abom_node", "removed_at", "TEXT"},
	{"abom_edge", "removed_at", "TEXT"},
	{"abom_node", "removed_generation", "INTEGER"},
	{"abom_edge", "removed_generation", "INTEGER"},
	// Databases that recorded submissions before renewal existed keep NULL here, which reads as
	// "what was sent is unknown" rather than "nothing was sent". Renewal treats that as needing a
	// submission, which is the safe direction: it re-sends rather than assuming still-current.
	{"abom_submission", "package_digest", "TEXT"},
	// Bumped by compaction, which deletes rows and so ends the meaning of every row-id cursor.
	{"abom_graph_generation", "epoch", "INTEGER NOT NULL DEFAULT 0"},
	// Derivable from rows already stored, so existing edges are filled by
	// ClassifyEdgeBoundaries rather than by re-analysis.
	{"abom_edge", "boundary", "TEXT"},
	// Facts about the relationship rather than either endpoint, filled by DeriveEdgeFacts. All
	// four are nullable because NULL is "nothing computed this": an edge ingested since the last
	// aggregate pass has no facts, which must not read as a reference in no style, on no route,
	// at no depth.
	{"abom_edge", "ref_style", "TEXT"},
	{"abom_edge", "advisory_route", "INTEGER"},
	{"abom_edge", "finding_route", "INTEGER"},
	{"abom_edge", "owned_depth", "INTEGER"},
	// Nullable: a result recorded before these were kept says nothing about where it stopped or
	// which tests it ran, and must not read as having stopped nowhere.
	{"abom_query_test", "failure_stage", "TEXT"},
	{"abom_query_test", "test_digest", "TEXT"},
	// cli_version is not here: it joins the key, which only migrateQueryTestKey's rebuild can do.
	{"abom_query_test", "fixtures_digest", "TEXT"},
	{"abom_query_test", "missing", "INTEGER"},
	{"abom_query_test", "unexpected", "INTEGER"},
	// Nullable: a request kept before targets were attributed says nobody is recorded as choosing
	// it, which must not read as the operator having chosen it.
	{"abom_query_request", "target_source", "TEXT"},
	{"abom_query_request", "source_run_id", "INTEGER"},
	{"abom_query_request", "target_detail", "TEXT"},
	// A member recorded before this has no way back to its findings, which reads as not evaluated
	// rather than as having found none.
	{"abom_analysis_member", "query_run_id", "INTEGER"},
	// A session kept before drafts existed belongs to a request, so it has no run and no query.
	{"abom_authoring_session", "run_id", "INTEGER"},
	{"abom_authoring_session", "query_id", "TEXT NOT NULL DEFAULT ''"},
	// Nullable: a session kept before spend was recorded says nothing about what it cost, which
	// must not read as having cost nothing.
	{"abom_authoring_session", "model_calls", "INTEGER"},
	{"abom_authoring_session", "input_tokens", "INTEGER"},
	{"abom_authoring_session", "output_tokens", "INTEGER"},
	{"abom_authoring_session", "nano_aiu", "INTEGER"},
	// A tree kept before ABOM printed its own came from a session, so nobody else asked for it.
	{"abom_authoring_ast", "printed_by", "TEXT NOT NULL DEFAULT ''"},
	{"abom_authoring_ast", "cli_version", "TEXT"},
	{"abom_analysis_run", "cli_version", "TEXT"},
	{"abom_analysis_run", "refusal", "TEXT"},
	{"abom_analysis_run", "held", "INTEGER NOT NULL DEFAULT 0"},
	{"abom_analysis_run", "started_at", "TEXT"},
	{"abom_analysis_run", "scope_name", "TEXT"},
	{"abom_query_run", "held", "INTEGER NOT NULL DEFAULT 0"},
	{"abom_held_result", "provider", "TEXT NOT NULL DEFAULT ''"},
	{"abom_held_result", "security_severity", "REAL"},
	{"abom_policy_simulation", "advisories", "INTEGER"},
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
` + frontierIndexDDL + `
` + refResolutionIndexDDL + `
CREATE INDEX IF NOT EXISTS idx_advisory_pkg    ON abom_advisory_package(package_name);
CREATE INDEX IF NOT EXISTS idx_exposure_repo   ON abom_exposure(repository_row_id);
CREATE INDEX IF NOT EXISTS idx_exposure_ghsa   ON abom_exposure(ghsa_id, hops);
CREATE INDEX IF NOT EXISTS idx_node_owner_repo ON abom_node(owner, repo);
CREATE INDEX IF NOT EXISTS idx_node_kind       ON abom_node(node_kind);
CREATE INDEX IF NOT EXISTS idx_node_external   ON abom_node(is_external);
CREATE INDEX IF NOT EXISTS idx_node_repo       ON abom_node(repository_row_id);
CREATE INDEX IF NOT EXISTS idx_node_container  ON abom_node(container_node_row_id);
CREATE INDEX IF NOT EXISTS idx_ledger_status   ON abom_ingest_ledger(status);
` + queryRunIndexDDL + `
` + observationIndexDDL + `
CREATE INDEX IF NOT EXISTS idx_catalog_has_db   ON abom_database_catalog(has_database);
CREATE INDEX IF NOT EXISTS idx_local_db_used    ON abom_local_database(used_seq);
CREATE INDEX IF NOT EXISTS idx_held_member ON abom_held_result(run_id, repository, rule_id);
`

// queryRunIndexDDL is created on open rather than after bulk loading, unlike every other index.
//
// Suite provenance joins each queue row to the runs on its commit, so the Discovery page needs
// it on a server that only ever reads and never loads. The deferral exists to keep million-row
// inserts cheap, and this table holds thousands.
const queryRunIndexDDL = `
CREATE INDEX IF NOT EXISTS idx_query_run_repo  ON abom_query_run(repository, db_commit);`

// observationIndexDDL serves the reader the fact key cannot: the traversal asks by attribute
// name across every subject, where UNIQUE leads with the subject.
//
// Created on open for the same reason as the run index, and for one more: an index added to
// indexDDL alone never reaches a database that already exists, because CreateIndexes runs only
// after a bulk load.
const observationIndexDDL = `
CREATE INDEX IF NOT EXISTS idx_observation_name ON abom_observation(subject_kind, name);`
