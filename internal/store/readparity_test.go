package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestPropertyScopeMatchCountsUseTheDeclaredCaseRules(t *testing.T) {
	s := propertyStore(t)
	terms := []PropertyTerm{{Name: "TEAM", Value: "a"}, {Name: "region", Value: "emea"}}
	got, err := s.PropertyScopeMatches("ACME", terms)
	if err != nil || got != 1 {
		t.Fatalf("matching property scope: count=%d error=%v, want 1", got, err)
	}
	if _, err := s.db.Exec(`INSERT INTO abom_repository_absence(repository, missing_at)
		VALUES ('ACME/ALPHA', ?)`, now()); err != nil {
		t.Fatal(err)
	}
	got, err = s.PropertyScopeMatches("acme", terms)
	if err != nil || got != 0 {
		t.Fatalf("absent repository counted in a property scope: count=%d error=%v", got, err)
	}
}

func TestJSONTextAndPropertyValuesKeepBinaryOrdering(t *testing.T) {
	s := newTestStore(t)
	values := []string{"actions/untrusted-checkout/high", "actions/untrusted-checkout-toctou/high"}
	if err := s.ReplacePropertyValues("acme/tool", []PropertyValue{{Name: "label", Value: values[0]}, {Name: "label", Value: values[1]}}); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := json.Marshal([]map[string]string{{"value": values[0]}, {"value": values[1]}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{values[1], values[0]}
	for _, query := range []struct{ sql, body string }{
		{`SELECT value FROM json_each(?) ORDER BY value`, string(body)},
		{`SELECT json_extract(j.value, '$.value') FROM json_each(?) j ORDER BY 1`, string(objects)},
		{`SELECT j.value FROM json_each(?) j JOIN abom_repository_property p ON p.value = j.value
		 WHERE p.property_name = 'LABEL' COLLATE NOCASE ORDER BY j.value`, string(body)},
	} {
		rows, err := s.db.Query(query.sql, query.body)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			got = append(got, value)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("JSON/property order=%v error=%v, want %v", got, err, want)
		}
	}
}

func TestReferencesKeepUsageFirstAndOrderEqualCountsByReference(t *testing.T) {
	s := newTestStore(t)
	for i, ref := range []string{"v2", "v1", "popular", "popular"} {
		seedDependency(t, s, "org/repo-"+string(rune('a'+i)), "vendor/tool", ref, i+1)
	}
	got, err := s.referencesTo("vendor/tool", AnalysisModeRegular)
	want := []string{"popular (2)", "v1 (1)", "v2 (1)"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("reference order=%v error=%v, want %v", got, err, want)
	}
}

func TestRemediationActionsAreOrderedWithinTheirAdvisory(t *testing.T) {
	s := newTestStore(t)
	advisoryAgainst(t, s, "GHSA-actions", "high", "github/codeql-action", "", "")
	for i, action := range []string{"init", "autobuild", "analyze"} {
		key := "github/codeql-action/" + action
		seedDependency(t, s, "org/repo", key, action, i+1)
		exposeRepo(t, s, "org/repo", "GHSA-actions", key, 1)
	}
	items, err := s.RemediationPriority(10, RepositoryScope{})
	if err != nil || len(items) != 1 {
		t.Fatalf("remediation=%v error=%v", items, err)
	}
	want := []string{"github/codeql-action/analyze", "github/codeql-action/autobuild", "github/codeql-action/init"}
	if !reflect.DeepEqual(items[0].Actions, want) || items[0].DirectRepos != 1 {
		t.Fatalf("advisory actions=%v direct=%d, want %v and 1", items[0].Actions, items[0].DirectRepos, want)
	}
}

func TestFrontierOrderingPreservesTimestampPrecisionAndClosesTrueTies(t *testing.T) {
	s := newTestStore(t)
	at := time.Date(2026, 10, 9, 0, 0, 0, 670970000, time.UTC)
	q := s.Frontier(FrontierOptions{Now: func() time.Time { return at }})
	if _, err := q.Enqueue([]Item{{NodeKey: "vendor/z-prior", Owner: "vendor", Repo: "z-prior", Ref: "old", Depth: 2}}); err != nil {
		t.Fatal(err)
	}
	at = at.Add(9 * time.Microsecond)
	items := []Item{
		{NodeKey: "vendor/tool", Owner: "vendor", Repo: "tool", Ref: "z", Depth: 2},
		{NodeKey: "vendor/tool", Owner: "vendor", Repo: "tool", Ref: "a", Depth: 2},
		{NodeKey: "vendor/alpha", Owner: "vendor", Repo: "alpha", Ref: "b", Depth: 2},
		{NodeKey: "vendor/z-depth", Owner: "vendor", Repo: "z-depth", Ref: "d", Depth: 1},
	}
	if _, err := q.Enqueue(items); err != nil {
		t.Fatal(err)
	}
	want := []string{"vendor/z-depth@d", "vendor/z-prior@old", "vendor/alpha@b", "vendor/tool@a", "vendor/tool@z"}
	listed, err := s.FrontierSnapshot("", 10)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, row := range listed {
		keys = append(keys, row.NodeKey+"@"+row.Ref)
	}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("displayed priority=%v, want %v", keys, want)
	}
	claimed, err := q.Claim(10)
	if err != nil {
		t.Fatal(err)
	}
	keys = keys[:0]
	for _, row := range claimed {
		keys = append(keys, row.NodeKey+"@"+row.Ref)
	}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("claim priority=%v, want %v", keys, want)
	}
}
