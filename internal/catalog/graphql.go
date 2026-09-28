// GraphQL is the preferred route for reading an organization, and REST is the fallback that has to
// keep working. This file is the transport only: one POST, the envelope GraphQL answers with, and
// the second budget it spends. What to ask for lives in sweep.go.
//
// Written over the standard library on purpose. `shurcooL/githubv4` is the usual answer and brings
// three modules with it, to a project whose whole subject is transitive dependency risk, in order
// to avoid a JSON POST. docs/architecture.md says what each route reads and where they differ.

package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// graphQLEndpoint derives the GraphQL address from the REST base URL.
//
// On GitHub Enterprise Server the two are siblings — /api/v3 and /api/graphql — rather than one
// beneath the other, so this replaces the suffix rather than appending to it.
func graphQLEndpoint(baseURL string) string {
	trimmed := strings.TrimSuffix(baseURL, "/")
	if rest, ok := strings.CutSuffix(trimmed, "/api/v3"); ok {
		return rest + "/api/graphql"
	}
	return trimmed + "/graphql"
}

// graphQLEnvelope is what every GraphQL answer arrives in. `data` is decoded twice: once here to
// read the cost GitHub charged, and once into the caller's own shape.
type graphQLEnvelope struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"errors"`
}

// graphQLCost is the price of the query, asked for alongside the data rather than predicted.
type graphQLCost struct {
	RateLimit struct {
		Cost      int `json:"cost"`
		Remaining int `json:"remaining"`
		Limit     int `json:"limit"`
	} `json:"rateLimit"`
}

// maxGraphQLBody bounds one answer. A page of a hundred repositories with their property values is
// tens of kilobytes; anything past this is a schema mistake rather than a large organization.
const maxGraphQLBody = 8 << 20

// graphQL posts one query and decodes `data` into out.
//
// A GraphQL failure arrives as HTTP 200 with an `errors` array, so the status code alone cannot
// distinguish a refusal from an empty organization. Both halves are checked, and errors win: a
// partial answer beside an error is still an answer somebody could not fully read.
func (c *Client) graphQL(ctx context.Context, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables,omitempty"`
	}{Query: query, Variables: vars})
	if err != nil {
		return fmt.Errorf("building GraphQL request: %w", err)
	}

	endpoint := graphQLEndpoint(c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("building GraphQL request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	if err := c.Pace.Wait(ctx); err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", redact(endpoint), err)
	}
	defer resp.Body.Close()
	c.requests.Add(1)
	c.observeBudget(resp)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s returned %d: %s",
			redact(endpoint), resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var envelope graphQLEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxGraphQLBody)).Decode(&envelope); err != nil {
		return fmt.Errorf("decoding the answer from %s: %w", redact(endpoint), err)
	}
	if len(envelope.Errors) > 0 {
		first := envelope.Errors[0]
		return fmt.Errorf("%s refused the query: %s%s",
			redact(endpoint), first.Message, moreErrors(len(envelope.Errors)))
	}

	var cost graphQLCost
	// A query that did not ask for rateLimit leaves this zero, which is "not asked", not "free".
	if err := json.Unmarshal(envelope.Data, &cost); err == nil && cost.RateLimit.Limit > 0 {
		c.points.Add(int64(cost.RateLimit.Cost))
		c.recordGraphQLBudget(cost.RateLimit.Remaining, cost.RateLimit.Limit)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decoding the answer from %s: %w", redact(endpoint), err)
	}
	return nil
}

// moreErrors says how many complaints were not shown, so a reader knows the first was not the only.
func moreErrors(n int) string {
	if n <= 1 {
		return ""
	}
	return fmt.Sprintf(" (and %d more)", n-1)
}

// recordGraphQLBudget keeps what the answer body said, without disturbing the reset time the
// headers carry.
func (c *Client) recordGraphQLBudget(remaining, limit int) {
	c.budgetMu.Lock()
	defer c.budgetMu.Unlock()
	c.graphqlBudget.Known = true
	c.graphqlBudget.Remaining = remaining
	c.graphqlBudget.Limit = limit
}
