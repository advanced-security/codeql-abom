package catalog

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/work"
)

// RefQuestion asks where one reference of one repository points now; an empty Ref is the default branch.
type RefQuestion struct {
	Owner, Repo, Ref string
}

// RefAnswer is the commit a reference pointed at when GitHub was asked.
type RefAnswer struct {
	Commit string
	// Unread says why there is no commit. It is never evidence the reference stayed put.
	Unread string
}

// RefBatch is one query's answers, in the order asked, and what GitHub charged and reported.
type RefBatch struct {
	Answers []RefAnswer
	// Requests is one where GitHub answered at all, and zero where the request never arrived.
	Requests int
	// Cost is the query's rateLimit cost; Priced is false where the answer named none.
	Cost    int
	Priced  bool
	Reading work.Reading
}

// peelFragment follows annotated tags to their commit, as `ls-remote` does with `^{}`, so the answer compares with resolved_sha.
const peelFragment = `fragment peel on GitObject { __typename oid ... on Tag { target { __typename oid ` +
	`... on Tag { target { __typename oid } } } } }`

// refAlias is where one question's answer sits: its repository's alias, the field asked first, and the fallback.
type refAlias struct {
	repo, first, second string
}

// refQuery passes every name as a variable, because a reference is whatever a workflow wrote.
func refQuery(qs []RefQuestion) (string, map[string]any, []refAlias) {
	vars := map[string]any{}
	var decls []string
	var fields [][]string
	repos := map[[2]string]int{}
	withDefault := map[int]bool{}
	asked := make([]refAlias, len(qs))
	for i, q := range qs {
		r, ok := repos[[2]string{q.Owner, q.Repo}]
		if !ok {
			r = len(fields)
			repos[[2]string{q.Owner, q.Repo}] = r
			fields = append(fields, nil)
			vars["o"+strconv.Itoa(r)], vars["n"+strconv.Itoa(r)] = q.Owner, q.Repo
			decls = append(decls, fmt.Sprintf("$o%d: String!, $n%d: String!", r, r))
		}
		a := refAlias{repo: "r" + strconv.Itoa(r)}
		ask := func(alias, qualified string) string {
			vars[alias] = qualified
			decls = append(decls, "$"+alias+": String!")
			fields[r] = append(fields[r], alias+": ref(qualifiedName: $"+alias+") { target { ...peel } }")
			return alias
		}
		switch {
		case q.Ref == "":
			a.first = "d"
			if !withDefault[r] {
				withDefault[r] = true
				fields[r] = append(fields[r], "d: defaultBranchRef { target { ...peel } }")
			}
		case strings.HasPrefix(q.Ref, "refs/"):
			a.first = ask("h"+strconv.Itoa(i), q.Ref)
		default:
			// A name that is both is the branch, as `git clone --branch` takes it.
			a.first = ask("h"+strconv.Itoa(i), "refs/heads/"+q.Ref)
			a.second = ask("t"+strconv.Itoa(i), "refs/tags/"+q.Ref)
		}
		asked[i] = a
	}
	var b strings.Builder
	fmt.Fprintf(&b, "query(%s) {\nrateLimit { cost remaining limit resetAt }\n", strings.Join(decls, ", "))
	for r, f := range fields {
		fmt.Fprintf(&b, "r%d: repository(owner: $o%d, name: $n%d) { %s }\n", r, r, r, strings.Join(f, " "))
	}
	b.WriteString("}\n" + peelFragment)
	return b.String(), vars, asked
}

type gitObject struct {
	Type   string     `json:"__typename"`
	OID    string     `json:"oid"`
	Target *gitObject `json:"target"`
}

type refField struct {
	Target *gitObject `json:"target"`
}

type refError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

// ResolveRefs asks where a batch of references point in one GraphQL query; one unread answer leaves the rest standing.
func (c *Client) ResolveRefs(ctx context.Context, qs []RefQuestion) (RefBatch, error) {
	out := RefBatch{Answers: make([]RefAnswer, len(qs))}
	if len(qs) == 0 {
		return out, nil
	}
	query, vars, asked := refQuery(qs)
	data, errs, err := c.postRefs(ctx, query, vars, &out)
	if err != nil {
		return out, err
	}
	for i, q := range qs {
		out.Answers[i] = answerFor(q, asked[i], data, errs)
	}
	return out, nil
}

