package checks

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	native "github.com/github/actions-lockfile/go/pkg/lockfile"
	"gopkg.in/yaml.v3"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

const readingDeclared = "1 what the commit's files declare"

// fileBatch is how many files one query reads.
const fileBatch = 100

// declared is one dependency a file declares, as either reading has it. target and ref are what the
// value names, empty where the reading names nothing: a local action, an image, a value GitHub's
// parser does not read.
type declared struct {
	file, kind, raw, target, ref string
}

// declaredOutcome is one commit compared: unread says why it was not, and class is "" where the
// two readings agree.
type declaredOutcome struct {
	unread, class, claim, independent string
}

// declarations is check 1: what each commit the queue stands at declared, as the graph reads it,
// against that commit's own files read by a parser that shares nothing with the extraction.
func (c *Checker) declarations(ctx context.Context, s store.Backend) ([]store.CheckPart, error) {
	commits, err := s.DeclaredCommits()
	if err != nil {
		return nil, err
	}
	open, err := c.Store.OpenSubjects(1)
	if err != nil {
		return nil, err
	}
	var rows []store.DeclaredCommit
	var cands []candidate
	excluded := map[string]map[string]int{stratumOwned: {}, stratumExternal: {}}
	for _, d := range commits {
		stratum := stratumExternal
		if d.Owned {
			stratum = stratumOwned
		}
		switch {
		case len(d.Answers) == 0:
			excluded[stratum]["nothing analyzed it, nor a commit with the same content"]++
		case d.Unknown:
			excluded[stratum]["what it declared is not known: recorded before sightings kept content"]++
		default:
			rows = append(rows, d)
			cands = append(cands, candidate{key: d.Repository + "@" + d.Commit, stratum: stratum, changed: d.Changed})
		}
	}
	claimed, gone := openAmong(open, cands)
	d := drawSample(cands, claimed, c.Recent, c.Sample, c.rng())
	outcomes := map[int]declaredOutcome{}
	for _, i := range d.order() {
		if outcomes[i], err = c.compareCommit(ctx, s, rows[i]); err != nil {
			return nil, err
		}
	}

	compare := func(name string, idx []int, population int) store.CheckPart {
		p := store.CheckPart{Part: name, Population: &population}
		unread := map[string]int{}
		for _, i := range idx {
			switch o := outcomes[i]; {
			case o.unread != "":
				unread[o.unread]++
			case o.class == "":
				p.Compared++
				p.Agreed = append(p.Agreed, store.CheckSubject{Subject: cands[i].key, Reading: readingDeclared})
			default:
				p.Compared++
				p.Disagreements = append(p.Disagreements, store.Disagreement{Subject: cands[i].key,
					Reading: readingDeclared, Class: o.class, Claim: o.claim, Independent: o.independent})
			}
		}
		skip(&p, unread)
		return p
	}
	reread := compare("1a open disagreements, read again", d.reread, len(open))
	withdrawn(&reread, gone, "settled: the queue stands at that commit no more")
	parts := []store.CheckPart{reread, compare("1b commits analyzed most recently", d.recent, len(cands))}
	for _, stratum := range []struct{ key, name string }{
		{stratumOwned, "1c owned commits, sampled at random"},
		{stratumExternal, "1d external commits, sampled at random"},
	} {
		p := compare(stratum.name, d.random[stratum.key], d.sizes[stratum.key])
		bounded(&p)
		skip(&p, excluded[stratum.key])
		parts = append(parts, p)
	}
	return parts, nil
}

