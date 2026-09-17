package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The values ride on the walk that was already being made. Measured on a real organization: adding
// `repositoryCustomPropertyValues` to a hundred-repository page leaves the price at 1 point, which
// is the whole reason this route is preferred over a second correlated walk.
func TestPropertiesArriveOnTheSameGraphQLWalk(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprint(w, `{"data":{"rateLimit":{"cost":1,"remaining":9999,"limit":10000},
		  "repositoryOwner":{"repositories":{
		    "pageInfo":{"hasNextPage":false,"endCursor":""},
		    "nodes":[{"nameWithOwner":"has-ghas/juice-shop","isArchived":false,
		              "createdAt":"2024-01-01T00:00:00Z","pushedAt":"2026-01-01T00:00:00Z",
		              "defaultBranchRef":{"name":"master"},
		              "repositoryCustomPropertyValues":{"totalCount":2,"nodes":[
		                {"propertyName":"business_id","value":"2"},
		                {"propertyName":"phi-scan","value":"scan-pending"}]}}]}}}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	listing, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("made %d calls, want the properties to cost no extra round trip", calls)
	}
	got := listing.Repositories[0].Properties
	if len(got) != 2 || got[0].Name != "business_id" || got[0].Value != "2" {
		t.Errorf("properties %+v, want business_id=2 and phi-scan=scan-pending", got)
	}
}

// A multi-select property answers with a list where a single-select answers with a string. Reading
// only one shape drops the values of every multi-select property, silently.
func TestAMultiSelectValueIsReadAsEveryValueItHolds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":{"rateLimit":{"cost":1,"remaining":9999,"limit":10000},
		  "repositoryOwner":{"repositories":{
		    "pageInfo":{"hasNextPage":false,"endCursor":""},
		    "nodes":[{"nameWithOwner":"has-ghas/one","isArchived":false,
		              "createdAt":"2024-01-01T00:00:00Z","pushedAt":"2026-01-01T00:00:00Z",
		              "repositoryCustomPropertyValues":{"totalCount":1,"nodes":[
		                {"propertyName":"tier","value":["gold","regulated"]}]}}]}}}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	listing, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	got := listing.Repositories[0].Properties
	if len(got) != 2 {
		t.Fatalf("read %+v, want both values of the multi-select property", got)
	}
	if got[0].Value != "gold" || got[1].Value != "regulated" {
		t.Errorf("values %+v, want gold and regulated", got)
	}
}

// A page that could not carry every property must say so. Storing the first N and reporting the
// rest as unset would put a repository in the wrong unit, which is the one thing the dividing line
// cannot be allowed to do.
func TestATruncatedPropertyListIsRefusedRatherThanHalfKept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":{"rateLimit":{"cost":1,"remaining":9999,"limit":10000},
		  "repositoryOwner":{"repositories":{
		    "pageInfo":{"hasNextPage":false,"endCursor":""},
		    "nodes":[{"nameWithOwner":"has-ghas/one","isArchived":false,
		              "createdAt":"2024-01-01T00:00:00Z","pushedAt":"2026-01-01T00:00:00Z",
		              "repositoryCustomPropertyValues":{"totalCount":500,"nodes":[
		                {"propertyName":"business_id","value":"2"}]}}]}}}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	listing, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	repo := listing.Repositories[0]
	if !repo.PropertiesTruncated {
		t.Error("500 values arrived as 1 and nothing said so")
	}
	if len(repo.Properties) != 0 {
		t.Errorf("kept %+v, want nothing: a partial dividing line is worse than an absent one",
			repo.Properties)
	}
}

// Neither route may be the only route. REST carries no properties on the repository listing, so it
// has to make the organization-wide walk — which returns every repository's values a hundred at a
// time rather than costing a request each.
func TestTheRestRouteReadsPropertiesInOneOrganizationWalk(t *testing.T) {
	var propertyCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/properties/values") {
			propertyCalls++
			fmt.Fprint(w, `[{"repository_full_name":"has-ghas/one",
			                 "properties":[{"property_name":"business_id","value":"3"}]},
			                {"repository_full_name":"has-ghas/two",
			                 "properties":[{"property_name":"tier","value":["gold","regulated"]}]}]`)
			return
		}
		fmt.Fprint(w, `[{"full_name":"has-ghas/one","archived":false,"default_branch":"main",
		                 "created_at":"2024-01-01T00:00:00Z","pushed_at":"2026-01-01T00:00:00Z"},
		                {"full_name":"has-ghas/two","archived":false,"default_branch":"main",
		                 "created_at":"2024-01-01T00:00:00Z","pushed_at":"2026-01-01T00:00:00Z"}]`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	c.Route = RouteREST
	listing, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if propertyCalls != 1 {
		t.Errorf("made %d property calls for 2 repositories, want one walk for the organization",
			propertyCalls)
	}
	byName := map[string][]PropertyValue{}
	for _, r := range listing.Repositories {
		byName[r.FullName] = r.Properties
	}
	if len(byName["has-ghas/one"]) != 1 || byName["has-ghas/one"][0].Value != "3" {
		t.Errorf("one has %+v, want business_id=3", byName["has-ghas/one"])
	}
	if len(byName["has-ghas/two"]) != 2 {
		t.Errorf("two has %+v, want both values of the multi-select property", byName["has-ghas/two"])
	}
}

// An organization whose token cannot read properties still has repositories. Failing the listing
// because the values were refused would make the whole account unreadable over a permission that
// only affects how it is grouped.
func TestPropertiesThatCannotBeReadDoNotLoseTheListing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/properties/values") {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"Resource not accessible by personal access token"}`)
			return
		}
		fmt.Fprint(w, `[{"full_name":"has-ghas/one","archived":false,
		                 "created_at":"2024-01-01T00:00:00Z","pushed_at":"2026-01-01T00:00:00Z"}]`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	c.Route = RouteREST
	listing, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatalf("the listing was lost over a property permission: %v", err)
	}
	if len(listing.Repositories) != 1 {
		t.Errorf("listed %d repositories, want the account still read", len(listing.Repositories))
	}
	if listing.PropertiesRead {
		t.Error("properties were refused, so the corpus must not be told they are known and empty")
	}
}
