package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeRepo is one repository as the fake GraphQL server knows it: its default branch's commit and
// each fully qualified ref's object, as GraphQL would render it.
type fakeRepo struct {
	head string
	refs map[string]string
}

func commitObject(oid string) string { return fmt.Sprintf(`{"__typename":"Commit","oid":%q}`, oid) }

func tagObject(oid, target string) string {
	return fmt.Sprintf(`{"__typename":"Tag","oid":%q,"target":%s}`, oid, target)
}

var (
	repoLine  = regexp.MustCompile(`(r\d+): repository\(owner: \$(o\d+), name: \$(n\d+)\)`)
	fieldLine = regexp.MustCompile(`(\w+): (?:defaultBranchRef|ref\(qualifiedName: \$(\w+)\))`)
)

// refServer answers ref queries from world, one repository per line of the query, and counts them.
func refServer(t *testing.T, world map[string]fakeRepo, cost int) (*Client, *atomic.Int32) {
	t.Helper()
	var queries atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("decoding the query: %v", err)
		}
		data := []string{fmt.Sprintf(`"rateLimit":{"cost":%d,"remaining":4990,"limit":5000,"resetAt":"2026-10-04T13:00:00Z"}`, cost)}
		var errs []string
		for line := range strings.SplitSeq(req.Query, "\n") {
			m := repoLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			name := req.Variables[m[2]] + "/" + req.Variables[m[3]]
			repo, ok := world[name]
			if !ok {
				data = append(data, fmt.Sprintf(`%q:null`, m[1]))
				errs = append(errs, fmt.Sprintf(`{"type":"NOT_FOUND","path":[%q],"message":"Could not resolve to a Repository with the name '%s'."}`, m[1], name))
				continue
			}
			var fields []string
			for _, f := range fieldLine.FindAllStringSubmatch(line[len(m[0]):], -1) {
				obj := repo.refs[req.Variables[f[2]]]
				if f[2] == "" {
					obj = commitObject(repo.head)
				}
				if obj == "" {
					fields = append(fields, fmt.Sprintf(`%q:null`, f[1]))
					continue
				}
				fields = append(fields, fmt.Sprintf(`%q:{"target":%s}`, f[1], obj))
			}
			data = append(data, fmt.Sprintf(`%q:{%s}`, m[1], strings.Join(fields, ",")))
		}
		body := `{"data":{` + strings.Join(data, ",") + `}`
		if len(errs) > 0 {
			body += `,"errors":[` + strings.Join(errs, ",") + `]`
		}
		fmt.Fprint(w, body+"}")
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "t", srv.Client()), &queries
}

const (
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	commitC = "cccccccccccccccccccccccccccccccccccccccc"
	tagOID  = "7777777777777777777777777777777777777777"
)

// Polling an enterprise's dependencies one request each would cost a request per reference per
// pass; aliases answer a batch for what GitHub charges one query.
func TestOneQueryResolvesABatchAndPeelsAnnotatedTags(t *testing.T) {
	c, queries := refServer(t, map[string]fakeRepo{
		"actions/checkout": {head: commitA, refs: map[string]string{
			"refs/tags/v4": tagObject(tagOID, commitObject(commitB)),
		}},
		"vendor/tool": {head: commitC, refs: map[string]string{
			"refs/tags/v1":    commitObject(commitA),
			"refs/heads/main": commitObject(commitB),
			// A name that is a branch and a tag is the branch, as `git clone --branch` takes it.
			"refs/heads/both": commitObject(commitC),
			"refs/tags/both":  commitObject(commitA),
		}},
		"vendor/nested": {head: commitA, refs: map[string]string{
			"refs/tags/v2": tagObject(tagOID, tagObject(tagOID, commitObject(commitC))),
		}},
	}, 1)

	batch, err := c.ResolveRefs(context.Background(), []RefQuestion{
		{Owner: "actions", Repo: "checkout", Ref: "v4"},
		{Owner: "actions", Repo: "checkout", Ref: ""},
		{Owner: "vendor", Repo: "tool", Ref: "v1"},
		{Owner: "vendor", Repo: "tool", Ref: "main"},
		{Owner: "vendor", Repo: "tool", Ref: "both"},
		{Owner: "vendor", Repo: "tool", Ref: "refs/heads/main"},
		{Owner: "vendor", Repo: "nested", Ref: "v2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := queries.Load(); got != 1 {
		t.Errorf("seven references are one query; made %d", got)
	}
	want := []string{commitB, commitA, commitA, commitB, commitC, commitB, commitC}
	if len(batch.Answers) != len(want) {
		t.Fatalf("one answer per question; got %+v", batch.Answers)
	}
	for i, a := range batch.Answers {
		if a.Commit != want[i] || a.Unread != "" {
			t.Errorf("answer %d: want commit %s, got %+v", i, want[i], a)
		}
	}
	if !batch.Priced || batch.Cost != 1 {
		t.Errorf("the query's price is what GitHub reported; got %+v", batch)
	}
	if !batch.Reading.Known || batch.Reading.Remaining != 4990 || batch.Reading.ResetAt.IsZero() {
		t.Errorf("what GitHub reported left travels with the answer; got %+v", batch.Reading)
	}
}

// A missing reference or repository is something nobody could read, not a reference that stayed
// put, and one repository missing must not cost the rest of the batch its answers.
func TestAnUnreadableReferenceIsUnreadAndLeavesTheRestOfTheBatchAnswered(t *testing.T) {
	c, _ := refServer(t, map[string]fakeRepo{
		"vendor/tool": {head: commitC, refs: map[string]string{"refs/tags/v1": commitObject(commitA)}},
	}, 1)

	batch, err := c.ResolveRefs(context.Background(), []RefQuestion{
		{Owner: "vendor", Repo: "deleted", Ref: "v1"},
		{Owner: "vendor", Repo: "tool", Ref: "v9"},
		{Owner: "vendor", Repo: "tool", Ref: "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a := batch.Answers[0]; a.Commit != "" || !strings.Contains(a.Unread, "Could not resolve") {
		t.Errorf("a deleted repository is unread, with GitHub's reason; got %+v", a)
	}
	if a := batch.Answers[1]; a.Commit != "" || a.Unread == "" {
		t.Errorf("a reference GitHub does not know is unread; got %+v", a)
	}
	if a := batch.Answers[2]; a.Commit != commitA {
		t.Errorf("the rest of the batch is still answered; got %+v", a)
	}
}

// A query GitHub refused as a whole is an error, and none of its references reads as unchanged.
func TestARefusedQueryIsAnErrorWithItsPriceStillReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":{"rateLimit":{"cost":1,"remaining":10,"limit":5000,"resetAt":"2026-10-04T13:00:00Z"}},
		                "errors":[{"type":"MAX_NODE_LIMIT_EXCEEDED","message":"too many"}]}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "t", srv.Client())

	batch, err := c.ResolveRefs(context.Background(), []RefQuestion{{Owner: "a", Repo: "b", Ref: "v1"}})
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("a refusal names GitHub's reason; got %v", err)
	}
	if !batch.Priced || batch.Cost != 1 || batch.Reading.Remaining != 10 {
		t.Errorf("a refused query that was priced is still charged; got %+v", batch)
	}
}
