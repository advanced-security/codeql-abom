package customql

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func aPack(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "custom")
	if _, err := Scaffold(dir, "2.27.0"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// An agent has no shell and its file tools cannot make a directory, so the one its fixtures go in
// is laid out before it starts. A recorded session without it misused a scaffolding tool to get one.
func TestATestIsLaidOutWhereTheFixturesGoAndNamesItsQuery(t *testing.T) {
	dir := aPack(t)
	tests, err := ScaffoldTest(dir, "CheckoutTagPin")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "test", "CheckoutTagPin"); tests != want {
		t.Errorf("laid out %s, want %s", tests, want)
	}
	if info, err := os.Stat(filepath.Join(tests, ".github", "workflows")); err != nil || !info.IsDir() {
		t.Errorf("no workflow directory for the fixtures: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(tests, "CheckoutTagPin.qlref"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != "src/CheckoutTagPin.ql" {
		t.Errorf("the .qlref names %q", body)
	}
}

func TestLayingOutATestNeverOverwritesOne(t *testing.T) {
	dir := aPack(t)
	tests := filepath.Join(dir, "test", "CheckoutTagPin")
	if err := os.MkdirAll(tests, 0o755); err != nil {
		t.Fatal(err)
	}
	qlref := filepath.Join(tests, "CheckoutTagPin.qlref")
	if err := os.WriteFile(qlref, []byte("query: src/CheckoutTagPin.ql\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ScaffoldTest(dir, "CheckoutTagPin"); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(qlref); string(body) != "query: src/CheckoutTagPin.ql\n" {
		t.Errorf("the .qlref was rewritten to %q", body)
	}
}

// The name becomes two paths, so anything but a plain file stem could put them elsewhere.
func TestATestIsLaidOutOnlyForAPlainQueryName(t *testing.T) {
	dir := aPack(t)
	for _, name := range []string{"", "../Evil", "a/b", `a\b`, ".hidden", "Name.ql", "with space",
		"9Lives", strings.Repeat("A", 65)} {
		if _, err := ScaffoldTest(dir, name); err == nil {
			t.Errorf("laid out a test for %q", name)
		}
	}
	if _, err := ScaffoldTest(t.TempDir(), "Fine"); err == nil {
		t.Error("laid out a test in a directory that holds no pack")
	}
}

// A new query exists in the pack from the moment it is decided, declaring the id ABOM recorded, so
// the next session finds it by that id even if the agent never wrote a line of it.
func TestANewQueryIsLaidOutDeclaringItsIDAndReportingNothing(t *testing.T) {
	dir := aPack(t)
	query, tests, err := ScaffoldQuery(dir, "CheckoutTagPin", "acme/checkout-tag-pin", 7)
	if err != nil {
		t.Fatal(err)
	}
	if query != filepath.Join(dir, "src", "CheckoutTagPin.ql") || tests != filepath.Join(dir, "test", "CheckoutTagPin") {
		t.Errorf("laid out %s tested in %s", query, tests)
	}
	body, err := os.ReadFile(query)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"@id acme/checkout-tag-pin", "@kind problem", "request 7", "none()"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the stub does not carry %q:\n%s", want, body)
		}
	}
	if _, _, err := ScaffoldQuery(dir, "CheckoutTagPin", "acme/other", 8); err == nil {
		t.Error("laid a second query over the first")
	}
	for _, id := range []string{"", "no-slash", "acme/", "Acme/Upper", "acme/with space", "acme/*/x",
		"acme/checkout\n * @kind path-problem"} {
		if _, _, err := ScaffoldQuery(dir, "Fresh", id, 9); err == nil {
			t.Errorf("laid out a query declaring %q", id)
		}
	}
}
