package store

// BOMSummary is what a document claims about itself, counted over the components something
// references: the same denominator as BOMCoverage.ComponentsInUse.
type BOMSummary struct {
	Roots       []string    `json:"roots"`
	GeneratedAt string      `json:"generatedAt"`
	Coverage    BOMCoverage `json:"coverage"`
	Direct      int         `json:"direct"`
	Indirect    int         `json:"indirect"`
	// DeepestHop is the farthest a component sits from a root; zero when nothing is in use.
	DeepestHop int `json:"deepestHop"`
	// WithFindings, NoneFound and NotExamined partition the components in use. Not examined is
	// not none found: nothing looked, so the document says nothing about what it contains.
	WithFindings int `json:"withFindings"`
	NoneFound    int `json:"noneFound"`
	NotExamined  int `json:"notExamined"`
}

// SummarizeBOM counts a document's claims from the document itself.
func SummarizeBOM(doc BOM) BOMSummary {
	out := BOMSummary{Roots: doc.Roots, GeneratedAt: doc.GeneratedAt, Coverage: doc.Coverage}
	for _, c := range doc.Components {
		if c.RefsInUse == 0 {
			continue
		}
		if c.Direct {
			out.Direct++
		} else {
			out.Indirect++
		}
		out.DeepestHop = max(out.DeepestHop, c.Depth)
		switch {
		case !c.Analyzed:
			out.NotExamined++
		case len(c.Findings) > 0:
			out.WithFindings++
		default:
			out.NoneFound++
		}
	}
	return out
}
