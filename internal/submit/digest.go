package submit

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// Digest identifies what a snapshot would publish, so a later run can tell whether the supply
// chain has moved.
//
// Over content, not over count. The ledger first recorded only how many packages were sent, and a
// version bump keeps that number identical while changing what every consumer is told — the same
// failure as reading an absent value as zero. Over sorted purls, not the order SQL returned them
// in, or every check reports a change.
//
// The commit and the scan time are deliberately excluded: they move on every run and say nothing
// about whether the dependencies did.
func Digest(snap Snapshot) string {
	var purls []string
	for _, m := range snap.Manifests {
		for _, r := range m.Resolved {
			purls = append(purls, r.PackageURL)
		}
	}
	sort.Strings(purls)

	// Prefixed so a release digests to a value of its own. Hashing nothing would make "we handed
	// the path back" and "we have never submitted" the same answer, and a repository whose
	// transitive dependencies disappeared would then never be released.
	sum := sha256.Sum256([]byte("abom-snapshot-v1\n" + strings.Join(purls, "\n")))
	return hex.EncodeToString(sum[:])
}
