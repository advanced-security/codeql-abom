package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoveryReportsRepositoriesItDidNotProbe(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/graphql":
			fmt.Fprint(writer, `{"data":{"rateLimit":{"cost":1,"remaining":4999,"limit":5000},
				"repositoryOwner":{"repositories":{"pageInfo":{"hasNextPage":false},"nodes":[
				{"nameWithOwner":"octo/with","defaultBranchRef":{"name":"main"},
				 "workflowTree":{"__typename":"Tree","entries":[{"name":"build.yaml","type":"blob"}]}},
				{"nameWithOwner":"octo/without","defaultBranchRef":{"name":"main"},"workflowTree":null}
				]}}}}`)
		case "/repos/octo/with/code-scanning/codeql/databases":
			fmt.Fprint(writer, `[]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	var stdout, stderr strings.Builder
	err := runDiscover(context.Background(), []string{
		"--db", filepath.Join(t.TempDir(), "abom.db"),
		"--org", "octo", "--api-url", server.URL,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"1 repositories probed", "0 with a actions database", "1 unprobed",
		"no workflow files on the default branch", "no new database measurement",
		"1 GraphQL points spent", "4999 of 5000 GraphQL points left", "no REST budget was reported",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, stdout.String())
		}
	}
}
