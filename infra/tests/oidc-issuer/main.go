// Command oidc-issuer serves an OpenID provider in miniature for trying sign-in on a local cluster:
// the people given on the command line, each with the app roles named, and a key generated at start
// and gone when it stops. It calls nothing, and nothing in the production image builds it.
//
//	go run ./infra/tests/oidc-issuer --issuer http://127.0.0.1:9400 \
//	    --person vera=ABOM.Viewer --person otto=ABOM.Operator
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/access/oidctest"
)

type people map[string]oidctest.Person

func (p people) String() string { return "" }

// Set reads name=Role+Role, the subject someone signs in under and the app roles they hold.
func (p people) Set(value string) error {
	name, roles, ok := strings.Cut(value, "=")
	if !ok || name == "" {
		return errors.New("a person is name=Role+Role, such as otto=ABOM.Operator")
	}
	p[name] = oidctest.Person{Name: strings.ToUpper(name[:1]) + name[1:], Roles: strings.Split(roles, "+")}
	return nil
}

func main() {
	addr := flag.String("addr", "127.0.0.1:9400", "where to listen")
	issuer := flag.String("issuer", "", "the issuer URL ABOM and the browser both reach this at (default: http://<addr>)")
	clientID := flag.String("client-id", "abom", "the one client this signs people in for")
	who := people{}
	flag.Var(who, "person", "someone who can sign in, as name=Role+Role (repeatable)")
	flag.Parse()
	if len(who) == 0 {
		fmt.Fprintln(os.Stderr, "oidc-issuer: name at least one --person")
		os.Exit(2)
	}
	base := *issuer
	if base == "" {
		base = "http://" + *addr
	}
	if u, err := url.Parse(base); err != nil || !u.IsAbs() {
		fmt.Fprintf(os.Stderr, "oidc-issuer: --issuer %q is not an absolute URL\n", base)
		os.Exit(2)
	}
	srv := &http.Server{Addr: *addr, Handler: oidctest.New(strings.TrimSuffix(base, "/"), *clientID, who),
		ReadHeaderTimeout: 10 * time.Second}
	log.Printf("oidc-issuer: %s for client %s, signing in %d people", base, *clientID, len(who))
	log.Fatal(srv.ListenAndServe())
}
