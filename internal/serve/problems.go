package serve

import (
	"encoding/json"
	"net/http"
	"regexp"
)

// problemCode names a kind of refusal a client can switch on. The list below is closed: a code
// determines its status, so one condition always answers with one status.
type problemCode string

// Codes for refusals that need telling apart from others with the same status.
const (
	problemUnknownParameter     problemCode = "unknown-parameter"
	problemSignInRequired       problemCode = "sign-in-required"
	problemNoRole               problemCode = "no-role"
	problemRoleTooLow           problemCode = "role-too-low"
	problemHostNotAllowed       problemCode = "host-not-allowed"
	problemReadOnlyInstance     problemCode = "read-only-instance"
	problemCrossSite            problemCode = "cross-site"
	problemControlHeaderMissing problemCode = "control-header-missing"
	problemUnregistered         problemCode = "unregistered-operation"
	problemDecisionUnrecorded   problemCode = "decision-unrecorded"
	problemNoDecisionKey        problemCode = "decision-key-missing"
	problemNoQueryDirectory     problemCode = "query-directory-missing"
	problemQueryNotFound        problemCode = "query-not-found"
	problemComponentChanged     problemCode = "component-changed"
	problemBundleRequired       problemCode = "bundle-required"
	problemBundleChanged        problemCode = "bundle-changed"
	problemNoPublisher          problemCode = "publisher-missing"
	problemPublisherMisinstall  problemCode = "publisher-misinstalled"
	problemUpstream             problemCode = "upstream-failed"
	problemScopeUndecided       problemCode = "scope-undecided"
	problemDeclarationRefused   problemCode = "declaration-refused"
	problemRoleCannotDeclare    problemCode = "declaration-refused-by-role"
	problemAccountNotListable   problemCode = "account-not-listable"
	problemReviewStale          problemCode = "review-stale"
)

// Codes for everything else, by status alone.
const (
	problemBadRequest      problemCode = "bad-request"
	problemUnauthorized    problemCode = "unauthorized"
	problemForbidden       problemCode = "forbidden"
	problemNotFound        problemCode = "not-found"
	problemConflict        problemCode = "conflict"
	problemTooLarge        problemCode = "too-large"
	problemMediaType       problemCode = "unsupported-media-type"
	problemTooManyRequests problemCode = "too-many-requests"
	problemInternal        problemCode = "internal"
	problemBadGateway      problemCode = "bad-gateway"
	problemUnavailable     problemCode = "unavailable"
)

type problemSpec struct {
	status int
	title  string
	// generic is a code answered by status alone, through writeError.
	generic bool
}

