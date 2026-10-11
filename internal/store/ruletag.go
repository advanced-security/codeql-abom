package store

import (
	"fmt"
	"strings"
)

// tagsFromDocumentation selects the tags of whichever recorded rules `scope` names.
const tagsFromDocumentation = `
	INSERT OR IGNORE INTO abom_query_run_rule_tag (query_run_id, rule_id, tag)
	SELECT r.query_run_id, r.rule_id, j.value
	FROM abom_query_run_rule r, json_each(r.documentation_json, '$.tags') j
	WHERE json_valid(r.documentation_json) AND j.type = 'text' AND `

// refreshRuleTags rewrites one recorded rule's tags from its documentation as it now stands.
func refreshRuleTags(tx *transaction, run int64, rule string) error {
	if _, err := tx.Exec(`DELETE FROM abom_query_run_rule_tag WHERE query_run_id = ? AND rule_id = ?`,
		run, rule); err != nil {
		return fmt.Errorf("clearing the tags of %s: %w", rule, err)
	}
	if _, err := tx.Exec(tagsFromDocumentation+`r.query_run_id = ? AND r.rule_id = ?`, run, rule); err != nil {
		return fmt.Errorf("recording the tags of %s: %w", rule, err)
	}
	return nil
}

// backfillRuleTags reads the tags of documentation recorded before tags were kept apart.
func (s *Store) backfillRuleTags() error {
	if _, err := s.db.Exec(tagsFromDocumentation + `NOT EXISTS (SELECT 1 FROM abom_query_run_rule_tag t
		WHERE t.query_run_id = r.query_run_id AND t.rule_id = r.rule_id)`); err != nil {
		return fmt.Errorf("reading recorded rule tags: %w", err)
	}
	return nil
}

// tagClause matches a finding whose rule declared any of `tags`, exactly, in the evaluation that
// reported it. Not inList, which lower-cases for account names: a tag is compared as written.
func tagClause(alias string, tags []string) (string, []any) {
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(tags)), ", ")
	args := make([]any, 0, len(tags))
	for _, tag := range tags {
		args = append(args, tag)
	}
	return `EXISTS (SELECT 1 FROM abom_query_run_rule_tag t
	                 WHERE t.query_run_id = ` + alias + `.query_run_id
	                   AND t.rule_id = ` + alias + `.rule_id
	                   AND t.tag IN (` + marks + `))`, args
}
