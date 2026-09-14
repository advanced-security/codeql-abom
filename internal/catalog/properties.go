// Custom property values are the dividing line an organization already administers, so ABOM reads
// them rather than asking anyone to maintain a second copy. They arrive on the walk the listing was
// already making; REST has no such field and pays an organization-wide walk instead.
//
// See plans/db/business-units.md.

package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// PropertyValue is one custom property value GitHub holds for a repository.
type PropertyValue struct {
	Name  string
	Value string
}

// maxRepositoryProperties is the nested page asked for per repository.
//
// The ceiling GitHub allows, because it is free: a hundred repositories each carrying a hundred
// property values measured the same 1 point as asking for none. An organization with more than a
// hundred properties on one repository is not something to guess at, which is what `totalCount` is
// checked against.
const maxRepositoryProperties = 100

// propertyNode is the shape a nested connection answers with. `value` is a `CustomPropertyValue`
// scalar, which is a string for a single-select property and a list for a multi-select one.
type propertyNode struct {
	PropertyName string          `json:"propertyName"`
	Value        json.RawMessage `json:"value"`
}

// values flattens one property into one row per value, so that "matches if any value matches" is a
// plain comparison rather than a parse of a joined string.
func (p propertyNode) values() []PropertyValue {
	if p.PropertyName == "" {
		return nil
	}
	var many []string
	if err := json.Unmarshal(p.Value, &many); err == nil {
		out := make([]PropertyValue, 0, len(many))
		for _, v := range many {
			out = append(out, PropertyValue{Name: p.PropertyName, Value: v})
		}
		return out
	}
	var one string
	if err := json.Unmarshal(p.Value, &one); err != nil {
		return nil
	}
	return []PropertyValue{{Name: p.PropertyName, Value: one}}
}

// restPropertyEntry is one repository in the organization-wide properties walk.
type restPropertyEntry struct {
	RepositoryFullName string `json:"repository_full_name"`
	Properties         []struct {
		PropertyName string          `json:"property_name"`
		Value        json.RawMessage `json:"value"`
	} `json:"properties"`
}

// PropertyDefinition is one property as the organization declares it, rather than as any repository
// carries it.
type PropertyDefinition struct {
	Name string
	// HasDefault separates "no default declared" from "the default is the empty string". A
	// required property with a default has no unset state, so the biggest bucket can be the one
	// nobody chose, and only the schema can say which one that is.
	HasDefault   bool
	DefaultValue string
}

// PropertySchema reads what an account declares its custom properties to be.
//
// One call for the organization, not per repository, and it answers for both routes: GraphQL
// exposes the values on a repository but not the schema behind them.
func (c *Client) PropertySchema(ctx context.Context, account string) ([]PropertyDefinition, error) {
	if account == "" || strings.ContainsAny(account, "/?#") {
		return nil, fmt.Errorf("account %q is not a valid name", account)
	}
	resp, err := c.do(ctx, fmt.Sprintf("%s/orgs/%s/properties/schema",
		c.baseURL, url.PathEscape(account)), jsonAccept)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var page []struct {
		PropertyName string  `json:"property_name"`
		DefaultValue *string `json:"default_value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("decoding the property schema for %s: %w", account, err)
	}

	out := make([]PropertyDefinition, 0, len(page))
	for _, p := range page {
		d := PropertyDefinition{Name: p.PropertyName}
		if p.DefaultValue != nil {
			d.HasDefault, d.DefaultValue = true, *p.DefaultValue
		}
		out = append(out, d)
	}
	return out, nil
}

// attachRESTProperties reads every repository's values in one walk and joins them to the listing.
//
// One walk for the whole organization rather than a call per repository: `/properties/values`
// returns a hundred repositories at a time, so the shape is the same as the listing's. Joining on
// name is the weakness — a repository renamed between the two walks is attributed to neither — and
// it is why GraphQL, where the values arrive already joined, is the preferred route.
//
// Any failure leaves the values *unknown* rather than empty, and says why. An organization whose
// token cannot read properties still has repositories, so losing the listing over a permission that
// only affects grouping would be the wrong trade — but reporting the grouping as empty would be a
// claim about the organization that nobody established.
func (c *Client) attachRESTProperties(
	ctx context.Context, account string, repos []Repository,
) (read bool, why string) {
	if len(repos) == 0 {
		return false, "no repositories to read properties for"
	}
	next := fmt.Sprintf("%s/orgs/%s/properties/values?per_page=%d",
		c.baseURL, url.PathEscape(account), reposPerPage)

	byName := map[string][]PropertyValue{}
	for next != "" {
		resp, err := c.do(ctx, next, jsonAccept)
		if err != nil {
			return false, err.Error()
		}
		var page []restPropertyEntry
		decodeErr := json.NewDecoder(resp.Body).Decode(&page)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if decodeErr != nil {
			return false, fmt.Sprintf("decoding property values for %s: %v", account, decodeErr)
		}

		for _, entry := range page {
			key := strings.ToLower(entry.RepositoryFullName)
			for _, p := range entry.Properties {
				node := propertyNode{PropertyName: p.PropertyName, Value: p.Value}
				byName[key] = append(byName[key], node.values()...)
			}
		}
		next = nextLink(link)
	}

	for i := range repos {
		repos[i].Properties = byName[strings.ToLower(repos[i].FullName)]
	}
	return true, ""
}
