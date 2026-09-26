package serve

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// policies serves every policy, drafts included. A draft is the interesting one: it is the rule
// somebody is still deciding about.
func (s *Server) policies(w http.ResponseWriter, _ *http.Request) {
	out, err := s.Store.Policies()
	writeJSON(w, out, err)
}

// savePolicy records a draft. It never accepts a state: a client that could write one would adopt a
// policy by spelling rather than by simulating it.
func (s *Server) savePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	var body struct {
		Name       string `json:"name"`
		Predicate  string `json:"predicate"`
		ScopeKind  string `json:"scopeKind"`
		ScopeValue string `json:"scopeValue"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "a policy is a name and a predicate")
		return
	}
	// Refused rather than dropped: saved without it, a policy for one unit would refuse the whole
	// organization. The scope is a term of the predicate now, applied by the one evaluation.
	if (body.ScopeKind != "" && body.ScopeKind != "organization") || body.ScopeValue != "" {
		writeError(w, http.StatusBadRequest,
			"a policy is bound by its predicate: write repositories=<owner/name> or "+
				"properties=<key>:<value> into it rather than a scope beside it")
		return
	}
	p := store.Policy{Name: body.Name, Predicate: body.Predicate}
	// Refused while the operator is still looking at it. A policy whose text carries more than it
	// enforces blocks pull requests for something wider than anybody read.
	spec, err := viewspec.Parse(p.Predicate)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refused := spec.UnenforceableAsPolicy(); len(refused) > 0 {
		writeError(w, http.StatusBadRequest, unenforceable(refused))
		return
	}
	if err := spec.SetConflict(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p.CreatedBy = store.DeclaredByInterface
	if err := s.Store.SavePolicy(p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.policies(w, r)
}

// simulatePolicy answers what a policy matches now and what it would refuse, changing nothing that
// is enforced. This is the question a security team has to be able to ask before turning a gate on.
func (s *Server) simulatePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	outcome, _, err := s.Store.SimulatePolicy(r.PathValue("name"), s.GroupProperty)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, outcome, nil)
}

// adoptPolicy turns a draft on, and is refused unless somebody has simulated it.
func (s *Server) adoptPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	var body struct {
		By string `json:"by"`
	}
	// A missing body is fine; an unreadable one is not worth guessing at.
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.Store.AdoptPolicy(r.PathValue("name"), body.By); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"adopted": r.PathValue("name")}, nil)
}

// grantWaiver records an exclusion or an exception against one named repository.
func (s *Server) grantWaiver(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	var body struct {
		Kind       string `json:"kind"`
		Repository string `json:"repository"`
		Reason     string `json:"reason"`
		GrantedBy  string `json:"grantedBy"`
		ExpiresAt  string `json:"expiresAt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "a waiver is a kind, a repository and a reason")
		return
	}

	waiver := store.Waiver{
		Policy: r.PathValue("name"), Kind: body.Kind, Repository: body.Repository,
		Reason: body.Reason, GrantedBy: body.GrantedBy,
	}
	if body.ExpiresAt != "" {
		at, err := time.Parse(time.RFC3339, body.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"an expiry has to be an RFC 3339 time, so everyone reads the same instant")
			return
		}
		waiver.ExpiresAt = at
	}
	if err := s.Store.GrantWaiver(waiver); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.Store.Waivers(waiver.Policy)
	writeJSON(w, out, err)
}

// policyWaivers is the audit answer: every exclusion and exception, across every policy, on demand.
//
// Not guarded, because it is a read, and a waiver nobody can list is a waiver nobody can withdraw.
func (s *Server) policyWaivers(w http.ResponseWriter, _ *http.Request) {
	out, err := s.Store.AllWaivers()
	writeJSON(w, out, err)
}
