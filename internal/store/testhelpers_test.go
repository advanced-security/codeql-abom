package store

// gapped reports whether a repository was named as a gap.
func (d DatabaseSet) gapped(repository string) bool {
	for _, g := range d.Gaps {
		if g.Repository == repository {
			return true
		}
	}
	return false
}

func (c SeverityCounts) total() int { return c.Critical + c.High + c.Medium + c.Low + c.Unknown }
