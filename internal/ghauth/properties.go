package ghauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// PropertyDefinition is one custom property as an organization defines it, read with the
// operator's own token for the declaration picker.
type PropertyDefinition struct {
	Name         string `json:"name"`
	ValueType    string `json:"valueType"`
	Required     bool   `json:"required"`
	HasDefault   bool   `json:"hasDefault"`
	DefaultValue string `json:"defaultValue"`
	// DefaultValues is a multi-select's default, a list GitHub gives a repository that chose nothing.
	DefaultValues []string `json:"defaultValues"`
	AllowedValues []string `json:"allowedValues"`
	// ValuesEditableBy is GitHub's answer, empty when it gave none.
	ValuesEditableBy string `json:"valuesEditableBy"`
	Description      string `json:"description"`
}

// PropertyValue is one value a repository carries; a multi-select arrives as several.
type PropertyValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// RepositoryProperties is what one repository carries, as GitHub answered for the organization.
type RepositoryProperties struct {
	Repository string          `json:"repository"`
	Values     []PropertyValue `json:"values"`
}

// ReadPropertySchema reads what an organization defines its custom properties to be.
func ReadPropertySchema(ctx context.Context, run Runner, owner string) ([]PropertyDefinition, error) {
	if !IsAccountName(owner) {
		return nil, fmt.Errorf("%q is not a GitHub account name", owner)
	}
	out, err := run(ctx, "gh", "api", "orgs/"+owner+"/properties/schema")
	if err != nil {
		return nil, propertyProblem(owner, err)
	}
	var page []struct {
		PropertyName     string          `json:"property_name"`
		ValueType        string          `json:"value_type"`
		Required         bool            `json:"required"`
		DefaultValue     json.RawMessage `json:"default_value"`
		AllowedValues    []string        `json:"allowed_values"`
		ValuesEditableBy string          `json:"values_editable_by"`
		Description      string          `json:"description"`
	}
	if err := json.Unmarshal(out, &page); err != nil {
		return nil, fmt.Errorf("reading %s's property schema: %w", owner, err)
	}
	defs := make([]PropertyDefinition, 0, len(page))
	for _, p := range page {
		d := PropertyDefinition{Name: p.PropertyName, ValueType: p.ValueType, Required: p.Required,
			AllowedValues: p.AllowedValues, ValuesEditableBy: p.ValuesEditableBy, Description: p.Description}
		if d.AllowedValues == nil {
			d.AllowedValues = []string{}
		}
		d.DefaultValues = []string{}
		var many []string
		var one *string
		switch {
		case json.Unmarshal(p.DefaultValue, &many) == nil && many != nil:
			d.DefaultValues = many
		case json.Unmarshal(p.DefaultValue, &one) == nil && one != nil:
			d.HasDefault, d.DefaultValue = true, *one
		}
		defs = append(defs, d)
	}
	sort.Slice(defs, func(i, j int) bool { return strings.ToLower(defs[i].Name) < strings.ToLower(defs[j].Name) })
	return defs, nil
}

// ReadPropertyValues reads what every repository of an organization carries, one row per value.
// Like a listing, a read that fails on any page returns nothing rather than a prefix.
func ReadPropertyValues(ctx context.Context, run Runner, owner string) ([]RepositoryProperties, error) {
	if !IsAccountName(owner) {
		return nil, fmt.Errorf("%q is not a GitHub account name", owner)
	}
	out, err := run(ctx, "gh", "api", "orgs/"+owner+"/properties/values?per_page=100", "--paginate")
	if err != nil {
		return nil, propertyProblem(owner, err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	repositories := []RepositoryProperties{}
	for dec.More() {
		var page []struct {
			FullName   string `json:"repository_full_name"`
			Properties []struct {
				PropertyName string          `json:"property_name"`
				Value        json.RawMessage `json:"value"`
			} `json:"properties"`
		}
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("reading %s's property values: %w", owner, err)
		}
		for _, r := range page {
			one := RepositoryProperties{Repository: r.FullName, Values: []PropertyValue{}}
			for _, p := range r.Properties {
				one.Values = append(one.Values, propertyValues(p.PropertyName, p.Value)...)
			}
			repositories = append(repositories, one)
		}
	}
	return repositories, nil
}

// propertyValues flattens one answer: a list is a multi-select, a string one value, null none.
func propertyValues(name string, raw json.RawMessage) []PropertyValue {
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		out := make([]PropertyValue, 0, len(many))
		for _, v := range many {
			out = append(out, PropertyValue{Name: name, Value: v})
		}
		return out
	}
	var one *string
	if err := json.Unmarshal(raw, &one); err != nil || one == nil {
		return nil
	}
	return []PropertyValue{{Name: name, Value: *one}}
}

// propertyProblem says why an organization's custom properties could not be read. Single sign-on
// and authentication are the listing's problems too; the rest are said of properties.
func propertyProblem(owner string, err error) error {
	text := err.Error()
	switch {
	case errors.Is(err, ErrNotInstalled), strings.Contains(text, "SAML enforcement"),
		strings.Contains(text, "gh auth login"), strings.Contains(text, "GH_TOKEN"),
		strings.Contains(text, "HTTP 401"):
		return listingProblem(owner, err)
	case strings.Contains(text, "HTTP 404"):
		return &Problem{
			Reason: "gh cannot see an organization named " + owner + ", so its custom properties cannot be offered.",
			Remedy: "Check the name. A property scope can still be declared with codeql-abom own --property, " +
				"and the next listing judges it.",
		}
	default:
		return &Problem{
			Reason: "gh could not read " + owner + "'s custom properties: " + text,
			Remedy: "Check the authentication with: gh auth status",
		}
	}
}