// compareCommit reads one commit's indexed files from GitHub, charging the checker's GraphQL pool,
// and compares what they declare with what the graph says the commit declared. Reasons it gives
// for an unread commit name no subject, so a run's skips stay countable.
func (c *Checker) compareCommit(ctx context.Context, s store.Backend, d store.DeclaredCommit) (declaredOutcome, error) {
	tree, err := c.GitHub.Tree(ctx, d.Repository, d.Commit)
	if ctx.Err() != nil {
		return declaredOutcome{}, ctx.Err()
	}
	if why := treeUnread(tree, err); why != "" {
		return declaredOutcome{unread: why}, nil
	}
	var paths []string
	for _, e := range tree.Entries {
		if e.Type == "blob" && e.Mode != "120000" && indexed(e.Path) {
			paths = append(paths, e.Path)
		}
	}
	files := make(map[string]string, len(paths))
	for start := 0; start < len(paths); start += fileBatch {
		batch := paths[start:min(start+fileBatch, len(paths))]
		if err := c.GraphQL.Wait(ctx); err != nil {
			return declaredOutcome{}, err
		}
		got, err := c.GitHub.CommitFiles(ctx, d.Repository, d.Commit, batch)
		c.GraphQL.Spend(got.Requests, got.Cost)
		c.GraphQL.Observe(got.Reading)
		if ctx.Err() != nil {
			return declaredOutcome{}, ctx.Err()
		}
		if why := filesUnread(batch, got, err); why != "" {
			return declaredOutcome{unread: why}, nil
		}
		for _, p := range batch {
			files[p] = got.Files[p].Text
		}
	}
	theirs, unparsed := readDeclared(files)
	if len(unparsed) > 0 {
		return declaredOutcome{unread: "unread: a file the independent reader could not parse"}, nil
	}
	claims, err := s.CommitDeclarations(d.Repository, d.Answers)
	if err != nil {
		return declaredOutcome{}, err
	}
	ours := make([]declared, 0, len(claims))
	for _, cl := range claims {
		ours = append(ours, declared{file: cl.File, kind: cl.Kind, raw: cl.Raw, target: cl.Target, ref: cl.Ref})
	}
	return compareDeclared(ours, theirs), nil
}

// treeUnread says why a listed tree cannot show what a commit declares: GitHub refused it, or listed
// only part of it, and files it did not list cannot be shown to declare nothing.
func treeUnread(tree catalog.Tree, err error) string {
	switch {
	case err != nil:
		return "unread: GitHub would not list the tree"
	case tree.Truncated:
		return "unread: GitHub listed only part of the tree"
	}
	return ""
}

// filesUnread says why a batch of a commit's files cannot be compared, "" where every one was read.
func filesUnread(paths []string, got catalog.FileBatch, err error) string {
	if err != nil {
		return "unread: the query for its files failed"
	}
	for _, p := range paths {
		if f, ok := got.Files[p]; !ok || f.Unread != "" {
			return "unread: GitHub did not give a file's text"
		}
	}
	return ""
}

// indexed is the actions extractor's default file set, which is what a database is built from:
// workflows at the root of .github/workflows, anything under .github/reusable_workflows, and every
// action manifest. It is written here rather than taken from the sparse filter ABOM fetches with,
// so a filter that drops a file the extractor reads shows as a declaration ABOM does not record.
// Lockfiles are indexed too, and declare nothing.
func indexed(p string) bool {
	ext := path.Ext(p)
	workflow := ext == ".yml" || ext == ".yaml"
	switch {
	case isActionManifest(p):
		return true
	case path.Dir(p) == ".github/workflows":
		return workflow
	case strings.HasPrefix(p, ".github/reusable_workflows/"):
		return workflow
	}
	return false
}

func isActionManifest(p string) bool {
	base := path.Base(p)
	return base == "action.yml" || base == "action.yaml"
}

// readDeclared reads what each file declares, with the reason each file it could not parse was not.
func readDeclared(files map[string]string) ([]declared, map[string]string) {
	var out []declared
	unparsed := map[string]string{}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		read := workflowDeclares
		if isActionManifest(p) {
			read = actionDeclares
		}
		got, err := read(p, files[p])
		if err != nil {
			unparsed[p] = err.Error()
			continue
		}
		out = append(out, got...)
	}
	return out, unparsed
}

