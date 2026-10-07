package ghauth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const schemaCall = "api orgs/has-ghas/properties/schema"
const valuesCall = "api orgs/has-ghas/properties/values?per_page=100 --paginate"

// The picker offers what the organization defines, with who may set each one, which is what
// decides whether a property may scope ownership at all.
func TestAnAccountsPropertySchemaIsReadWithWhoMaySetEachProperty(t *testing.T) {
	gh := &fakeGH{replies: map[string]reply{schemaCall: {out: []byte(`[
	  {"property_name":"phi-scan","value_type":"single_select","required":true,"default_value":"scan-pending",
	   "allowed_values":["scan-pending","no-phi-detected"],"values_editable_by":"org_and_repo_actors"},
	  {"property_name":"github-codeql-config-file","value_type":"string","required":true,
	   "default_value":"remote=x","values_editable_by":"org_actors","description":"Config"}]`)}}}

	got, err := ReadPropertySchema(context.Background(), gh.run, "has-ghas")
	if err != nil {
		t.Fatalf("ReadPropertySchema: %v", err)
	}
	if len(got) != 2 || got[0].Name != "github-codeql-config-file" || got[1].Name != "phi-scan" {
		t.Fatalf("got %+v, want both definitions sorted by name", got)
	}
	phi := got[1]
	if phi.ValuesEditableBy != "org_and_repo_actors" || phi.ValueType != "single_select" ||
		!phi.HasDefault || phi.DefaultValue != "scan-pending" || len(phi.AllowedValues) != 2 || !phi.Required {
		t.Errorf("phi-scan read as %+v", phi)
	}
}

// A multi-select's default arrives as a list; read as a string it failed the whole schema, and
// the picker with it.
func TestAMultiSelectDefaultIsOfferedAsAList(t *testing.T) {
	gh := &fakeGH{replies: map[string]reply{schemaCall: {out: []byte(`[
	  {"property_name":"tags","value_type":"multi_select","allowed_values":["a","b","c"],
	   "default_value":["a","c"],"values_editable_by":"org_actors"},
	  {"property_name":"tier","value_type":"single_select","default_value":"2","values_editable_by":"org_actors"}]`)}}}

	got, err := ReadPropertySchema(context.Background(), gh.run, "has-ghas")
	if err != nil {
		t.Fatalf("ReadPropertySchema: %v", err)
	}
	tags, tier := got[0], got[1]
	if tags.HasDefault || len(tags.DefaultValues) != 2 || tags.DefaultValues[0] != "a" || tags.DefaultValues[1] != "c" {
		t.Errorf("tags read as %+v, want the defaults a and c and no single default", tags)
	}
	if !tier.HasDefault || tier.DefaultValue != "2" || tier.DefaultValues == nil || len(tier.DefaultValues) != 0 {
		t.Errorf("tier read as %+v, want the single default 2 and an empty list", tier)
	}
}

// Values arrive one row per value, as the store keeps them, so a multi-select is matched the same
// way here as it is when ownership is decided.
func TestAnAccountsPropertyValuesAreReadOneRowPerValue(t *testing.T) {
	gh := &fakeGH{replies: map[string]reply{valuesCall: {out: []byte(`[
	  {"repository_name":"one","repository_full_name":"has-ghas/one",
	   "properties":[{"property_name":"team","value":"a"},{"property_name":"tags","value":["x","y"]},
	                 {"property_name":"unset","value":null}]}][
	  {"repository_name":"two","repository_full_name":"has-ghas/two","properties":[]}]`)}}}

	got, err := ReadPropertyValues(context.Background(), gh.run, "has-ghas")
	if err != nil {
		t.Fatalf("ReadPropertyValues: %v", err)
	}
	if len(got) != 2 || got[0].Repository != "has-ghas/one" || len(got[0].Values) != 3 {
		t.Fatalf("got %+v, want has-ghas/one with three values and has-ghas/two with none", got)
	}
	for _, v := range got[0].Values {
		if v.Name == "unset" {
			t.Errorf("an unset value was read as %q, a value nobody chose", v.Value)
		}
	}
}

// A refusal names its cause, like a repository listing's: single sign-on or an unknown account.
func TestAPropertyReadGhRefusedNamesTheRemedy(t *testing.T) {
	gh := &fakeGH{replies: map[string]reply{
		"api orgs/codeql/properties/schema": {err: errors.New(
			"gh api: exit status 1: gh: Resource protected by organization SAML enforcement. (HTTP 403)")},
	}}
	_, err := ReadPropertySchema(context.Background(), gh.run, "codeql")
	var p *Problem
	if !errors.As(err, &p) || !strings.Contains(p.Remedy, "orgs/codeql/sso") {
		t.Errorf("err = %v, want the single sign-on remedy", err)
	}
	if _, err := ReadPropertySchema(context.Background(), gh.run, "-x"); err == nil {
		t.Error("a name GitHub could not issue was asked")
	}
}

// A refusal is said in the words of what was asked for: the account is already declared, so the
// listing's "declare it by name" points nowhere, and these are properties, not repositories.
func TestAPropertyReadSaysWhatCouldNotBeReadAndWhatStillWorks(t *testing.T) {
	gh := &fakeGH{replies: map[string]reply{
		"api orgs/acme/properties/schema": {err: errors.New("gh api: exit status 1: gh: Not Found (HTTP 404)")},
		valuesCall:                        {err: errors.New("gh api: exit status 1: gh: Server Error (HTTP 502)")},
	}}
	_, err := ReadPropertySchema(context.Background(), gh.run, "acme")
	var p *Problem
	if !errors.As(err, &p) || !strings.Contains(p.Reason, "custom properties") ||
		!strings.Contains(p.Remedy, "own --property") || strings.Contains(p.Remedy, "declare it by name") {
		t.Errorf("err = %v, want the property scope's own remedy", err)
	}
	_, err = ReadPropertyValues(context.Background(), gh.run, "has-ghas")
	if !errors.As(err, &p) || !strings.Contains(p.Reason, "custom properties") || strings.Contains(p.Reason, "repositories") {
		t.Errorf("err = %v, want a reason naming custom properties", err)
	}
}
