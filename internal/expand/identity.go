package expand

import (
	"context"
	"slices"
)

// CurrentIdentity reports the CodeQL version and the set of pack digests the bound suites resolve
// to right now.
//
// It has to come from a fresh resolution rather than from the ledger. After a CodeQL upgrade the
// most recent recorded version is the old one, so selecting against the stored answer would
// report the corpus as current at exactly the moment all of it went stale.
func (e *Expander) CurrentIdentity(ctx context.Context) (string, []string, error) {
	plans, err := e.plansByTier(ctx, Options{})
	if err != nil {
		return "", nil, err
	}

	var cliVersion string
	var digests []string
	for _, p := range plans {
		if p.cliVersion != "" {
			cliVersion = p.cliVersion
		}
		for _, c := range p.contents {
			if c.PackDigest != "" {
				digests = append(digests, c.PackDigest)
			}
		}
	}
	slices.Sort(digests)
	return cliVersion, slices.Compact(digests), nil
}