// workflowDeclares walks a workflow's jobs for the reusable workflows they call and the actions
// their steps use.
func workflowDeclares(file, text string) ([]declared, error) {
	var doc struct {
		Jobs map[string]struct {
			Uses  string `yaml:"uses"`
			Steps []struct {
				Uses string `yaml:"uses"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, err
	}
	var out []declared
	for _, job := range doc.Jobs {
		if job.Uses != "" {
			out = append(out, workflowCall(file, job.Uses))
		}
		for _, step := range job.Steps {
			if step.Uses != "" {
				out = append(out, actionUse(file, store.EdgeKindStepUsesAction, step.Uses))
			}
		}
	}
	return out, nil
}

// actionDeclares is what a composite action's steps use, read by GitHub's own metadata parser.
func actionDeclares(file, text string) ([]declared, error) {
	meta, err := native.ParseActionMeta(text)
	if err != nil {
		return nil, err
	}
	var out []declared
	if meta.Execution == native.ExecComposite {
		for _, raw := range meta.NestedUses {
			out = append(out, actionUse(file, store.EdgeKindCompositeStepUsesAction, raw))
		}
	}
	return out, nil
}

func actionUse(file, kind, raw string) declared {
	d := declared{file: file, kind: kind, raw: raw}
	if strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "docker://") {
		return d
	}
	if a := native.ParseActionRef(raw); a != nil {
		d.target, d.ref = strings.ToLower(a.FullName()), a.Ref
	}
	return d
}

func workflowCall(file, raw string) declared {
	d := declared{file: file, kind: store.EdgeKindJobUsesReusableWorkflow, raw: raw}
	if w := native.ParseReusableWorkflowRef(raw); w != nil {
		d.target, d.ref = strings.ToLower(w.FullName()), w.Ref
	}
	return d
}

// compareDeclared matches the readings value for value within each file and kind, as many times as
// each wrote it, then compares the target of each match where GitHub's parser read one. A value
// either side lacks is extraction; the same value read as another target is normalization.
func compareDeclared(ours, theirs []declared) declaredOutcome {
	type at struct{ file, kind, raw string }
	pending := map[at][]declared{}
	for _, t := range theirs {
		k := at{t.file, t.kind, t.raw}
		pending[k] = append(pending[k], t)
	}
	var extracted, extractedTheirs, misread, misreadTheirs []string
	for _, o := range sortedDeclarations(ours) {
		k := at{o.file, o.kind, o.raw}
		if len(pending[k]) == 0 {
			extracted = append(extracted, "ABOM records "+o.file+": "+o.raw)
			extractedTheirs = append(extractedTheirs, o.file+" declares no "+o.raw)
			continue
		}
		t := pending[k][0]
		pending[k] = pending[k][1:]
		if t.target != "" && (!strings.EqualFold(t.target, o.target) || t.ref != o.ref) {
			misread = append(misread, fmt.Sprintf("ABOM reads %s: %s as %s@%s", o.file, o.raw, o.target, o.ref))
			misreadTheirs = append(misreadTheirs, fmt.Sprintf("GitHub's parser reads it as %s@%s", t.target, t.ref))
		}
	}
	var missed, missedTheirs []string
	for _, k := range slices.SortedFunc(maps.Keys(pending), func(a, b at) int {
		return cmp.Or(strings.Compare(a.file, b.file), strings.Compare(a.kind, b.kind), strings.Compare(a.raw, b.raw))
	}) {
		for range pending[k] {
			missed = append(missed, "ABOM records nothing for "+k.file+": "+k.raw)
			missedTheirs = append(missedTheirs, k.file+" declares "+k.raw)
		}
	}
	switch {
	case len(missed)+len(extracted) > 0:
		return declaredOutcome{class: "extraction", claim: summarize(append(missed, extracted...)),
			independent: summarize(append(missedTheirs, extractedTheirs...))}
	case len(misread) > 0:
		return declaredOutcome{class: "normalization", claim: summarize(misread),
			independent: summarize(misreadTheirs)}
	}
	return declaredOutcome{}
}

func sortedDeclarations(ds []declared) []declared {
	out := slices.Clone(ds)
	slices.SortFunc(out, func(a, b declared) int {
		return cmp.Or(strings.Compare(a.file, b.file), strings.Compare(a.kind, b.kind),
			strings.Compare(a.raw, b.raw), strings.Compare(a.target, b.target), strings.Compare(a.ref, b.ref))
	})
	return out
}

// summarize names the first few differences and counts the rest, so a disagreement stays readable.
func summarize(items []string) string {
	const shown = 3
	if len(items) <= shown {
		return strings.Join(items, "; ")
	}
	return fmt.Sprintf("%s; and %d more", strings.Join(items[:shown], "; "), len(items)-shown)
}
