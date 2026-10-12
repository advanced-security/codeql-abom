package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// propertyScopedServer is acme with team (organization-only) and business_id (repository-editable), and
// three repositories on record: alpha and beta on team a, gamma on team b.
func propertyScopedServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	srv, s := newServer(t)
	if err := s.ReplacePropertySchema("acme", []store.PropertyDefinition{
		{Name: "team", ValueType: "single_select", ValuesEditableBy: store.PropertyEditableByOrganization},
		{Name: "business_id", ValueType: "string", ValuesEditableBy: "org_and_repo_actors"},
	}); err != nil {
		t.Fatal(err)
	}
	for repo, unit := range map[string]string{"acme/alpha": "a", "acme/beta": "a", "acme/gamma": "b"} {
		if _, err := s.InsertOwnedRepository(repo); err != nil {
			t.Fatal(err)
		}
		if err := s.ReplacePropertyValues(repo, []store.PropertyValue{{Name: "team", Value: unit}}); err != nil {
			t.Fatal(err)
		}
	}
	return srv, s
}

type servedScope struct {
	Owner      string `json:"owner"`
	Properties []struct {
		Terms      []store.PropertyTerm `json:"terms"`
		Held       bool                 `json:"held"`
		HeldReason string               `json:"heldReason"`
		Matching   int                  `json:"matching"`
	} `json:"properties"`
}

const teamA = `[{"owner":"acme","kind":"organization","enabled":true,"includesOnly":true,"includes":[],
  "properties":[{"terms":[{"name":"team","value":"a"}]}]}]`

// A scope arrives with how many repositories it covers now, so the reader sees what it did rather
// than the terms alone.
func TestAPropertyScopeIsServedWithWhatItCovers(t *testing.T) {
	srv, _ := propertyScopedServer(t)
	if rec := put(t, srv, "/api/owned-scope", teamA); rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}

	got := decode[[]servedScope](t, get(t, srv, "/api/owned-scope"))
	if len(got) != 1 || len(got[0].Properties) != 1 {
		t.Fatalf("served %+v, want acme with its one scope", got)
	}
	if p := got[0].Properties[0]; p.Matching != 2 || p.Held || p.Terms[0].Value != "a" {
		t.Errorf("scope served as %+v, want team=a covering 2", p)
	}
}

// The schema's refusal reaches the reader in its own words, as a request they can correct.
func TestAPropertyScopeTheSchemaRulesOutIsRefusedWithTheReason(t *testing.T) {
	srv, _ := propertyScopedServer(t)
	rec := put(t, srv, "/api/owned-scope", `[{"owner":"acme","kind":"organization","enabled":true,
	  "includesOnly":true,"properties":[{"terms":[{"name":"business_id","value":"7"}]}]}]`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "repository administrators") ||
		strings.Contains(rec.Body.String(), "store:") {
		t.Errorf("PUT answered %d %s, want 400 naming repository administrators in a sentence", rec.Code, rec.Body)
	}
}

// A declaration the store could not write is ABOM failing, not the reader asking for something
// refused, so it is not answered as a 400 the page would show as the reader's mistake.
func TestADeclarationTheStoreCouldNotWriteIsAFault(t *testing.T) {
	srv, s := propertyScopedServer(t)
	s.Close()
	if rec := put(t, srv, "/api/owned-scope", teamA); rec.Code < 500 {
		t.Errorf("PUT on a closed store answered %d %s, want a server fault", rec.Code, rec.Body)
	}
}

// A candidate scope is judged before it is written, from the values on record, and the preview
// says how many it could not judge rather than counting them as left out.
func TestThePreviewJudgesACandidatePropertyScope(t *testing.T) {
	srv, s := propertyScopedServer(t)
	if _, err := s.InsertOwnedRepository("acme/delta"); err != nil {
		t.Fatal(err)
	}
	put(t, srv, "/api/owned-scope", `[{"owner":"acme","kind":"organization","enabled":true}]`)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localRequest(http.MethodPost, "/api/owned-scope/preview",
		strings.NewReader(teamA)))
	var preview struct {
		RootsNow, RootsNext, NotJudged int
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	if preview.RootsNow != 4 || preview.RootsNext != 2 || preview.NotJudged != 1 {
		t.Errorf("preview %+v, want 4 now, 2 next and delta not judged", preview)
	}
}

type servedProperties struct {
	Owner       string `json:"owner"`
	Definitions []struct {
		Name       string `json:"name"`
		Declarable bool   `json:"declarable"`
		Reason     string `json:"reason"`
		Values     []struct {
			Value        string `json:"value"`
			Repositories int    `json:"repositories"`
			IsDefault    bool   `json:"isDefault"`
		} `json:"values"`
	} `json:"definitions"`
	Repositories int             `json:"repositories"`
	ClauseCounts []int           `json:"clauseCounts"`
	Problem      *ghauth.Problem `json:"problem"`
}

