package expand

import (
	"context"
	"strings"
)

// commitLength is the width of a git SHA-1 in hex.
const commitLength = 40

// commitWithoutFetching reports the commit a batch's reference points at, if that can be known
// without fetching the repository.
//
// Not knowing is reported as such rather than guessed. Both ways of being wrong here degrade to
// fetching, which is what the caller would have done anyway: a reference mistaken for a commit
// yields a key the ledger has never seen, and a commit mistaken for a reference costs one
// request. Answering "nothing outstanding" on a bad guess would instead settle a dependency
// nobody looked at, so failure is never that answer.
func (e *Expander) commitWithoutFetching(ctx context.Context, b batch) (string, bool) {
	// A reference that is already a commit cannot move. There is no resolution to compare
	// against yet, which is what Resolution.Mutable() uses, so syntax is the only signal here.
	if isCommit(b.ref) {
		return b.ref, true
	}
	if e.Probe == nil {
		return "", false
	}
	sha, err := e.Probe.Resolve(ctx, b.owner, b.repo, b.ref)
	if err != nil || sha == "" {
		return "", false
	}
	return sha, true
}

func isCommit(ref string) bool {
	if len(ref) != commitLength {
		return false
	}
	return strings.IndexFunc(ref, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f')
	}) < 0
}
