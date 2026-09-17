package store

import (
	"fmt"
	"sort"
)

// DefaultTierSuites is what a tier runs until somebody says otherwise.
//
// Direct dependencies are named by a repository under review and can be changed there, so they
// earn the findings pass as well as the graph. Transitive ones arrive through an intermediary
// and outnumber them several times over, so they get the graph alone — that is a cost decision,
// not a claim that they matter less.
//
// Source repositories are absent because they are not in this queue at all: they are what the
// queue is measured from.
var DefaultTierSuites = map[string][]string{
	TierDirect: {
		"advanced-security/abom-actions-graph-extraction-src:codeql-suites/abom-dependency-graph.qls",
		"advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls",
	},
	TierTransitive: {
		"advanced-security/abom-actions-graph-extraction-src:codeql-suites/abom-dependency-graph.qls",
	},
}

// renamedSuites maps the file paths bindings used before the suites moved into their own packs.
//
// A stored binding is configuration somebody chose, so the move rewrites it rather than reseeding
// the defaults over it. Anything absent here is left verbatim: a binding may name a suite from an
// installed pack, and dropping one would silently change what a crawl runs.
var renamedSuites = map[string]string{
	"ql/actions/abom/src/codeql-suites/abom-dependency-graph.qls": "advanced-security/abom-actions-graph-extraction-src:codeql-suites/abom-dependency-graph.qls",
	"ql/actions/abom/src/codeql-suites/abom-findings.qls":         "advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls",
	"ql/actions/abom/src/codeql-suites/abom-selfcheck.qls":        "advanced-security/abom-actions-selfcheck-src:codeql-suites/abom-selfcheck.qls",
}

// migrateSuiteNames rewrites bindings left over from before suites were named by their pack.
func (s *Store) migrateSuiteNames() error {
	for old, current := range renamedSuites {
		// The tier may already hold the new name, and (tier, suite) is unique, so a plain UPDATE
		// would fail rather than converge. Dropping the stale row is the same end state.
		if _, err := s.db.Exec(`
			DELETE FROM abom_tier_suite
			WHERE suite = ?
			  AND EXISTS (SELECT 1 FROM abom_tier_suite n
			              WHERE n.tier = abom_tier_suite.tier AND n.suite = ?)`,
			old, current); err != nil {
			return fmt.Errorf("clearing the superseded binding for %s: %w", old, err)
		}
		if _, err := s.db.Exec(
			`UPDATE abom_tier_suite SET suite = ? WHERE suite = ?`, current, old); err != nil {
			return fmt.Errorf("renaming the binding for %s: %w", old, err)
		}
	}
	return nil
}

// TierSuites reads what each tier runs.
//
// Seeded at migration rather than defaulted on read, so the table is the whole answer: a tier
// bound to nothing means "analyze nothing here", which a read-time fallback could not express.
func (s *Store) TierSuites() (map[string][]string, error) {
	out := map[string][]string{TierDirect: {}, TierTransitive: {}}
	rows, err := s.db.Query(`SELECT tier, suite FROM abom_tier_suite ORDER BY tier, suite`)
	if err != nil {
		return nil, fmt.Errorf("reading tier suites: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tier, suite string
		if err := rows.Scan(&tier, &suite); err != nil {
			return nil, fmt.Errorf("scanning a tier suite: %w", err)
		}
		out[tier] = append(out[tier], suite)
	}
	return out, rows.Err()
}

// SetTierSuites replaces what one tier runs, leaving every other tier alone.
func (s *Store) SetTierSuites(tier string, suites []string) error {
	if _, err := tierWhere(tier); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM abom_tier_suite WHERE tier = ?`, tier); err != nil {
		return fmt.Errorf("clearing the %s tier: %w", tier, err)
	}
	for _, suite := range dedupe(suites) {
		if _, err := tx.Exec(
			`INSERT INTO abom_tier_suite (tier, suite) VALUES (?, ?)`, tier, suite); err != nil {
			return fmt.Errorf("binding %s to %s: %w", tier, suite, err)
		}
	}
	// A tier bound to nothing still has to be on record as configured, or the next migration
	// would seed the defaults back over a deliberate choice.
	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO abom_tier_configured (tier) VALUES (?)`, tier); err != nil {
		return fmt.Errorf("recording that %s was configured: %w", tier, err)
	}
	return tx.Commit()
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// seedTierSuites writes the defaults for any tier nobody has configured.
//
// Owned has no default of its own: it is seeded from whatever direct is bound to, so introducing
// the tier reclassifies dependencies without changing what runs against them. A default of its
// own would silently move the corpus's analysis — cost and results — on the first round after an
// upgrade, which is not a thing to decide on someone's behalf.
func (s *Store) seedTierSuites() error {
	defaults := map[string][]string{}
	for tier, suites := range DefaultTierSuites {
		defaults[tier] = suites
	}
	if _, ok := defaults[TierOwned]; !ok {
		bound, err := s.TierSuites()
		if err != nil {
			return err
		}
		inherited := bound[TierDirect]
		if len(inherited) == 0 {
			inherited = DefaultTierSuites[TierDirect]
		}
		defaults[TierOwned] = inherited
	}

	for tier, suites := range defaults {
		var configured int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM abom_tier_configured WHERE tier = ?`, tier).
			Scan(&configured); err != nil {
			return fmt.Errorf("reading whether %s was configured: %w", tier, err)
		}
		if configured > 0 {
			continue
		}
		for _, suite := range suites {
			if _, err := s.db.Exec(
				`INSERT OR IGNORE INTO abom_tier_suite (tier, suite) VALUES (?, ?)`,
				tier, suite); err != nil {
				return fmt.Errorf("seeding %s: %w", tier, err)
			}
		}
		if _, err := s.db.Exec(
			`INSERT OR REPLACE INTO abom_tier_configured (tier) VALUES (?)`, tier); err != nil {
			return fmt.Errorf("recording the %s default: %w", tier, err)
		}
	}
	return nil
}
