package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTheGraphQLListingSaysWhereActionsCodeLives(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		query = string(raw)
		fmt.Fprint(w, `{"data":{"rateLimit":{"cost":1,"remaining":9999,"limit":10000},
		  "repositoryOwner":{"repositories":{
		    "pageInfo":{"hasNextPage":false,"endCursor":""},
		    "nodes":[
		      {"nameWithOwner":"has-ghas/composite","isArchived":false,"isFork":true,"visibility":"INTERNAL",
		       "createdAt":"2024-10-03T21:50:22Z","pushedAt":"2024-10-03T21:50:22Z","defaultBranchRef":{"name":"main"},
		       "workflowTree":null,"actionsDir":{"__typename":"Tree"},"actionYml":null,"actionYaml":{"__typename":"Blob"}},
		      {"nameWithOwner":"has-ghas/plain","isArchived":false,"isFork":false,"visibility":"PUBLIC",
		       "createdAt":"2024-10-02T21:50:22Z","pushedAt":"2024-10-02T21:50:22Z","defaultBranchRef":{"name":"main"},
		       "workflowTree":null,"actionsDir":null,"actionYml":null,"actionYaml":null}]}}}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	listing, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	for _, expression := range []string{"HEAD:.github/actions", "HEAD:action.yml", "HEAD:action.yaml", "isFork", "visibility"} {
		if !strings.Contains(query, expression) {
			t.Errorf("the listing never asks for %s", expression)
		}
	}
	if len(listing.Repositories) != 2 {
		t.Fatalf("listed %d repositories, want 2", len(listing.Repositories))
	}
	composite, plain := listing.Repositories[0], listing.Repositories[1]
	if composite.Fork == nil || !*composite.Fork || composite.Visibility != "INTERNAL" ||
		composite.HasActionsDir == nil || !*composite.HasActionsDir ||
		composite.HasRootAction == nil || !*composite.HasRootAction ||
		composite.HasWorkflows == nil || *composite.HasWorkflows {
		t.Errorf("composite read as %+v", composite)
	}
	if plain.HasActionsDir == nil || *plain.HasActionsDir || plain.HasRootAction == nil || *plain.HasRootAction {
		t.Errorf("an absent path is a measured no, got %+v", plain)
	}
}

// REST cannot look inside a tree, so it says nothing about one rather than saying no.
func TestTheRESTListingSaysForkAndVisibilityAndLeavesTreesUnknown(t *testing.T) {
	f := threePages()
	c, done := client(t, f)
	defer done()
	c.Route = RouteREST
	listing, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range listing.Repositories {
		if r.Fork == nil || *r.Fork || r.Visibility != "public" {
			t.Errorf("%s: fork %v visibility %q, want false and public", r.FullName, r.Fork, r.Visibility)
		}
		if r.HasActionsDir != nil || r.HasRootAction != nil || r.HasWorkflows != nil {
			t.Errorf("%s: REST claimed to know a tree: %+v", r.FullName, r)
		}
	}
}
