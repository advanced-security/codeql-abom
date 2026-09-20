package serve

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// everyTerm writes one query parameter per term the language has, read out of a spec that carries
// all of them so this cannot drift from the serializer the way a hand-written list would.
func everyTerm(t *testing.T) url.Values {
	t.Helper()
	full := viewspec.Spec{
		Scope: "owned", Search: "x", Kinds: []string{"action"}, Origins: []string{"external"},
		Rules: []string{"actions/unpinned-tag"}, NotRules: []string{"actions/unpinned-tag"},
		Repositories:  []string{"octo-org/app"},
		Properties:    []viewspec.PropertyMatch{{Name: "k", Value: "v"}},
		NotProperties: []viewspec.PropertyMatch{{Name: "k", Value: "v"}},
		Provider:      "codeql", Severity: "high", EdgeClass: []string{"same-repo"},
		EdgeKinds: []string{viewspec.EdgeKindJobNeedsJob},
		RefStyle:  "tag", Boundary: "cross-owner", MinReach: 1,
		Flags: []string{"findings"}, Steps: true,
	}
	held, err := url.ParseQuery(full.String())
	if err != nil {
		t.Fatalf("parsing the canonical text: %v", err)
	}
	return held
}

// A term a unit declares and its route refuses is the gap this language is most likely to open:
// the two lists live in different packages, and nothing compared them. `refStyle` sat that way —
// the unit applied it, the store filtered on it, and the route turned it away at the door.
func TestTheRelationshipsRouteAcceptsEveryTermTheUnitApplies(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	for name, values := range everyTerm(t) {
		one := url.Values{name: values}
		spec, err := viewspec.Parse(one.Encode())
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		// The unit says it cannot answer this one, so being refused is the correct answer.
		if len(spec.UnenforceableAsRelationships()) > 0 {
			continue
		}
		rec := get(t, srv, "/api/relationships?"+one.Encode())
		if rec.Code != http.StatusOK {
			t.Errorf("the unit applies %s but the route answered %d: %s",
				name, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}
