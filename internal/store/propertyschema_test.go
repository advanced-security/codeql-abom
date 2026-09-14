package store

import "testing"

// A required property with a default has no unset state: every repository carries a value whether
// anybody chose one or not. Reporting that bucket like any other tells a reader that eight teams
// picked unit "0" when the truth is that none did — which is the default bucket this plan forbids,
// arriving from upstream rather than from us. Measured on has-ghas: `business_id` is required with
// `default_value: "0"`, and 8 of 25 repositories sit in it.
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