func propertyServer(t *testing.T) (*Server, *[]string) {
	t.Helper()
	srv, _ := newServer(t)
	var asked []string
	srv.ReadProperties = func(_ context.Context, owner string) ([]ghauth.PropertyDefinition, []ghauth.RepositoryProperties, error) {
		asked = append(asked, owner)
		if owner == "codeql" {
			return nil, nil, &ghauth.Problem{Reason: "codeql requires single sign-on.", Remedy: "authorize it"}
		}
		definitions := []ghauth.PropertyDefinition{
			{Name: "team", ValueType: "single_select", ValuesEditableBy: "org_actors",
				AllowedValues: []string{"a", "b", "c"}, HasDefault: true, DefaultValue: "a"},
			{Name: "business_id", ValueType: "string", ValuesEditableBy: "org_and_repo_actors"},
			{Name: "legacy", ValueType: "string"},
			{Name: "flag", ValueType: "true_false", ValuesEditableBy: "org_actors"},
			{Name: "tags", ValueType: "multi_select", ValuesEditableBy: "org_actors",
				AllowedValues: []string{"x", "y"}, DefaultValues: []string{"x"}},
			{Name: "region", ValueType: "string", ValuesEditableBy: "org_actors"},
		}
		values := []ghauth.RepositoryProperties{
			{Repository: "acme/alpha", Values: []ghauth.PropertyValue{{Name: "team", Value: "a"},
				{Name: "business_id", Value: "7"}, {Name: "flag", Value: "true"}, {Name: "region", Value: "emea"}}},
			{Repository: "acme/beta", Values: []ghauth.PropertyValue{{Name: "team", Value: "a"}, {Name: "REGION", Value: "emea"}}},
			{Repository: "acme/gamma", Values: []ghauth.PropertyValue{{Name: "team", Value: "b"}}},
			{Repository: "acme/delta", Values: []ghauth.PropertyValue{{Name: "team", Value: "retired"}}},
		}
		return definitions, values, nil
	}
	return srv, &asked
}

// Whether a property may scope ownership is the server's answer, never re-derived in the browser:
// two derivations would be two answers to what is yours.
func TestTheServerSaysWhichPropertiesMayScopeOwnershipAndWhy(t *testing.T) {
	srv, _ := propertyServer(t)
	rec := httptest.NewRecorder()
	req := controlRequest("/api/accounts/acme/properties")
	req.Body = http.NoBody
	srv.Handler().ServeHTTP(rec, req)
	got := decode[servedProperties](t, rec)

	by := map[string]bool{}
	reasons := map[string]string{}
	for _, d := range got.Definitions {
		by[d.Name], reasons[d.Name] = d.Declarable, d.Reason
	}
	if !by["team"] || by["business_id"] || by["legacy"] {
		t.Errorf("declarable %v, want only team", by)
	}
	if !strings.Contains(reasons["business_id"], "repository administrators") || reasons["legacy"] == "" {
		t.Errorf("reasons %v, want each refusal explained", reasons)
	}
	if got.Repositories != 4 {
		t.Errorf("counted %d repositories, want 4", got.Repositories)
	}
}

// Each property offers what a scope could name, with how many repositories carry it now and which
// value they carry for choosing nothing. Nothing is offered for a property that may not scope.
func TestEachPropertyOffersTheValuesAScopeCouldName(t *testing.T) {
	srv, _ := propertyServer(t)
	rec := httptest.NewRecorder()
	req := controlRequest("/api/accounts/acme/properties")
	req.Body = http.NoBody
	srv.Handler().ServeHTTP(rec, req)
	got := decode[servedProperties](t, rec)

	offered := map[string]string{}
	for _, d := range got.Definitions {
		var parts []string
		for _, v := range d.Values {
			part := fmt.Sprintf("%s:%d", v.Value, v.Repositories)
			if v.IsDefault {
				part += "*"
			}
			parts = append(parts, part)
		}
		offered[d.Name] = strings.Join(parts, " ")
	}
	want := map[string]string{
		// A select offers its allowed values, chosen or not, and a value GitHub still reports that
		// is no longer allowed, because a repository carries it.
		"team": "a:2* b:1 retired:1 c:0",
		"flag": "true:1 false:0",
		// A multi-select's default is each of its listed values.
		"tags": "x:0* y:0",
		// A name matches whatever its case, as it does when ownership is decided.
		"region":      "emea:2",
		"business_id": "",
		"legacy":      "",
	}
	for name, w := range want {
		if offered[name] != w {
			t.Errorf("%s offers %q, want %q", name, offered[name], w)
		}
	}
}

// Counting a candidate uses the same matching ownership does, over what GitHub says now.
func TestACandidateScopeIsCountedAgainstWhatGitHubSaysNow(t *testing.T) {
	srv, _ := propertyServer(t)
	rec := httptest.NewRecorder()
	req := controlRequest("/api/accounts/acme/properties")
	req.Body = http.NoBody
	req = localRequest(http.MethodPost, "/api/accounts/acme/properties", strings.NewReader(
		`{"clauses":[[{"name":"team","value":"a"}],[{"name":"team","value":"a"},{"name":"business_id","value":"7"}]]}`))

	req.Host = "127.0.0.1:8080"
	req.Header.Set(controlHeader, "1")
	srv.Handler().ServeHTTP(rec, req)
	got := decode[servedProperties](t, rec)

	if len(got.ClauseCounts) != 2 || got.ClauseCounts[0] != 2 || got.ClauseCounts[1] != 1 {
		t.Errorf("counts %v, want [2 1]", got.ClauseCounts)
	}
}

