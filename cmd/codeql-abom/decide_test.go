package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/coordination"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Every command either is decided as a registered operation or says why it needs no decision of its
// own, and the table names no command that does not exist. A command added without either fails here.
func TestEveryCommandIsDecidedOrSaysWhyNot(t *testing.T) {
	names := map[string]bool{}
	for _, c := range subcommands {
		names[c.name] = true
		d, ok := commandDecisions[c.name]
		switch {
		case !ok:
			t.Errorf("%s is in no row of commandDecisions; name its operation or why it decides nothing", c.name)
		case d.op == "" && d.exempt == "":
			t.Errorf("%s names neither an operation nor a reason", c.name)
		case d.op != "":
			if _, ok := authz.Lookup(d.op); !ok {
				t.Errorf("%s names %q, which the guard registry does not hold", c.name, d.op)
			}
		}
	}
	for name := range commandDecisions {
		if !names[name] {
			t.Errorf("commandDecisions names %s, which is not a command", name)
		}
	}
}

// A command decided as an operation records that decision itself: exactly the commands the table
// says decide are the ones whose source asks decideCommand or openDecided, by their own name.
func TestEveryDecidingCommandAsksForItsDecision(t *testing.T) {
	var asked []string
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(tree, func(n ast.Node) bool {
			// openDecided passes its caller's name on; the caller is what names the command.
			if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name == "openDecided" {
				return false
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			at, ok := map[string]int{"decideCommand": 2, "openDecided": 1}[id.Name]
			if !ok || len(call.Args) <= at {
				return true
			}
			lit, ok := call.Args[at].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: %s names its command with something other than a literal", file, id.Name)
				return true
			}
			name, _ := strconv.Unquote(lit.Value)
			asked = append(asked, name)
			return true
		})
	}
	var want []string
	for name, d := range commandDecisions {
		if d.op != "" {
			want = append(want, name)
		}
	}
	slices.Sort(asked)
	asked = slices.Compact(asked)
	slices.Sort(want)
	if !slices.Equal(asked, want) {
		t.Errorf("commands asking for a decision: %v\nwant those the table decides: %v", asked, want)
	}
}

// The command line's decision is logged before it acts, naming the command and what it was given.
func TestACommandsDecisionIsLoggedBeforeItActs(t *testing.T) {
	db := ownDB(t)
	runOwn(t, "--db", db, "--org", "acme")

	var operations []string
	for _, d := range decisionsIn(t, db) {
		operations = append(operations, d.Subject())
		if d.Subject() == "abom/command:own" && !strings.Contains(string(d.Evidence()), `"--org","acme"`) {
			t.Errorf("the command's evidence does not say what it was given: %s", d.Evidence())
		}
	}
	if len(operations) < 2 || operations[0] != "abom/command:own" || !strings.HasPrefix(operations[1], "abom/owned-scope@") {
		t.Errorf("logged %v; want the command decided before the declaration it makes", operations)
	}
}

// In a pod the command line is whichever ServiceAccount the pod runs as, so its decisions say so, and
// the declaration it makes cites the command's decision as one made through the interface cites the
// route's.
func TestACommandInAPodNamesItsServiceAccountAndItsDeclarationCitesIt(t *testing.T) {
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"system:serviceaccount:abom:abom-coordinator"}`))
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("e30."+claims+".sig"), 0o600); err != nil {
		t.Fatal(err)
	}
	was := serviceAccountToken
	serviceAccountToken = token
	t.Cleanup(func() { serviceAccountToken = was })
	db := ownDB(t)

	runOwn(t, "--db", db, "--org", "acme")

	decisions := decisionsIn(t, db)
	if len(decisions) < 2 {
		t.Fatalf("logged %d decisions; want the command's and the declaration's", len(decisions))
	}
	command, declaration := decisions[len(decisions)-2], decisions[len(decisions)-1]
	want := authz.Local("command line as system:serviceaccount:abom:abom-coordinator")
	if command.Principal() != want || declaration.Principal() != want {
		t.Errorf("decisions name %+v and %+v; want %+v", command.Principal(), declaration.Principal(), want)
	}
	if !strings.Contains(string(declaration.Evidence()), `"route":"`+command.Digest()+`"`) {
		t.Errorf("the declaration's evidence %s does not cite the command's decision", declaration.Evidence())
	}
}

// The log cannot be edited and every viewer may read it, so a credential given on the command line,
// such as the password in a PostgreSQL URL, would be published for good. What a command was given
// stays readable; what could sign in anywhere does not reach the log.
func TestACommandsEvidenceCarriesNoCredential(t *testing.T) {
	db := ownDB(t)
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	args := []string{
		"--db", "postgres://abom:hunter2@db.internal:5432/abom?sslmode=require&password=second",
		"--read-replica=postgresql://reader:pw-three@replica/abom",
		"--org", "acme",
		"--webhook-secret", "s3cr3t-value",
		"--github-token=ghs_tokenvalue",
		"host=db.internal password=pw-four dbname=abom",
	}
	if _, err := decideCommand(context.Background(), s, "prune", args); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Assertions(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var env authz.Envelope
	if err := json.Unmarshal(entries[len(entries)-1].Envelope, &env); err != nil {
		t.Fatal(err)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		t.Fatal(err)
	}
	logged := string(payload)
	for _, secret := range []string{"hunter2", "second", "pw-three", "s3cr3t-value", "ghs_tokenvalue", "pw-four"} {
		if strings.Contains(logged, secret) {
			t.Errorf("the log holds %q", secret)
		}
	}
	for _, kept := range []string{`"--org","acme"`, "db.internal:5432", "sslmode=require", "replica/abom", `"--webhook-secret","REDACTED"`} {
		if !strings.Contains(logged, kept) {
			t.Errorf("the log lost %q, which says what the command was given: %s", kept, logged)
		}
	}
}

// A command that cannot hold the coordinator's lease does nothing, so it decides nothing either: the
// log records only what a command went on to do.
func TestExpandAllDecidesOnlyOnceItHoldsTheLease(t *testing.T) {
	db := ownDB(t)
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _, release, err := coordination.Hold(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()

	var out, errs bytes.Buffer
	if err := run(context.Background(), []string{"expand-all", "--db", db, "--work-dir", t.TempDir()}, &out, &errs); err == nil {
		t.Fatal("expand-all ran while another coordinator held the lease")
	}
	if logged, err := s.Assertions(0, 0); err != nil || len(logged) != 0 {
		t.Errorf("the log holds %d entries (%v); expand-all never ran, so it decided nothing", len(logged), err)
	}
}

// A declaration no decision holds for is not "nothing owned": a command that reports on owned
// repositories says it cannot, and why, rather than reporting on none.
func TestACommandOverOwnedRepositoriesSaysWhenTheScopeIsUndecided(t *testing.T) {
	for _, command := range []string{"branches", "closure"} {
		t.Run(command, func(t *testing.T) {
			db := ownDB(t)
			s, err := store.Open(db, store.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB().Exec(`INSERT INTO abom_owned_scope (owner, kind, enabled, includes_only, declared_at)
				VALUES ('acme', 'organization', 1, 0, '2026-01-01T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			s.Close()

			var out, errs bytes.Buffer
			err = run(context.Background(), []string{command, "--db", db}, &out, &errs)
			if err == nil || !strings.Contains(err.Error(), "not confirmed") || !strings.Contains(err.Error(), "Settings") {
				t.Errorf("%s under an undecided declaration = %v; want it refused, saying why and where to decide it", command, err)
			}
		})
	}
}
