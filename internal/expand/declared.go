package expand

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// cannotRead opens every reason a version's declaration could not be read for.
const cannotRead = "Declared metadata cannot be read: "

// ReadDeclarations records what each listed version declares, read from the file on disk whose
// content is that version, and says why for every one it could not read.
//
// Content is the only match. A version is the hash of a query and the libraries it imports, so a
// file hashing to it is it; the same id at another version, even in the same pack version, may
// declare another severity, and reading that instead reports a score under a version that never
// declared it.
func ReadDeclarations(
	ctx context.Context, st store.Backend, cli Resolver, unread []store.CatalogQuery,
) (map[string]string, error) {
	unreadable := map[string]string{}
	if len(unread) == 0 {
		return unreadable, nil
	}
	if _, err := cli.Version(ctx); err != nil {
		return nil, fmt.Errorf("reading declared metadata needs CodeQL: %w", err)
	}
	sweep := &declarationSweep{cli: cli, byVersion: map[string][]string{}, onDisk: map[string][]string{},
		tried: map[string]error{}, read: map[string]readMetadata{}}
	// The pack version each ran from first, then wherever the search path puts the pack now: a
	// working copy has no pack version, and a pack bumped without editing a query still holds it.
	for _, q := range unread {
		if ref := versionedRef(q); ref != "" {
			if err := sweep.resolve(ctx, ref); err != nil {
				return nil, err
			}
		}
	}
	for _, q := range unread {
		if len(sweep.byVersion[q.QueryVersion]) > 0 {
			continue
		}
		for _, pack := range strings.Fields(q.Pack) {
			if err := sweep.resolve(ctx, pack+":."); err != nil {
				return nil, err
			}
		}
	}

	read := make([]store.QueryMetadata, 0, len(unread))
	for _, q := range unread {
		md, ok, err := sweep.declaration(ctx, q)
		if err != nil {
			return nil, err
		}
		if !ok {
			unreadable[q.QueryID+"\x00"+q.QueryVersion] = sweep.reason(q)
			continue
		}
		read = append(read, store.QueryMetadata{QueryID: q.QueryID, QueryVersion: q.QueryVersion, Pairs: md.Pairs})
	}
	if err := st.RecordQueryMetadata(read); err != nil {
		return nil, err
	}
	return unreadable, nil
}

type declarationSweep struct {
	cli Resolver
	// byVersion is every file resolved so far, by the version its content is.
	byVersion map[string][]string
	// onDisk is each pack version the resolved files say they are, by pack. Never the version a
	// reference asked for: a local pack answers a reference to any version of itself.
	onDisk map[string][]string
	// tried is every reference resolved so far, with why where the CLI could not resolve it.
	tried map[string]error
	// read is each file's declaration, or why it could not be read, once per file.
	read map[string]readMetadata
}

type readMetadata struct {
	md  runner.Metadata
	err error
}

// versionedRef is the pack version a row ran from, as a reference the CLI resolves; empty where the
// row names no single pack version.
func versionedRef(q store.CatalogQuery) string {
	if q.Pack == "" || q.PackVersion == "" || strings.ContainsAny(q.Pack+q.PackVersion, " \t") {
		return ""
	}
	return q.Pack + "@" + q.PackVersion + ":."
}

func (s *declarationSweep) resolve(ctx context.Context, ref string) error {
	if _, tried := s.tried[ref]; tried {
		return nil
	}
	paths, err := s.cli.ResolveQueries(ctx, ref)
	if err != nil {
		// A reference the CLI could not resolve is a state of the disk the reasons name rather
		// than a failure of the sweep. A sweep that was stopped has learned nothing missing.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.tried[ref] = err
		return nil
	}
	s.tried[ref] = nil
	versions, err := runner.QueryVersions(paths)
	if err != nil {
		return fmt.Errorf("versioning what %s holds: %w", ref, err)
	}
	for i, v := range versions {
		if !slices.Contains(s.byVersion[v], paths[i]) {
			s.byVersion[v] = append(s.byVersion[v], paths[i])
		}
		if pack, version, _ := runner.PackOf(paths[i]); pack != "" && !slices.Contains(s.onDisk[pack], version) {
			s.onDisk[pack] = append(s.onDisk[pack], version)
		}
	}
	return nil
}

// declaration is what the file that is this version declares, from the first such file declaring
// the row's own id.
func (s *declarationSweep) declaration(ctx context.Context, q store.CatalogQuery) (runner.Metadata, bool, error) {
	for _, path := range slices.Sorted(slices.Values(s.byVersion[q.QueryVersion])) {
		md, err := s.metadata(ctx, path)
		if err != nil {
			if ctx.Err() != nil {
				return runner.Metadata{}, false, ctx.Err()
			}
			continue
		}
		if md.ID == q.QueryID {
			return md, true, nil
		}
	}
	return runner.Metadata{}, false, nil
}

// metadata reads a file's declaration once, however many rows it is the version of. A read the
// sweep's own cancellation stopped says nothing about the file and is not kept.
func (s *declarationSweep) metadata(ctx context.Context, path string) (runner.Metadata, error) {
	if r, ok := s.read[path]; ok {
		return r.md, r.err
	}
	md, err := s.cli.ResolveMetadata(ctx, path)
	if err == nil || ctx.Err() == nil {
		s.read[path] = readMetadata{md, err}
	}
	return md, err
}

func (s *declarationSweep) reason(q store.CatalogQuery) string {
	for _, path := range s.byVersion[q.QueryVersion] {
		if err := s.read[path].err; err != nil {
			return fmt.Sprintf("%sCodeQL could not read %s: %v", cannotRead, path, err)
		}
	}
	if q.Pack == "" {
		return cannotRead + "no pack was recorded for this version."
	}
	asked := strings.TrimSpace(q.Pack + " " + q.PackVersion)
	at := strings.Join(slices.Sorted(slices.Values(s.onDisk[q.Pack])), " or ")
	switch {
	case at == "":
	case q.PackVersion == "" || slices.Contains(s.onDisk[q.Pack], q.PackVersion):
		return fmt.Sprintf("%sno file of %s %s on disk is this version.", cannotRead, q.Pack, cmp.Or(q.PackVersion, at))
	default:
		return fmt.Sprintf("%s%s is not on disk, and no file of %s is this version.", cannotRead, asked, at)
	}
	for _, ref := range []string{versionedRef(q), q.Pack + ":."} {
		if err := s.tried[ref]; err != nil && !strings.Contains(err.Error(), "cannot be found") {
			return fmt.Sprintf("%s%s could not be resolved: %v", cannotRead, asked, err)
		}
	}
	return fmt.Sprintf("%s%s is not on disk.", cannotRead, asked)
}
