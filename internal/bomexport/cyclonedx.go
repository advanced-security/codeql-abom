package bomexport

import (
	"bytes"
	"fmt"
	"strings"

	cdx "github.com/CycloneDX/cyclonedx-go"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// CycloneDX writes the document as CycloneDX 1.6.
//
// Coverage and provenance have no first-class home in the format, so they are carried as `abom:`
// properties rather than dropped. A format's limits are not a license to omit the measurement:
// silently emitting an unexamined component beside an examined one is the failure the coverage
// statement exists to prevent.
func CycloneDX(doc store.BOM) ([]byte, error) {
	bom := cdx.NewBOM()
	bom.SerialNumber = ""
	bom.Metadata = &cdx.Metadata{
		Timestamp:  doc.GeneratedAt,
		Properties: cycloneProperties(coverageProperties(doc)),
		Tools: &cdx.ToolsChoice{Components: &[]cdx.Component{{
			Type: cdx.ComponentTypeApplication, Name: "codeql-abom",
		}}},
	}

	components := make([]cdx.Component, 0, len(doc.Components))
	dependencies := make([]cdx.Dependency, 0, len(doc.Components))
	vulnerabilities := []cdx.Vulnerability{}

	for _, c := range doc.Components {
		components = append(components, cycloneComponent(c))
		if v := cycloneVulnerabilities(c); len(v) > 0 {
			vulnerabilities = append(vulnerabilities, v...)
		}
	}

	// One entry per component, in the document's own order, so the graph is reproducible.
	targets := map[string][]string{}
	for _, r := range doc.Relationships {
		targets[r.Source] = append(targets[r.Source], r.Target)
	}
	for _, c := range doc.Components {
		d := cdx.Dependency{Ref: c.Key}
		if to := targets[c.Key]; len(to) > 0 {
			refs := append([]string{}, to...)
			d.Dependencies = &refs
		}
		dependencies = append(dependencies, d)
	}

	bom.Components = &components
	bom.Dependencies = &dependencies
	if len(vulnerabilities) > 0 {
		bom.Vulnerabilities = &vulnerabilities
	}

	var buf bytes.Buffer
	enc := cdx.NewBOMEncoder(&buf, cdx.BOMFileFormatJSON).SetPretty(true)
	if err := enc.EncodeVersion(bom, cdx.SpecVersion1_6); err != nil {
		return nil, fmt.Errorf("encoding CycloneDX: %w", err)
	}
	return buf.Bytes(), nil
}

func cycloneComponent(c store.BOMComponent) cdx.Component {
	out := cdx.Component{
		BOMRef:  c.Key,
		Type:    cdx.ComponentTypeLibrary,
		Name:    c.Key,
		Version: cycloneVersion(c),
	}
	props := [][2]string{
		{"abom:kind", c.Kind},
		{"abom:analyzed", fmt.Sprintf("%t", c.Analyzed)},
		{"abom:direct", fmt.Sprintf("%t", c.Direct)},
		{"abom:depth", fmt.Sprintf("%d", c.Depth)},
		{"abom:referencesInUse", fmt.Sprintf("%d", c.RefsInUse)},
		{"abom:referencesAnalyzed", fmt.Sprintf("%d", c.RefsAnalyzed)},
	}
	// Every reference as written, because the version field can hold only one and which one it
	// holds is a choice this document should not make silently.
	for _, r := range c.References {
		if r.Raw != "" {
			props = append(props, [2]string{"abom:reference", r.Raw})
		}
		if r.ResolvedSHA != "" {
			props = append(props, [2]string{"abom:resolved", r.Raw + "=> " + r.ResolvedSHA})
		}
		// What runs for the repositories this document is about, beside what they wrote.
		for _, l := range r.Locked {
			props = append(props, [2]string{"abom:locked", r.Raw + " locked=> " + l.Commit +
				" (" + strings.Join(l.Consumers, ", ") + ")"})
		}
	}
	for _, f := range c.Findings {
		if f.RunKey != "" {
			props = append(props, [2]string{"abom:runKey", f.RunKey})
		}
	}
	out.Properties = cycloneProperties(props)
	if c.Repository != "" {
		out.ExternalReferences = &[]cdx.ExternalReference{{
			Type: cdx.ERTypeVCS, URL: "https://github.com/" + c.Repository,
		}}
	}
	return out
}

// cycloneVersion is the single version the format allows.
//
// Only when a component is referenced exactly one way. Several references are several versions in
// use at once, and picking one to put here would state something no workflow says.
func cycloneVersion(c store.BOMComponent) string {
	if len(c.References) == 1 {
		return c.References[0].Value
	}
	return ""
}

// cycloneVulnerabilities reports advisories beside the component, never applied to a reference.
//
// `Affects` names the component, not a version range: whether an observed reference falls inside
// an advisory's range is a judgment ABOM has not made, and encoding one here would smuggle it in.
func cycloneVulnerabilities(c store.BOMComponent) []cdx.Vulnerability {
	out := []cdx.Vulnerability{}
	for _, a := range c.Advisories {
		v := cdx.Vulnerability{
			ID:          a.GHSAID,
			Description: a.Summary,
			Affects:     &[]cdx.Affects{{Ref: c.Key}},
		}
		if a.VulnerableRange != "" {
			v.Detail = "vulnerable range as published: " + a.VulnerableRange
		}
		out = append(out, v)
	}
	return out
}

func coverageProperties(doc store.BOM) [][2]string {
	cov := doc.Coverage
	return [][2]string{
		{"abom:roots", strings.Join(doc.Roots, ",")},
		{"abom:componentsInUse", fmt.Sprintf("%d", cov.ComponentsInUse)},
		{"abom:componentsAnalyzed", fmt.Sprintf("%d", cov.ComponentsAnalyzed)},
		{"abom:referencesInUse", fmt.Sprintf("%d", cov.ReferencesInUse)},
		{"abom:referencesAnalyzed", fmt.Sprintf("%d", cov.ReferencesAnalyzed)},
		{"abom:findings", fmt.Sprintf("%d", cov.Findings)},
		{"abom:findingsUntraced", fmt.Sprintf("%d", cov.FindingsUntraced)},
		{"abom:findingsWithoutResultHash", fmt.Sprintf("%d", cov.FindingsWithoutHash)},
		{"abom:findingsWithoutPackDigest", fmt.Sprintf("%d", cov.FindingsWithoutDigest)},
	}
}

func cycloneProperties(pairs [][2]string) *[]cdx.Property {
	out := make([]cdx.Property, 0, len(pairs))
	for _, p := range pairs {
		if p[1] == "" {
			continue
		}
		out = append(out, cdx.Property{Name: p[0], Value: p[1]})
	}
	if len(out) == 0 {
		return nil
	}
	return &out
}
