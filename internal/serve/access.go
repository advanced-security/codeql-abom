package serve

import (
	"context"
	"net/http"

	"github.com/advanced-security/codeql-abom/internal/access"
	"github.com/advanced-security/codeql-abom/internal/authz"
)

// Access is how people sign in: *access.Authenticator in production.
type Access interface {
	User(*http.Request) (access.User, bool)
	// Page says where signing in stands for a browser with no session, and offers to sign in.
	Page(http.ResponseWriter, *http.Request)
	Login(http.ResponseWriter, *http.Request)
	Callback(http.ResponseWriter, *http.Request)
	Logout(http.ResponseWriter, *http.Request)
}

type userKey struct{}

func withUser(ctx context.Context, u access.User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// actor is who did what a request asks. With sign-in on it is the person signed in, whatever name
// the body carries, which anyone signed in could set; without it, nobody signed in to say otherwise,
// so the name the interface sent stands.
func (s *Server) actor(r *http.Request, claimed string) string {
	if u, ok := userOf(r.Context()); ok {
		return u.Called()
	}
	return claimed
}

func userOf(ctx context.Context) (access.User, bool) {
	u, ok := ctx.Value(userKey{}).(access.User)
	return u, ok
}

type meView struct {
	// Mode is "local", the single operator at this machine, or "signed-in".
	Mode string       `json:"mode"`
	User *access.User `json:"user"`
	// May names the operations the person's role allows, which the interface offers, and Needs the
	// role each operation needs, so a control not offered can say which one it waits for.
	May   []string          `json:"may"`
	Needs map[string]string `json:"needs"`
}

// me says who is asking and what they may do, so a control the role does not allow is explained
// rather than offered and refused.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	out := meView{Mode: "local", May: []string{}, Needs: map[string]string{}}
	u, signed := userOf(r.Context())
	if signed {
		out.Mode, out.User = "signed-in", &u
	}
	for _, op := range authz.Operations() {
		out.Needs[op.Name] = op.Role
		if !signed || op.Permits(u.Role) {
			out.May = append(out.May, op.Name)
		}
	}
	writeJSON(w, out, nil)
}
