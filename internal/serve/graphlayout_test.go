package serve

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/layout"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Layout gaps must be counted by node name because Graphviz may return extra container positions.
func TestUnplacedCountsNodesWithNoPositionRatherThanTheDifferenceInCounts(t *testing.T) {
	g := store.Graph{Nodes: []store.GraphNode{
		{Key: "a"}, {Key: "b"}, {Key: "c"},
	}}

	cases := []struct {
		name   string
		result layout.Result
		want   int
	}{
		{
			name: "every node placed",
			result: layout.Result{Positions: []layout.Position{
				{Key: "a"}, {Key: "b"}, {Key: "c"},
			}},
			want: 0,
		},
		{
			// The case the count subtraction cannot see: one node missing, but the engine also
			// returned containers the payload does not hold, so the totals balance.
			name: "one node missing behind padding the payload does not contain",
			result: layout.Result{Positions: []layout.Position{
				{Key: "a"}, {Key: "b"}, {Key: "repo:octo/one"}, {Key: "repo:octo/two"},
			}},
			want: 1,
		},
		{
			name: "more positions than nodes, none of them missing",
			result: layout.Result{Positions: []layout.Position{
				{Key: "a"}, {Key: "b"}, {Key: "c"}, {Key: "repo:octo/one"},
			}},
			want: 0,
		},
		{
			name:   "nothing placed at all",
			result: layout.Result{},
			want:   3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unplaced(g, tc.result); got != tc.want {
				t.Errorf("unplaced = %d, want %d", got, tc.want)
			}
		})
	}
}
