package serve

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// PropertyChoice is one property the picker lists, whether it may scope ownership, and the values
// a scope could name.
type PropertyChoice struct {
	ghauth.PropertyDefinition
	// Declarable is the server's answer, never re-derived in the browser.
	Declarable bool   `json:"declarable"`
	Reason     string `json:"reason,omitempty"`
	// Values are a select's allowed values, true and false, or what repositories carry, with any
	// value a repository carries that is no longer allowed. Empty when it may not scope.
	Values []PropertyValueCount `json:"values"`
}

// PropertyValueCount is one value, how many repositories carry it now, and whether a repository
// that chose nothing carries it.
type PropertyValueCount struct {
	Value        string `json:"value"`
	Repositories int    `json:"repositories"`
	IsDefault    bool   `json:"isDefault"`
}

// AccountProperties is what GitHub says an organization defines and its repositories carry.
type AccountProperties struct {
	Owner        string           `json:"owner"`
	Definitions  []PropertyChoice `json:"definitions"`
	Repositories int              `json:"repositories"`
	// ClauseCounts answers each posted scope, in order, with the matching ownership uses.
	ClauseCounts []int           `json:"clauseCounts"`
	Problem      *ghauth.Problem `json:"problem"`
}

// accountProperties reads an organization's properties for the property scope picker.
//
// A control for the same reasons as the repository listing: it spends the operator's token, and
// what it names is what that token may see. Nothing is written.
func (s *Server) accountProperties(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	owner := r.PathValue("owner")
	if !ghauth.IsAccountName(owner) {
		writeError(w, http.StatusBadRequest, owner+" is not a GitHub account name")
		return
	}
	var body struct {
		Clauses [][]store.PropertyTerm `json:"clauses"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "the body must be {\"clauses\": [[{\"name\", \"value\"}]]}")
		return
	}
	read := s.ReadProperties
	if read == nil {
		read = readPropertiesWithGh
	}

	out := AccountProperties{Owner: owner, Definitions: []PropertyChoice{},
		ClauseCounts: make([]int, len(body.Clauses))}
	defs, repositories, err := read(r.Context(), owner)
	var problem *ghauth.Problem
	switch {
	case errors.As(err, &problem):
		out.Problem = problem
		writeJSON(w, out, nil)
		return
	case err != nil:
		writeJSON(w, nil, err)
		return
	}
	out.Repositories = len(repositories)
	counts := map[string]map[string]int{}
	for _, repo := range repositories {
		values := make([]store.PropertyValue, 0, len(repo.Values))
		for _, v := range repo.Values {
			values = append(values, store.PropertyValue{Name: v.Name, Value: v.Value})
			key := strings.ToLower(v.Name)
			if counts[key] == nil {
				counts[key] = map[string]int{}
			}
			counts[key][v.Value]++
		}
		for i, terms := range body.Clauses {
			if (store.PropertyClause{Terms: terms}).CarriedBy(values) {
				out.ClauseCounts[i]++
			}
		}
	}
	for _, d := range defs {
		may, why := store.PropertyMayScope(d.Name, d.ValuesEditableBy)
		choice := PropertyChoice{PropertyDefinition: d, Declarable: may, Reason: why, Values: []PropertyValueCount{}}
		if may {
			choice.Values = offeredValues(d, counts[strings.ToLower(d.Name)])
		}
		out.Definitions = append(out.Definitions, choice)
	}
	writeJSON(w, out, nil)
}

// offeredValues lists what a scope on one property could name, most carried first.
func offeredValues(d ghauth.PropertyDefinition, carried map[string]int) []PropertyValueCount {
	named := map[string]bool{}
	for v := range carried {
		named[v] = true
	}
	switch d.ValueType {
	case "single_select", "multi_select":
		for _, v := range d.AllowedValues {
			named[v] = true
		}
	case "true_false":
		named["true"], named["false"] = true, true
	}
	schema := store.PropertyDefinition{HasDefault: d.HasDefault, DefaultValue: d.DefaultValue,
		DefaultValues: d.DefaultValues}
	out := make([]PropertyValueCount, 0, len(named))
	for v := range named {
		out = append(out, PropertyValueCount{Value: v, Repositories: carried[v], IsDefault: schema.IsDefault(v)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repositories != out[j].Repositories {
			return out[i].Repositories > out[j].Repositories
		}
		return out[i].Value < out[j].Value
	})
	return out
}

var readPropertiesWithGh = readPropertiesWith(ghauth.Exec)

// readPropertiesWith reads an organization's schema and what its repositories carry, leaving out
// the archived ones: no listing records them, so no scope can own them.
func readPropertiesWith(run ghauth.Runner) func(context.Context, string) ([]ghauth.PropertyDefinition, []ghauth.RepositoryProperties, error) {
	return func(ctx context.Context, owner string) ([]ghauth.PropertyDefinition, []ghauth.RepositoryProperties, error) {
		defs, err := ghauth.ReadPropertySchema(ctx, run, owner)
		if err != nil {
			return nil, nil, err
		}
		values, err := ghauth.ReadPropertyValues(ctx, run, owner)
		if err != nil {
			return nil, nil, err
		}
		listed, err := ghauth.ListRepositories(ctx, run, owner, false)
		if err != nil {
			return nil, nil, err
		}
		archived := map[string]bool{}
		for _, r := range listed {
			if r.Archived {
				archived[strings.ToLower(owner+"/"+r.Name)] = true
			}
		}
		kept := make([]ghauth.RepositoryProperties, 0, len(values))
		for _, v := range values {
			if !archived[strings.ToLower(v.Repository)] {
				kept = append(kept, v)
			}
		}
		return defs, kept, nil
	}
}
