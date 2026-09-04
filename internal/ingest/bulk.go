package ingest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/sarifin"
)

// RepoResolver maps a downloaded SARIF filename back to the repository it came from.
//
// Both gh-mrva and sarif-sql flatten owner/name into the filename, replacing the slash with a
// separator that repository names may themselves contain. Splitting on position is therefore
// unreliable; matching against the known repository list is not.
type RepoResolver struct {
	// canonical maps a sanitised prefix to the repository names that produce it. More than one
	// name means the filename is genuinely ambiguous.
	canonical map[string][]string
}

// NewRepoResolver indexes the repositories a run covered.
func NewRepoResolver(repos []string) *RepoResolver {
	r := &RepoResolver{canonical: make(map[string][]string, len(repos)*2)}
	for _, repo := range repos {
		for _, sep := range []string{"_", "-"} {
			key := strings.ToLower(strings.ReplaceAll(repo, "/", sep))
			r.canonical[key] = appendUnique(r.canonical[key], repo)
		}
	}
	return r
}

// Resolve returns the repository a SARIF filename belongs to.
//
// The longest matching prefix wins, so octo/app-service is preferred over octo/app for
// octo_app-service_42.sarif. A prefix that several repositories share resolves to nothing:
// guessing would attribute findings to the wrong repository silently.
func (r *RepoResolver) Resolve(filename string) (string, bool) {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(filename), ".sarif"))

	var keys []string
	for key := range r.canonical {
		if strings.HasPrefix(name, key) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return "", false
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })

	best := keys[0]
	if len(keys) > 1 && len(keys[1]) == len(best) {
		return "", false
	}
	if matches := r.canonical[best]; len(matches) == 1 {
		return matches[0], true
	}
	return "", false
}

// LoadRepoList reads a gh-mrva repository list file. An empty listName merges every list.
func LoadRepoList(path, listName string) ([]string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("reading repository list: %w", err)
	}

	var lists map[string][]string
	if err := json.Unmarshal(data, &lists); err != nil {
		return nil, fmt.Errorf("parsing repository list: %w", err)
	}

	if listName != "" {
		repos, ok := lists[listName]
		if !ok {
			return nil, fmt.Errorf("repository list %q not found in %s", listName, path)
		}
		return repos, nil
	}

	var all []string
	for _, repos := range lists {
		all = append(all, repos...)
	}
	sort.Strings(all)
	return all, nil
}

// BulkResult summarises a directory ingestion.
type BulkResult struct {
	Files    int
	Findings int
	// Unresolved names files whose repository could not be determined; they are skipped rather
	// than attributed to a guess.
	Unresolved []string
	// Failed names files that could not be read or parsed, such as truncated downloads.
	Failed []string
}

// FindingsFromDirectory ingests every SARIF file in a directory.
//
// A single unreadable or unattributable file is recorded and skipped: at a thousand
// repositories per run, aborting on the first bad download would waste the whole batch.
func (i *Ingester) FindingsFromDirectory(
	dir string, resolver *RepoResolver, opts Options,
) (BulkResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return BulkResult{}, fmt.Errorf("reading %s: %w", dir, err)
	}

	var res BulkResult
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(name), ".sarif") {
			continue
		}

		repo, ok := resolver.Resolve(name)
		path := filepath.Join(dir, name)

		// The run records the repository it analysed, which beats inferring it from a
		// filename that flattened owner/name with an ambiguous separator.
		if prov, err := readProvenanceFile(path); err == nil && prov.Repository != "" {
			repo, ok = prov.Repository, true
		}
		if !ok {
			res.Unresolved = append(res.Unresolved, name)
			continue
		}

		n, err := i.ingestSarifFile(path, repo, opts)
		if err != nil {
			res.Failed = append(res.Failed, name)
			continue
		}
		res.Files++
		res.Findings += n
	}
	return res, nil
}

func readProvenanceFile(path string) (sarifin.Provenance, error) {
	f, err := os.Open(path)
	if err != nil {
		return sarifin.Provenance{}, err
	}
	defer f.Close()
	return sarifin.ReadProvenance(f)
}

func (i *Ingester) ingestSarifFile(path, repo string, opts Options) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	fileOpts := opts
	fileOpts.Repository = repo
	return i.FindingsFromSARIF(f, fileOpts)
}

func appendUnique(existing []string, value string) []string {
	for _, v := range existing {
		if v == value {
			return existing
		}
	}
	return append(existing, value)
}
