// Package failure classes a failed read of a repository by what the remote said, because Git exits
// 128 for a resolver outage and for a deleted repository alike.
package failure

import (
	"regexp"
	"strings"
)

// Class is why a read failed, and so whether reading again could change the answer.
type Class string

const (
	// Transient is anything a later read could answer: the network, the host, a limit, a deadline,
	// and everything not recognized below.
	Transient Class = "transient"
	// Access is the remote refusing this credential, or not showing it the repository. Only a change
	// of access fixes it.
	Access Class = "access"
	// Absent is the remote answering and the reference not being among its refs.
	Absent Class = "absent"
)

// Terminal reports whether the answer would repeat until something outside ABOM changes.
func (c Class) Terminal() bool { return c == Access || c == Absent }

var httpStatus = regexp.MustCompile(`returned error: (\d{3})`)

// Lower-cased phrases. A remote that names a branch says it was not found; a fetch by commit says
// the commit is not one of its refs.
var (
	absent = []string{"not found in upstream origin", "not our ref", "couldn't find remote ref",
		"reference missing"}
	access = []string{"could not read username", "could not read password", "authentication failed",
		"invalid username or password", "repository not found", "does not exist", "permission denied",
		"app is not installed", "does not appear to be a git repository"}
)

// Classify reads Git's or GitHub's own words. A failure that fits no rule is transient: a wrong
// retry costs a clone, and a wrong terminal answer costs coverage nobody notices losing.
func Classify(text string) Class {
	lower := strings.ToLower(text)
	if m := httpStatus.FindStringSubmatch(lower); m != nil {
		switch m[1] {
		case "401", "403", "404":
			return Access
		default:
			return Transient
		}
	}
	for _, phrase := range absent {
		if strings.Contains(lower, phrase) {
			return Absent
		}
	}
	for _, phrase := range access {
		if strings.Contains(lower, phrase) {
			return Access
		}
	}
	return Transient
}
