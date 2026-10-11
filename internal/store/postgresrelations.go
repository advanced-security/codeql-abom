package store

import (
	"fmt"
	"strings"
)

// PostgreSQL readers use CTEs rather than temporary DDL, so they can run on a physical standby.
func (d *database) expandRelations(query string) (string, error) {
	if !d.postgres || !strings.Contains(query, "temp.declared_edge_") {
		return query, nil
	}
	t, err := lexSQL(query)
	if err != nil {
		return "", err
	}
	d.relationMu.RLock()
	defer d.relationMu.RUnlock()
	var out []sqlToken
	for i := 0; i < len(t); i++ {
		if i+2 < len(t) && tokenIs(t[i], "temp") && t[i+1].text == "." &&
			strings.HasPrefix(t[i+2].text, "declared_edge_") {
			q, ok := d.relations[t[i+2].text]
			if !ok {
				return "", fmt.Errorf("PostgreSQL declaration relation %s was not prepared", t[i+2].text)
			}
			out = append(out, sqlFragment("("+q+")"))
			i += 2
			continue
		}
		out = append(out, t[i])
	}
	return renderSQL(out), nil
}

func bindDeclarationAnswer(query, answers string) (string, error) {
	t, err := lexSQL(query)
	if err != nil {
		return "", err
	}
	count := 0
	for i := range t {
		if t[i].kind == 'p' {
			if t[i].text != "?" {
				return "", fmt.Errorf("unexpected declaration parameter %s", t[i].text)
			}
			t[i] = sqlToken{"'" + strings.ReplaceAll(answers, "'", "''") + "'", 'q'}
			count++
		}
	}
	if count != 1 {
		return "", fmt.Errorf("declaration CTE has %d inputs, expected one", count)
	}
	return renderSQL(t), nil
}

func (s *Store) postgresDeclarations(name, mode, answers string) error {
	edge, err := bindDeclarationAnswer(`WITH `+declaredEdges(mode)+` SELECT * FROM declared_edge`, answers)
	if err != nil {
		return err
	}
	starting, err := bindDeclarationAnswer(`WITH `+liveDeclared(mode)+`
		SELECT DISTINCT d.repository_row_id, d.dst_node_row_id
		  FROM declared d JOIN repository r ON r.row_id = d.repository_row_id
		 WHERE LOWER(r.repository_full_name) NOT IN (SELECT repository FROM live_answer)
		    OR EXISTS (SELECT 1 FROM live_answer a
		                WHERE a.repository = LOWER(r.repository_full_name) AND a.starting = 1
		                  AND (d.commit_sha = '' OR a.commit_sha = d.commit_sha))`, answers)
	if err != nil {
		return err
	}
	s.db.relationMu.Lock()
	defer s.db.relationMu.Unlock()
	if s.db.relations == nil {
		s.db.relations = map[string]string{}
	}
	s.db.relations[name], s.db.relations[name+"_starting"] = edge, starting
	return nil
}
