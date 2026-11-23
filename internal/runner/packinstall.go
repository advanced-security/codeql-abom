package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/customql"
)

// packLock is what `codeql pack install` writes, and what the CLI will not resolve a pack's
// dependencies without.
const packLock = "codeql-pack.lock.yml"

// InstallPack installs the dependencies of the operator's pack and of its test pack, wherever
// either has no lock, under the CodeQL lock.
//
// ABOM scaffolded the pack, so ABOM installs it: the CLI refuses to compile a query or a test in a
// pack whose dependencies were never installed, even when every one of them is already downloaded,
// and the way out was a command typed by hand in two directories. Only where no lock exists: a
// lock is what makes the pack resolve the dependencies it was reviewed with, and replacing one is
// `codeql pack upgrade`, which is the operator's to run — except a lock ABOM itself wrote under the
// "*" its older scaffold declared, which nobody chose and alignActionsAll brings forward.
func (r *Runner) InstallPack(ctx context.Context) error {
	return r.installPack(ctx, true)
}

// InstallPackHeld is InstallPack for a caller already holding the lock: an authoring session holds
// it for its whole life, and would otherwise wait on itself.
func (r *Runner) InstallPackHeld(ctx context.Context) error {
	return r.installPack(ctx, false)
}

func (r *Runner) installPack(ctx context.Context, lock bool) error {
	// Nothing to install takes no lock: a Test or a run of an installed pack must not queue behind
	// a crawl to find that out.
	root := CustomPackRoot()
	unlocked := unlockedPacks(root)
	if len(unlocked) == 0 && (r.Offline || len(unaligned(root, customql.ActionsAll)) == 0) {
		return nil
	}
	if r.Offline {
		return fmt.Errorf("%s has no lock file, and this server installs nothing from a registry; "+
			"provision its locks before serving", strings.Join(unlocked, " and "))
	}
	if lock {
		release, err := r.Lock.Acquire(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	if err := alignActionsAll(root, customql.ActionsAll); err != nil {
		return err
	}
	// Read again under the lock, which another install may have been holding.
	for _, dir := range unlockedPacks(root) {
		// The search path, because the test pack names the query pack, which is on no registry.
		args := append([]string{"pack", "install", dir}, localPackArgs()...)
		out, err := exec.CommandContext(ctx, r.codeql(), args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("codeql pack install %s: %w: %s", dir, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// unlockedPacks lists the operator's query pack and its test pack, where each is a pack with no
// lock. The query pack first: the test pack's dependencies are resolved through it.
func unlockedPacks(root string) []string {
	if root == "" {
		return nil
	}
	var out []string
	for _, dir := range []string{root, filepath.Join(root, "test")} {
		if !isPack(dir) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, packLock)); err == nil {
			continue
		}
		out = append(out, dir)
	}
	return out
}

// isPack reports whether dir holds a pack manifest, under either name the CLI reads.
func isPack(dir string) bool {
	for name := range manifestNames {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// anyActionsAll is how ABOM's older scaffold declared codeql/actions-all: any version, which nobody chose.
const anyActionsAll = `  codeql/actions-all: "*"`

// unaligned lists the operator's packs whose manifest still declares codeql/actions-all as "*" and
// whose lock resolved another version than want.
func unaligned(root, want string) []string {
	if root == "" {
		return nil
	}
	var out []string
	for _, dir := range []string{root, filepath.Join(root, "test")} {
		manifest, err := os.ReadFile(filepath.Join(dir, "qlpack.yml"))
		if err != nil || !strings.Contains(string(manifest), anyActionsAll+"\n") {
			continue
		}
		lock, err := os.ReadFile(filepath.Join(dir, packLock))
		if err != nil || strings.Contains(string(lock), "  codeql/actions-all:\n    version: "+want+"\n") {
			continue
		}
		out = append(out, dir)
	}
	return out
}

// alignActionsAll pins codeql/actions-all to want in each pack nobody pinned, and drops the lock ABOM
// wrote for it, so the install that follows locks the library ABOM's own queries run.
func alignActionsAll(root, want string) error {
	for _, dir := range unaligned(root, want) {
		path := filepath.Join(dir, "qlpack.yml")
		manifest, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		pinned := strings.Replace(string(manifest), anyActionsAll+"\n", "  codeql/actions-all: "+want+"\n", 1)
		if err := os.WriteFile(path, []byte(pinned), 0o644); err != nil {
			return fmt.Errorf("pinning codeql/actions-all %s in %s: %w", want, path, err)
		}
		if err := os.Remove(filepath.Join(dir, packLock)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("dropping the lock in %s: %w", dir, err)
		}
	}
	return nil
}
