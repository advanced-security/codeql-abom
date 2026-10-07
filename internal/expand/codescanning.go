package expand

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"gopkg.in/yaml.v3"
)

// CodeScanning is what GitHub's code scanning holds for a repository: its actions database and analyses.
type CodeScanning interface {
	FindDatabase(ctx context.Context, repository, language string) (catalog.Database, error)
	DownloadDatabase(ctx context.Context, repository, language, destPath string) error
	ActionsAnalyses(ctx context.Context, repository, ref string) ([]catalog.Analysis, error)
	DownloadAnalysis(ctx context.Context, repository string, id int64, destPath string) error
	// FileContent reads the configuration file default setup applies, which can narrow its paths.
	FileContent(ctx context.Context, repository, path, ref string) ([]byte, error)
}

// Why code scanning's database did not stand in for a build.
const (
	refusedNoDatabase = "no database"
	refusedCommit     = "another commit"
	refusedCodeQL     = "another CodeQL"
	refusedFiles      = "other files"
	refusedUnreadable = "not readable"
)

// ReuseCounts is what code scanning has supplied, and why it did not supply the rest.
type ReuseCounts struct {
	// Databases stood in for a build.
	Databases int
	// Analyses stood in for part of a findings lane.
	Analyses int
	// Refused counts each refusal as "database: <reason>" or "analysis: <reason>".
	Refused map[string]int
}

// reuseCounter counts across every worker of every round.
type reuseCounter struct {
	mu     sync.Mutex
	counts ReuseCounts
}

// count records one answer from code scanning: taken when reason is empty, refused otherwise.
func (c *reuseCounter) count(what, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case reason != "":
		if c.counts.Refused == nil {
			c.counts.Refused = map[string]int{}
		}
		c.counts.Refused[what+": "+reason]++
	case what == "database":
		c.counts.Databases++
	default:
		c.counts.Analyses++
	}
}

// ReuseCounts reports what code scanning has supplied since the Expander was built.
func (e *Expander) ReuseCounts() ReuseCounts {
	e.reuse.mu.Lock()
	defer e.reuse.mu.Unlock()
	out := e.reuse.counts
	out.Refused = maps.Clone(out.Refused)
	if out.Refused == nil {
		out.Refused = map[string]int{}
	}
	return out
}

// since is what was counted after prev.
func (r ReuseCounts) since(prev ReuseCounts) ReuseCounts {
	out := ReuseCounts{Databases: r.Databases - prev.Databases, Analyses: r.Analyses - prev.Analyses,
		Refused: map[string]int{}}
	for reason, n := range r.Refused {
		if n -= prev.Refused[reason]; n > 0 {
			out.Refused[reason] = n
		}
	}
	return out
}

// Asked reports whether code scanning was asked anything.
func (r ReuseCounts) Asked() bool { return r.Databases+r.Analyses+len(r.Refused) > 0 }

