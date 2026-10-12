package customql

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDeployedSuiteRejectsMalformedIDsBeforeChangingItsFile(t *testing.T) {
	for _, id := range []string{
		"", " ", "acme/rule\n- exclude:\n    id: /.*/", "acme/rule\r", "/.*/",
		"acme/rule: disabled", "acme/\"rule", "acme/rule # hidden",
	} {
		t.Run(id, func(t *testing.T) {
			dir := t.TempDir()
			if err := WriteDeployed(dir, []string{"acme/known"}); err != nil {
				t.Fatal(err)
			}
			before := deployed(t, dir)
			if err := WriteDeployed(dir, []string{"acme/known", id}); err == nil {
				t.Error("a malformed ID was accepted for deployment")
			}
			if after := deployed(t, dir); after != before {
				t.Errorf("a rejected ID changed the suite:\n%s", after)
			}
		})
	}
}

func TestDeployedSuiteEncodesOnlyItsSelectionDirectives(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDeployed(dir, []string{"yes/no", "acme/known", "yes/no"}); err != nil {
		t.Fatal(err)
	}
	type directive struct {
		Queries string `yaml:"queries"`
		From    string `yaml:"from"`
		Include *struct {
			IDs []string `yaml:"id"`
		} `yaml:"include"`
		Exclude *struct {
			ID string `yaml:"id"`
		} `yaml:"exclude"`
	}
	read := func() []directive {
		t.Helper()
		var got []directive
		decoder := yaml.NewDecoder(strings.NewReader(deployed(t, dir)))
		decoder.KnownFields(true)
		if err := decoder.Decode(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := read()
	if len(got) != 2 || got[0].Queries != "." || got[0].From != Pack ||
		got[0].Include != nil || got[0].Exclude != nil || got[1].Include == nil ||
		got[1].Exclude != nil || !reflect.DeepEqual(got[1].Include.IDs, []string{"acme/known", "yes/no"}) {
		t.Fatalf("deployed IDs changed the suite structure: %+v", got)
	}
	if err := WriteDeployed(dir, nil); err != nil {
		t.Fatal(err)
	}
	got = read()
	if len(got) != 2 || got[1].Include != nil || got[1].Exclude == nil || got[1].Exclude.ID != "/.*/" {
		t.Fatalf("an empty deployment does not explicitly select nothing: %+v", got)
	}
}