// It spends the operator's token, so it is a control like the repository listing, and a refusal
// gh can explain arrives as its remedy.
func TestReadingAnAccountsPropertiesIsAControl(t *testing.T) {
	srv, asked := propertyServer(t)
	if rec := get(t, srv, "/api/accounts/acme/properties"); rec.Code == http.StatusOK {
		t.Errorf("GET answered %d", rec.Code)
	}
	if rec := post(t, srv, "/api/accounts/acme/properties"); rec.Code != http.StatusForbidden {
		t.Errorf("a POST without the control header answered %d", rec.Code)
	}
	if len(*asked) != 0 {
		t.Errorf("gh was asked %v by refused requests", *asked)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, controlRequest("/api/accounts/codeql/properties"))
	if got := decode[servedProperties](t, rec); got.Problem == nil || got.Problem.Remedy != "authorize it" {
		t.Errorf("problem %+v, want the remedy carried", got.Problem)
	}
	srv.ReadProperties = func(context.Context, string) ([]ghauth.PropertyDefinition, []ghauth.RepositoryProperties, error) {
		return nil, nil, errors.New("decoding")
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, controlRequest("/api/accounts/acme/properties"))
	if rec.Code < 500 {
		t.Errorf("an unexplained failure answered %d", rec.Code)
	}
}

// A held scope counts nothing, so it is served with its reason and no count that could read as
// what it covers.
func TestAHeldPropertyScopeIsServedWithItsReasonAndNoCount(t *testing.T) {
	srv, s := propertyScopedServer(t)
	if rec := put(t, srv, "/api/owned-scope", teamA); rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	if err := s.ReplacePropertySchema("acme", []store.PropertyDefinition{
		{Name: "team", ValueType: "single_select", ValuesEditableBy: "org_and_repo_actors"},
	}); err != nil {
		t.Fatal(err)
	}

	got := decode[[]servedScope](t, get(t, srv, "/api/owned-scope"))
	p := got[0].Properties[0]
	if !p.Held || p.HeldReason == "" || p.Matching != 0 {
		t.Errorf("held scope served as %+v, want held with a reason and nothing counted", p)
	}
}

// No listing records an archived repository, so no scope can own one; counting it before saving
// would promise a repository the save then leaves out.
func TestALiveCountLeavesOutArchivedRepositories(t *testing.T) {
	replies := map[string]string{
		"api orgs/has-ghas/properties/schema": `[{"property_name":"team","value_type":"string",
		  "values_editable_by":"org_actors"}]`,
		"api orgs/has-ghas/properties/values?per_page=100 --paginate": `[
		  {"repository_full_name":"has-ghas/live","properties":[{"property_name":"team","value":"a"}]},
		  {"repository_full_name":"has-ghas/Old","properties":[{"property_name":"team","value":"a"}]}]`,
		"api orgs/has-ghas/repos?type=all&per_page=100 --paginate": `[
		  {"name":"live","archived":false,"owner":{"login":"has-ghas"}},
		  {"name":"old","archived":true,"owner":{"login":"has-ghas"}}]`,
	}
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if out, ok := replies[strings.Join(args, " ")]; ok {
			return []byte(out), nil
		}
		return nil, errors.New("unexpected gh " + strings.Join(args, " "))
	}

	_, repositories, err := readPropertiesWith(run)(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 1 || repositories[0].Repository != "has-ghas/live" {
		t.Errorf("read %+v, want has-ghas/live alone", repositories)
	}
}

// A candidate scope the recorded schema cannot honor counts nothing, and the preview names it with
// the reason, so "every root leaves" is not read as what the scope does once saving reads the schema.
func TestThePreviewNamesACandidateScopeThatIsHeld(t *testing.T) {
	srv, s := newServer(t)
	if _, err := s.InsertOwnedRepository("acme/alpha"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePropertyValues("acme/alpha", []store.PropertyValue{{Name: "team", Value: "a"}}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localRequest(http.MethodPost, "/api/owned-scope/preview",
		strings.NewReader(teamA)))
	var preview struct {
		RootsNext int `json:"rootsNext"`
		Held      []struct {
			Owner  string               `json:"owner"`
			Terms  []store.PropertyTerm `json:"terms"`
			Reason string               `json:"reason"`
		} `json:"held"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	if preview.RootsNext != 0 || len(preview.Held) != 1 || preview.Held[0].Owner != "acme" ||
		preview.Held[0].Terms[0].Value != "a" || !strings.Contains(preview.Held[0].Reason, "not been read") {
		t.Errorf("preview %+v, want acme's team=a held as not read", preview)
	}
}