func (r ReuseCounts) String() string {
	out := fmt.Sprintf("took %d %s and %d %s", r.Databases, plural(r.Databases, "database", "databases"),
		r.Analyses, plural(r.Analyses, "analysis", "analyses"))
	if len(r.Refused) == 0 {
		return out
	}
	var refused []string
	for _, reason := range slices.Sorted(maps.Keys(r.Refused)) {
		if n := r.Refused[reason]; n > 1 {
			reason = fmt.Sprintf("%s ×%d", reason, n)
		}
		refused = append(refused, reason)
	}
	return out + "; refused " + strings.Join(refused, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// downloadDatabase puts code scanning's database at dbDir if it is the one a build by cli would make.
func (e *Expander) downloadDatabase(ctx context.Context, repository string, tree Tree, dbDir, cli string) bool {
	// A round that does not know its CodeQL cannot match a database, and inherited index filters
	// reach ABOM's builds and not GitHub's, so either way asking spends a download on nothing.
	if e.CodeScanning == nil || cli == "" || os.Getenv("LGTM_INDEX_FILTERS") != "" {
		return false
	}
	reason := e.tryDownload(ctx, repository, tree, dbDir, cli)
	if ctx.Err() != nil {
		return false
	}
	e.reuse.count("database", reason)
	return reason == ""
}

func (e *Expander) tryDownload(ctx context.Context, repository string, tree Tree, dbDir, cli string) string {
	if refusal := e.configFileRefusal(ctx, repository, tree.SHA); refusal != "" {
		return refusal
	}
	listed, err := e.CodeScanning.FindDatabase(ctx, repository, "actions")
	if err != nil {
		return refusalOf(err)
	}
	// The listing's commit spares a download; where it agrees, the database's own still decides.
	if listed.CommitOID != "" && !strings.EqualFold(listed.CommitOID, tree.SHA) {
		return refusedCommit
	}
	if err := os.MkdirAll(filepath.Dir(dbDir), 0o755); err != nil {
		return refusedUnreadable
	}
	scratch, err := os.MkdirTemp(filepath.Dir(dbDir), "download-")
	if err != nil {
		return refusedUnreadable
	}
	defer os.RemoveAll(scratch)
	archive := filepath.Join(scratch, "database.zip")
	if err := e.CodeScanning.DownloadDatabase(ctx, repository, "actions", archive); err != nil {
		return refusalOf(err)
	}
	db, err := runner.Extract(archive, filepath.Join(scratch, "db"))
	if err != nil {
		return refusedUnreadable
	}
	if reason := sameDatabase(db, tree, cli); reason != "" {
		return reason
	}
	if err := os.RemoveAll(dbDir); err != nil {
		return refusedUnreadable
	}
	if err := os.Rename(db, dbDir); err != nil {
		return refusedUnreadable
	}
	return ""
}

func refusalOf(err error) string {
	if errors.Is(err, catalog.ErrNoDatabase) {
		return refusedNoDatabase
	}
	return refusedUnreadable
}

// sameDatabase says why db is not what a build of tree by cli would make, read from db itself.
func sameDatabase(db string, tree Tree, cli string) string {
	body, err := os.ReadFile(filepath.Join(db, "codeql-database.yml"))
	if err != nil {
		return refusedUnreadable
	}
	var meta struct {
		SourceLocationPrefix string `yaml:"sourceLocationPrefix"`
		CreationMetadata     struct {
			SHA string `yaml:"sha"`
		} `yaml:"creationMetadata"`
	}
	if err := yaml.Unmarshal(body, &meta); err != nil {
		return refusedUnreadable
	}
	if tree.SHA == "" || !strings.EqualFold(meta.CreationMetadata.SHA, tree.SHA) {
		return refusedCommit
	}
	if cli == "" || BuiltBy(db) != cli {
		return refusedCodeQL
	}
	indexed, err := archivedSources(db, meta.SourceLocationPrefix)
	if err != nil {
		return refusedUnreadable
	}
	checkout, err := indexedByDefault(tree.Dir)
	if err != nil {
		return refusedUnreadable
	}
	if !slices.Equal(indexed, checkout) {
		return refusedFiles
	}
	return ""
}

// archivedSources lists the files in a database's source archive, relative to its source root.
func archivedSources(db, prefix string) ([]string, error) {
	zr, err := zip.OpenReader(filepath.Join(db, "src.zip"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	// Entries are stored without the leading slash.
	root := strings.TrimPrefix(prefix, "/") + "/"
	files := []string{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel, ok := strings.CutPrefix(f.Name, root)
		if !ok {
			// No checkout path starts with a slash, so a file from outside the root matches none.
			rel = "/" + f.Name
		}
		files = append(files, rel)
	}
	slices.Sort(files)
	return files, nil
}

// indexedByDefault lists the files under dir that the actions extractor's default filters index.
func indexedByDefault(dir string) ([]string, error) {
	files := []string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		parent, base := path.Split(rel)
		yamlFile := strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml")
		switch {
		case parent == ".github/workflows/" && yamlFile,
			strings.HasPrefix(parent, ".github/reusable_workflows/") && yamlFile,
			base == "action.yml" || base == "action.yaml" || base == "actions.lock":
			files = append(files, rel)
		}
		return nil
	})
	slices.Sort(files)
	return files, err
}
