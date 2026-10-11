package setup

import (
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Readings is every repository under review in its class, with the counts of each.
type Readings struct {
	Counts map[string]int `json:"counts"`
	// Declared counts repositories inside the declaration; Read those whose setup was read, and
	// OldestRead is the stalest of those readings.
	Declared   int       `json:"declared"`
	Read       int       `json:"read"`
	OldestRead string    `json:"oldestRead,omitempty"`
	Items      []Reading `json:"items"`
}

// Reading is one repository's class, with the sentence the classification gives for it.
type Reading struct {
	store.SetupClass
	// Writable is a class ABOM may write, once a preview listing it is approved.
	Writable bool `json:"writable"`
}

// Classify reads every repository's class, keeping only class when one is named. The counts are
// always of everything, so a narrowed list still says how big the whole is.
func Classify(s store.Backend, class string) (Readings, error) {
	classes, err := s.SetupClasses()
	if err != nil {
		return Readings{}, err
	}
	out := Readings{Counts: map[string]int{}, Items: []Reading{}}
	for _, c := range classes {
		out.Counts[c.Class]++
		if c.Class != store.SetupNotOwned {
			out.Declared++
		}
		if c.SetupReadAt != "" {
			out.Read++
			if out.OldestRead == "" || c.SetupReadAt < out.OldestRead {
				out.OldestRead = c.SetupReadAt
			}
		}
		if class != "" && c.Class != class {
			continue
		}
		out.Items = append(out.Items, Reading{SetupClass: c, Writable: Writable(c.Class)})
	}
	return out, nil
}

// Writable reports the only classes ABOM ever writes.
func Writable(class string) bool {
	return class == store.SetupAdd || class == store.SetupEnable
}
