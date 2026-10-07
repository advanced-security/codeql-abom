package store

import (
	"strings"
	"testing"
)

// A required property with a default has no unset state: every repository carries a value whether
// anybody chose one or not. Reporting that bucket like any other tells a reader teams picked the
// default when none did.
func TestTheBucketNobodyChoseIsLabelledAsTheDefault(t *testing.T) {
	s := ownedStore(t, "has-ghas/a", "has-ghas/b", "has-ghas/c")
	if err := s.ReplacePropertySchema("has-ghas", []PropertyDefinition{
		{Name: "business_id", DefaultValue: "0", HasDefault: true},
	}); err != nil {
		t.Fatal(err)
	}
	for repo, unit := range map[string]string{"has-ghas/a": "0", "has-ghas/b": "0", "has-ghas/c": "3"} {
		if err := s.ReplacePropertyValues(repo, []PropertyValue{
			{Name: "business_id", Value: unit}}); err != nil {
			t.Fatal(err)
		}
	}

	units, err := s.PropertyBreakdown("business_id")
	if err != nil {
		t.Fatal(err)
	}
	byValue := map[string]PropertyCount{}
	for _, v := range units.Values {
		byValue[v.Value] = v
	}
	if !byValue["0"].IsDefault {
		t.Error(`"0" is the organization's default and is reported as a unit somebody chose`)
	}
	if byValue["3"].IsDefault {
		t.Error(`"3" was chosen and is reported as the default`)
	}
	if units.DefaultValue != "0" {
		t.Errorf("DefaultValue = %q, want the value a repository gets for doing nothing",
			units.DefaultValue)
	}
}

// A multi-select's default is a list, and a repository that chose nothing carries every value in
// it, so each of those buckets is labeled; a value nobody gets for free is not.
func TestAMultiSelectsDefaultBucketsAreLabeledAsTheDefault(t *testing.T) {
	s := ownedStore(t, "has-ghas/a", "has-ghas/b")
	if err := s.ReplacePropertySchema("has-ghas", []PropertyDefinition{
		{Name: "tags", ValueType: "multi_select", DefaultValues: []string{"x", "z"}},
	}); err != nil {
		t.Fatal(err)
	}
	for repo, tags := range map[string][]string{"has-ghas/a": {"x", "z"}, "has-ghas/b": {"y"}} {
		var values []PropertyValue
		for _, tag := range tags {
			values = append(values, PropertyValue{Name: "tags", Value: tag})
		}
		if err := s.ReplacePropertyValues(repo, values); err != nil {
			t.Fatal(err)
		}
	}

	tags, err := s.PropertyBreakdown("tags")
	if err != nil {
		t.Fatal(err)
	}
	labeled := map[string]bool{}
	for _, v := range tags.Values {
		labeled[v.Value] = v.IsDefault
	}
	if !labeled["x"] || !labeled["z"] || labeled["y"] {
		t.Errorf("labeled %v, want x and z as the default and y as chosen", labeled)
	}
	if tags.HasDefault || strings.Join(tags.DefaultValues, ",") != "x,z" {
		t.Errorf("HasDefault %v, DefaultValues %v; want no single default and the list x, z",
			tags.HasDefault, tags.DefaultValues)
	}
}

// A property with no default genuinely can be unset, and nothing should be labeled. Marking a
// value as the default when the organization declared none is the same invention in reverse.
func TestAKeyWithNoDefaultLabelsNothing(t *testing.T) {
	s := ownedStore(t, "has-ghas/a")
	if err := s.ReplacePropertySchema("has-ghas", []PropertyDefinition{
		{Name: "app_name", HasDefault: false},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePropertyValues("has-ghas/a", []PropertyValue{
		{Name: "app_name", Value: ""}}); err != nil {
		t.Fatal(err)
	}

	units, err := s.PropertyBreakdown("app_name")
	if err != nil {
		t.Fatal(err)
	}
	if units.HasDefault {
		t.Error("the organization declared no default and one was reported anyway")
	}
	for _, v := range units.Values {
		if v.IsDefault {
			t.Errorf("%q was labeled the default of a key that has none", v.Value)
		}
	}
}

// An organization whose schema has never been read must not have its values labeled from a
// remembered default, and must not claim there is none either.
func TestASchemaNobodyReadLabelsNothingEitherWay(t *testing.T) {
	s := ownedStore(t, "has-ghas/a")
	if err := s.ReplacePropertyValues("has-ghas/a", []PropertyValue{
		{Name: "business_id", Value: "0"}}); err != nil {
		t.Fatal(err)
	}

	units, err := s.PropertyBreakdown("business_id")
	if err != nil {
		t.Fatal(err)
	}
	if units.HasDefault {
		t.Error("no schema was read, so whether there is a default is unknown rather than known")
	}
	for _, v := range units.Values {
		if v.IsDefault {
			t.Error("a value was labeled the default on the strength of no schema at all")
		}
	}
}

// Who may edit a property is what a property scope is held against, so it is stored with the
// schema, and an older read that never asked stays unknown rather than reading as permissive.
func TestASchemaRecordsWhoMayEditEachPropertyAndWhenItWasRead(t *testing.T) {
	s := newTestStore(t)
	if _, known, err := s.PropertySchemaReadAt("has-ghas"); err != nil || known {
		t.Fatalf("PropertySchemaReadAt before any read = known %v, err %v; want unknown", known, err)
	}
	if err := s.ReplacePropertySchema("has-ghas", []PropertyDefinition{
		{Name: "business_id", HasDefault: true, DefaultValue: "0", ValueType: "string",
			ValuesEditableBy: "org_and_repo_actors"},
		{Name: "team", ValueType: "multi_select", ValuesEditableBy: "org_actors", DefaultValues: []string{"a", "b"}},
		{Name: "legacy", ValueType: "string"},
	}); err != nil {
		t.Fatal(err)
	}

	defs, err := s.PropertySchema("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]PropertyDefinition{}
	for _, d := range defs {
		byName[d.Name] = d
	}
	if team := byName["team"]; team.ValuesEditableBy != "org_actors" || team.ValueType != "multi_select" ||
		team.HasDefault || strings.Join(team.DefaultValues, ",") != "a,b" {
		t.Errorf("team read back as %+v", byName["team"])
	}
	if byName["business_id"].ValuesEditableBy != "org_and_repo_actors" || !byName["business_id"].HasDefault {
		t.Errorf("business_id read back as %+v", byName["business_id"])
	}
	if byName["legacy"].ValuesEditableBy != "" || byName["legacy"].DefaultValues != nil {
		t.Errorf("legacy says %q may edit it; GitHub said nothing", byName["legacy"].ValuesEditableBy)
	}
	if _, known, err := s.PropertySchemaReadAt("HAS-GHAS"); err != nil || !known {
		t.Errorf("PropertySchemaReadAt after a read = known %v, err %v; want the read on record", known, err)
	}
}
