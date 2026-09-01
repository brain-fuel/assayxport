package diff

import (
	"fmt"
	"strings"
)

// Text renders a human-readable summary of a diff document. It is a view
// over the JSON contract, not a second contract: consumers parse the JSON.
func Text(doc Doc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ax diff (%s): %s vs %s\n", doc.Mode, doc.Sources[0].Label, doc.Sources[1].Label)
	for _, s := range doc.Sources {
		fmt.Fprintf(&b, "  %-9s %s [%s]\n", s.Kind+":", s.Label, extractionSummary(s))
	}
	if doc.Drift != nil {
		renderDriftText(&b, doc.Drift)
	}
	if doc.Correspond != nil {
		renderCorrespondText(&b, doc.Correspond)
	}
	return b.String()
}

func extractionSummary(s Source) string {
	if len(s.Languages) == 0 {
		return "no languages"
	}
	parts := make([]string, 0, len(s.Languages))
	for _, l := range s.Languages {
		parts = append(parts, l+" "+s.Extraction[l])
	}
	return strings.Join(parts, ", ")
}

func renderDriftText(b *strings.Builder, d *Drift) {
	s := d.Summary
	if !d.HasDifferences() {
		b.WriteString("no drift: the sources declare the same API\n")
		return
	}
	fmt.Fprintf(b, "packages: +%d -%d, %d changed; symbols: +%d -%d, %d changed\n",
		s.PackagesAdded, s.PackagesRemoved, len(d.Packages.Changed),
		s.SymbolsAdded, s.SymbolsRemoved, s.SymbolsChanged)
	for _, p := range d.Packages.Added {
		fmt.Fprintf(b, "  + package %s (%d symbols)\n", p.ID, p.SymbolCount)
	}
	for _, p := range d.Packages.Removed {
		fmt.Fprintf(b, "  - package %s (%d symbols)\n", p.ID, p.SymbolCount)
	}
	for _, p := range d.Packages.Changed {
		name := p.ID
		if p.RenamedFromID != "" {
			name = p.RenamedFromID + " -> " + p.ID
		}
		fmt.Fprintf(b, "  ~ package %s\n", name)
		for _, id := range p.Symbols.Added {
			fmt.Fprintf(b, "      + %s\n", id)
		}
		for _, id := range p.Symbols.Removed {
			fmt.Fprintf(b, "      - %s\n", id)
		}
		for _, c := range p.Symbols.Changed {
			fmt.Fprintf(b, "      ~ %s (%s)\n", c.ID, strings.Join(c.Changes, ", "))
			if c.Signature != nil {
				fmt.Fprintf(b, "          a: %s\n          b: %s\n", c.Signature.A, c.Signature.B)
			}
		}
	}
}

func renderCorrespondText(b *strings.Builder, c *Correspond) {
	fmt.Fprintf(b, "eligible symbols: %d vs %d\n", c.Eligible.A, c.Eligible.B)
	if len(c.Candidates) == 0 {
		b.WriteString("no candidate pairs met the threshold\n")
		return
	}
	fmt.Fprintf(b, "%d candidate pairs (combined signature/name/calls):\n", len(c.Candidates))
	for _, cand := range c.Candidates {
		calls := "-"
		if cand.Scores.Calls != nil {
			calls = fmt.Sprintf("%d", *cand.Scores.Calls)
		}
		tag := ""
		if !cand.SameLanguage {
			tag = "  [cross-language]"
		}
		fmt.Fprintf(b, "  %4d  %s  ~  %s  (sig %d, name %d, calls %s)%s\n",
			cand.Scores.Combined, cand.A.Ref, cand.B.Ref,
			cand.Scores.Signature, cand.Scores.Name, calls, tag)
	}
}
