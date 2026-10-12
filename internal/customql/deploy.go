package customql

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// DeploySuite is the suite a tier binds to run what the operator has deployed.
//
// Separate from the development suite deliberately. `abom-custom.qls` selects the whole pack, so
// a query appears in it the moment the file is saved — which is what a run should be able to
// name, and exactly what a crawl must not pick up. Deploying is the act that moves a query from
// the first to the second.
const DeploySuite = Pack + ":codeql-suites/abom-deployed.qls"

// DeploySuitePath is where that suite lives inside the pack.
const DeploySuitePath = "codeql-suites/abom-deployed.qls"

// WriteDeployed rewrites the deployed suite from the query ids given.
//
// The whole set each time rather than an append: the suite is a projection of what is at the
// deploy stage, and a file that only ever grew would keep running a query somebody took back.
//
// An empty set writes a suite that selects nothing rather than removing the file, so a tier bound
// to it keeps its binding and simply has nothing to run. Removing it would make the binding look
// invented the next time the grid was drawn.
func WriteDeployed(dir string, queryIDs []string) error {
	if dir == "" {
		return fmt.Errorf("custom queries: no directory named")
	}
	body, err := deployedSuite(queryIDs)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, filepath.FromSlash(DeploySuitePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("custom queries: preparing the suite directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("custom queries: writing the deployed suite: %w", err)
	}
	return nil
}

// deployedSuite renders the suite. Sorted and de-duplicated so the file is byte-identical for the
// same set, which is what keeps a rewrite that changed nothing out of the operator's diff.
func deployedSuite(queryIDs []string) (string, error) {
	seen := map[string]bool{}
	ids := make([]string, 0, len(queryIDs))
	for _, id := range queryIDs {
		if !ValidQueryID(id) {
			return "", fmt.Errorf("custom queries: %q is not a query id such as acme/checkout-tag-pin", id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)

	directives := []map[string]any{{"queries": ".", "from": Pack}}
	if len(ids) == 0 {
		// A suite with no selection would run the whole pack, which is the opposite of what an
		// empty deploy set means.
		directives = append(directives, map[string]any{"exclude": map[string]string{"id": "/.*/"}})
	} else {
		directives = append(directives, map[string]any{"include": map[string][]string{"id": ids}})
	}
	body, err := yaml.Marshal(directives)
	if err != nil {
		return "", fmt.Errorf("custom queries: encoding the deployed suite: %w", err)
	}
	return "# Written by ABOM from the query versions at the deploy stage. Edits are lost.\n" +
		string(body), nil
}
