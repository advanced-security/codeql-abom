package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Who may set a property decides whether it may scope ownership: a value a repository's own
// administrator can change would let them move it into or out of the operator's responsibility.
func TestASchemaSaysWhoMayEditEachPropertyAndItsType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
		  {"property_name":"business_id","value_type":"string","required":true,"default_value":"0",
		   "values_editable_by":"org_and_repo_actors"},
		  {"property_name":"team","value_type":"multi_select","allowed_values":["a","b"],
		   "values_editable_by":"org_actors"},
		  {"property_name":"legacy","value_type":"string"}]`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", nil).PropertySchema(context.Background(), "has-ghas")
	if err != nil {
		t.Fatalf("PropertySchema: %v", err)
	}
	want := []PropertyDefinition{
		{Name: "business_id", HasDefault: true, DefaultValue: "0", ValueType: "string",
			ValuesEditableBy: "org_and_repo_actors"},
		{Name: "team", ValueType: "multi_select", ValuesEditableBy: "org_actors"},
		// An answer that does not say who may edit leaves it unsaid, never "anybody".
		{Name: "legacy", ValueType: "string"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("definition %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

// GitHub answers a multi-select's default as a list. Read as a string, it failed the whole schema,
// leaving every default in the organization unknown and every property scope on it held.
func TestAMultiSelectDefaultIsReadAsAListBesideTheOthers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
		  {"property_name":"tags","value_type":"multi_select","allowed_values":["a","b","c"],
		   "default_value":["a","c"],"values_editable_by":"org_actors"},
		  {"property_name":"tier","value_type":"single_select","allowed_values":["1","2"],
		   "default_value":"2","values_editable_by":"org_actors"}]`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", nil).PropertySchema(context.Background(), "has-ghas")
	if err != nil {
		t.Fatalf("PropertySchema: %v", err)
	}
	want := []PropertyDefinition{
		// A repository that chose nothing carries every listed value, and no single one of them.
		{Name: "tags", ValueType: "multi_select", ValuesEditableBy: "org_actors", DefaultValues: []string{"a", "c"}},
		{Name: "tier", HasDefault: true, DefaultValue: "2", ValueType: "single_select", ValuesEditableBy: "org_actors"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