func answerFor(q RefQuestion, a refAlias, data map[string]json.RawMessage, errs []refError) RefAnswer {
	name := q.Owner + "/" + q.Repo
	var fields map[string]*refField
	if raw := data[a.repo]; len(raw) == 0 || string(raw) == "null" {
		return RefAnswer{Unread: cmp.Or(reasonAt(errs, a.repo, ""), "GitHub answered nothing for "+name)}
	} else if err := json.Unmarshal(raw, &fields); err != nil {
		return RefAnswer{Unread: fmt.Sprintf("decoding the answer for %s: %v", name, err)}
	}
	for _, alias := range []string{a.first, a.second} {
		if alias == "" {
			continue
		}
		if why := reasonAt(errs, a.repo, alias); why != "" {
			return RefAnswer{Unread: why}
		}
		f := fields[alias]
		if f == nil || f.Target == nil {
			continue
		}
		obj := f.Target
		for obj.Type == "Tag" && obj.Target != nil {
			obj = obj.Target
		}
		if obj.Type != "Commit" || obj.OID == "" {
			return RefAnswer{Unread: fmt.Sprintf("%s@%s points at a %s, not a commit", name, q.Ref, obj.Type)}
		}
		return RefAnswer{Commit: strings.ToLower(obj.OID)}
	}
	if q.Ref == "" {
		return RefAnswer{Unread: name + " has no default branch"}
	}
	return RefAnswer{Unread: fmt.Sprintf("%s has no branch or tag named %s", name, q.Ref)}
}

// reasonAt is what GitHub said went wrong at a repository alias, or at one field beneath it.
func reasonAt(errs []refError, repo, field string) string {
	for _, e := range errs {
		if len(e.Path) == 0 || e.Path[0] != repo {
			continue
		}
		if field == "" || (len(e.Path) > 1 && e.Path[1] == field) {
			return e.Message
		}
	}
	return ""
}

// postRefs fails the whole query only on an error naming no path; one naming a path is that alias's alone.
func (c *Client) postRefs(ctx context.Context, query string, vars map[string]any,
	batch *RefBatch) (map[string]json.RawMessage, []refError, error) {
	payload, err := json.Marshal(struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}{Query: query, Variables: vars})
	if err != nil {
		return nil, nil, fmt.Errorf("building GraphQL request: %w", err)
	}
	endpoint := graphQLEndpoint(c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, fmt.Errorf("building GraphQL request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// A batch spans owners, so with an App the designated installation reads it; private repositories answer unread.
	if _, err := c.authorize(ctx, req, "", false); err != nil {
		return nil, nil, err
	}
	if err := c.Pace.Wait(ctx); err != nil {
		return nil, nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("requesting %s: %w", redact(endpoint), err)
	}
	defer resp.Body.Close()
	c.requests.Add(1)
	batch.Requests = 1
	batch.Reading = graphQLHeaderReading(resp)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, nil, fmt.Errorf("%s returned %d: %s",
			redact(endpoint), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []refError                 `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxGraphQLBody)).Decode(&envelope); err != nil {
		return nil, nil, fmt.Errorf("decoding the answer from %s: %w", redact(endpoint), err)
	}
	var rl struct {
		Cost      int    `json:"cost"`
		Remaining int    `json:"remaining"`
		Limit     int    `json:"limit"`
		ResetAt   string `json:"resetAt"`
	}
	if raw := envelope.Data["rateLimit"]; len(raw) > 0 && json.Unmarshal(raw, &rl) == nil && rl.Limit > 0 {
		resetAt, _ := time.Parse(time.RFC3339, rl.ResetAt)
		batch.Cost, batch.Priced = rl.Cost, true
		batch.Reading = work.Reading{Known: true, Remaining: rl.Remaining, Limit: rl.Limit, ResetAt: resetAt}
		c.points.Add(int64(rl.Cost))
		c.recordGraphQLBudget(rl.Remaining, rl.Limit, resetAt)
	}
	var whole []string
	for _, e := range envelope.Errors {
		if len(e.Path) == 0 {
			whole = append(whole, e.Message)
		}
	}
	if len(whole) > 0 {
		return nil, nil, fmt.Errorf("%s refused the query: %s%s",
			redact(endpoint), whole[0], moreErrors(len(whole)))
	}
	if envelope.Data == nil {
		return nil, nil, fmt.Errorf("%s answered no data", redact(endpoint))
	}
	return envelope.Data, envelope.Errors, nil
}

// graphQLHeaderReading reads the allowance from headers, which is all a refused answer carries.
func graphQLHeaderReading(resp *http.Response) work.Reading {
	resource := resp.Header.Get("x-ratelimit-resource")
	remaining, err := strconv.Atoi(resp.Header.Get("x-ratelimit-remaining"))
	if err != nil || (resource != "" && !strings.EqualFold(resource, work.ResourceGraphQL)) {
		return work.Reading{}
	}
	r := work.Reading{Known: true, Remaining: remaining}
	r.Limit, _ = strconv.Atoi(resp.Header.Get("x-ratelimit-limit"))
	if v, err := strconv.ParseInt(resp.Header.Get("x-ratelimit-reset"), 10, 64); err == nil {
		r.ResetAt = time.Unix(v, 0)
	}
	return r
}