var problems = map[problemCode]problemSpec{
	problemUnknownParameter:     {status: http.StatusBadRequest, title: "Not a parameter this answers"},
	problemSignInRequired:       {status: http.StatusUnauthorized, title: "Sign in to use ABOM"},
	problemNoRole:               {status: http.StatusForbidden, title: "No ABOM role"},
	problemRoleTooLow:           {status: http.StatusForbidden, title: "Your role does not allow this"},
	problemHostNotAllowed:       {status: http.StatusForbidden, title: "Not reachable at this address"},
	problemReadOnlyInstance:     {status: http.StatusForbidden, title: "This instance only reads"},
	problemCrossSite:            {status: http.StatusForbidden, title: "Refused from another site"},
	problemControlHeaderMissing: {status: http.StatusForbidden, title: "Not sent as a control"},
	problemUnregistered:         {status: http.StatusForbidden, title: "Nothing can decide this"},
	problemDecisionUnrecorded:   {status: http.StatusServiceUnavailable, title: "The decision could not be recorded"},
	problemNoDecisionKey:        {status: http.StatusServiceUnavailable, title: "No decision key"},
	problemNoQueryDirectory:     {status: http.StatusServiceUnavailable, title: "No query directory"},
	problemQueryNotFound:        {status: http.StatusNotFound, title: "No such query"},
	problemComponentChanged:     {status: http.StatusConflict, title: "A file changed since it was read"},
	problemBundleRequired:       {status: http.StatusBadRequest, title: "Name the bundle you reviewed"},
	problemBundleChanged:        {status: http.StatusConflict, title: "Not the bundle you reviewed"},
	problemNoPublisher:          {status: http.StatusServiceUnavailable, title: "No publisher App"},
	problemPublisherMisinstall:  {status: http.StatusConflict, title: "The publisher App is installed wrongly"},
	problemUpstream:             {status: http.StatusBadGateway, title: "GitHub refused the request"},
	problemScopeUndecided:       {status: http.StatusConflict, title: "The declared scope is not confirmed"},
	problemDeclarationRefused:   {status: http.StatusBadRequest, title: "Not a declaration ABOM accepts"},
	problemRoleCannotDeclare:    {status: http.StatusForbidden, title: "Your role cannot declare this"},
	problemAccountNotListable:   {status: http.StatusBadRequest, title: "Not an account discovery lists"},
	problemReviewStale:          {status: http.StatusConflict, title: "What you reviewed no longer holds"},

	problemBadRequest:      {status: http.StatusBadRequest, title: "Bad request", generic: true},
	problemUnauthorized:    {status: http.StatusUnauthorized, title: "Not signed in", generic: true},
	problemForbidden:       {status: http.StatusForbidden, title: "Refused", generic: true},
	problemNotFound:        {status: http.StatusNotFound, title: "Not found", generic: true},
	problemConflict:        {status: http.StatusConflict, title: "Conflict", generic: true},
	problemTooLarge:        {status: http.StatusRequestEntityTooLarge, title: "Too large", generic: true},
	problemMediaType:       {status: http.StatusUnsupportedMediaType, title: "Unsupported media type", generic: true},
	problemTooManyRequests: {status: http.StatusTooManyRequests, title: "Too many requests", generic: true},
	problemInternal:        {status: http.StatusInternalServerError, title: "ABOM could not answer", generic: true},
	problemBadGateway:      {status: http.StatusBadGateway, title: "Upstream failed", generic: true},
	problemUnavailable:     {status: http.StatusServiceUnavailable, title: "Unavailable", generic: true},
}

// problem is an RFC 9457 problem detail. Upstream is a provider's own words where it refused, and
// Login where to sign in.
type problem struct {
	Type     string      `json:"type"`
	Title    string      `json:"title"`
	Status   int         `json:"status"`
	Detail   string      `json:"detail"`
	Code     problemCode `json:"code"`
	Upstream string      `json:"upstream,omitempty"`
	Login    string      `json:"login,omitempty"`
}

// problemExtra adds what one kind of problem carries beside its detail.
type problemExtra func(*problem)

func withUpstream(text string) problemExtra { return func(p *problem) { p.Upstream = text } }
func withLogin(path string) problemExtra    { return func(p *problem) { p.Login = path } }

// writeProblem answers a refusal of the kind code names, which a constant above names.
func writeProblem(w http.ResponseWriter, code problemCode, detail string, extra ...problemExtra) {
	send(w, newProblem(code, detail, extra...))
}

// newProblem is a refusal of the kind code names, for an answer that carries more beside it.
func newProblem(code problemCode, detail string, extra ...problemExtra) problem {
	p := problem{Type: "urn:abom:problem:" + string(code), Title: problems[code].title, Status: problems[code].status,
		Detail: detail, Code: code}
	for _, e := range extra {
		e(&p)
	}
	return p
}

// send writes body, a problem or one carrying extension members beside it, with status.
func send(w http.ResponseWriter, p problem, body ...any) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	var payload any = p
	if len(body) > 0 {
		payload = body[0]
	}
	// The status is already sent; there is no second error to report.
	_ = json.NewEncoder(w).Encode(payload)
}

// writeError answers a refusal no client needs to tell apart from others of its status.
func writeError(w http.ResponseWriter, status int, message string) {
	for code, spec := range problems {
		if spec.generic && spec.status == status {
			send(w, newProblem(code, message))
			return
		}
	}
	send(w, newProblem(problemInternal, message))
}

// goPackage is the prefix a Go error carries from the package that raised it, which says nothing to
// a reader and names the code's layout to anyone.
var goPackage = regexp.MustCompile(`^(?:store|authz|ghapp|querypack|publish|catalog|expand|execution|serve|runner|access|enumerate|sourcepolicy|supervisor): `)

// internalDetail is a server failure in its own words.
func internalDetail(err error) string { return goPackage.ReplaceAllString(err.Error(), "") }
