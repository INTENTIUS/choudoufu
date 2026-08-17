// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"sort"
	"strings"
)

// reportHeader is printed once, before any service batch. It states the
// tool's non-goals in its own output, per the issue's acceptance criterion,
// so a reader of the report alone - not just this file's doc comment - sees
// the boundary.
const reportHeader = `row-gen: registry-evidence admission proposals (issue #44)

Turns live/registry.json's per-type evidence, joined against live/mapping.json,
into proposed rows for internal/live/lint/admission.go (admittedTypesV0) and
internal/live/identity/table.go (DefaultTable). Nothing is written to either
file by this tool: every block below is printed for a human to paste, edit
and ratify. A wrong row touches live infrastructure, so the generator
proposes and humans decide - see issue #37.

Non-goals (also true of every block below, not just this header):
  - no IdentityAttrs id-alias inference: whether a type's own "id" attribute
    equals its import identity is not decided here (synthesize.go:47-52);
    a ratifier adds "id" to IdentityAttrs only after confirming it by hand.
  - no matchTable adoption rows: scope-uniqueness for content-match adoption
    is human judgment (internal/live/foreign/classify.go:62-67).
  - no composite separators: a type whose primaryIdentifier has more than
    one part lands in "needs hand separator" and is never pastable, because
    the join character (DefaultTable's are "/", ":", "_", ","; Cloud
    Control's own is "|") is in no schema this tool can read.

Every Reason string in a server-assigned block is flagged TEMPLATED: it names
the CFN service and asserts server assignment, nothing more specific.
Every client-named block whose argument name could only be GUESSED (a
snake-cased CFN property name, backed by neither a provider identity schema,
live/import-grammar.json, nor the carve seed) is evidence-only, not a
proposal - see the argument line in each such block.
`

// renderReport batches every proposal by CFN service, renders each batch in
// service-name order, and appends the summary counts. If service is
// non-empty, the report is restricted to that one batch - the mode
// tools/row-gen/testdata's golden file was captured with.
func renderReport(proposals []proposal, service string) string {
	byService := map[string][]proposal{}
	for _, p := range proposals {
		byService[p.Service] = append(byService[p.Service], p)
	}

	var services []string
	for svc := range byService {
		if service != "" && svc != service {
			continue
		}
		services = append(services, svc)
	}
	sort.Strings(services)

	var b strings.Builder
	b.WriteString(reportHeader)
	for _, svc := range services {
		ps := byService[svc]
		sort.Slice(ps, func(i, j int) bool { return ps[i].TFType < ps[j].TFType })
		b.WriteString("\n================================================================\n")
		fmt.Fprintf(&b, "service: %s (%d types)\n", svc, len(ps))
		b.WriteString("================================================================\n")
		for _, p := range ps {
			b.WriteString("\n")
			b.WriteString(renderProposal(p))
		}
	}

	if service == "" {
		b.WriteString("\n================================================================\n")
		b.WriteString(summaryCounts(proposals))
	}
	return b.String()
}

// renderProposal is one type's whole block: the header line, the evidence,
// and - for the two proposed buckets - the pastable snippets.
func renderProposal(p proposal) string {
	var b strings.Builder

	// target is the "-> X" the header shows: the CFN type for a mapped row,
	// and the honest "(no CFN model)" for a classifyUnmapped one, whose
	// CFNType is empty because CloudFormation models no such type - not
	// because anything failed to look it up.
	target := p.CFNType
	if p.NoCFNModel {
		target = serviceUnmodeled
	}

	switch p.Bucket {
	case bucketServerAssigned:
		fmt.Fprintf(&b, "## %s -> %s [proposed: server-assigned]\n", p.TFType, target)
	case bucketClientNamed:
		fmt.Fprintf(&b, "## %s -> %s [proposed: client-named]\n", p.TFType, target)
	case bucketNeedsHandSeparator:
		fmt.Fprintf(&b, "## %s -> %s [needs hand separator]\n", p.TFType, target)
	case bucketComposite:
		fmt.Fprintf(&b, "## %s -> %s [proposed: composite]\n", p.TFType, target)
	case bucketAssembled:
		fmt.Fprintf(&b, "## %s -> %s [proposed: assembled template]\n", p.TFType, target)
	case bucketFoldChild:
		fmt.Fprintf(&b, "## %s -> (property-child of %s) [fold-child: parent %s]\n", p.TFType, p.FoldParent, p.ParentTFType)
	case bucketEvidenceOnly:
		if p.FoldParent != "" {
			fmt.Fprintf(&b, "## %s -> (property-child of %s) [evidence-only]\n", p.TFType, p.FoldParent)
		} else {
			fmt.Fprintf(&b, "## %s -> %s [evidence-only]\n", p.TFType, target)
		}
	}

	fmt.Fprintf(&b, "rule: %s\n", p.Rule)
	if p.FoldParent == "" && !p.NoCFNModel {
		fmt.Fprintf(&b, "registry fields read: primary_identifier=%s read_only_properties=%s create_only_properties=%s\n",
			quoteList(p.PrimaryIdentifier), quoteList(p.ReadOnly), quoteList(p.CreateOnly))
		if len(p.ParentInputs) > 0 {
			fmt.Fprintf(&b, "enumeration: %s %s\n", p.Enumeration, quoteList(p.ParentInputs))
		} else {
			fmt.Fprintf(&b, "enumeration: %s\n", p.Enumeration)
		}
	}
	if p.Bucket == bucketClientNamed || (p.Bucket == bucketEvidenceOnly && p.ArgName != "" && p.FoldParent == "") {
		fmt.Fprintf(&b, "argument: %s (source: %s)\n", p.ArgName, p.ArgSource)
	}
	if p.Bucket == bucketComposite {
		fmt.Fprintf(&b, "components: %s joined by %q (source: %s)\n", quoteList(p.CompositeArgs), p.CompositeSep, p.ArgSource)
	}
	if p.Bucket == bucketAssembled {
		fmt.Fprintf(&b, "template: %s (source: %s)\n", describeSegments(p.Assembled), p.ArgSource)
	}
	for _, n := range p.Notes {
		fmt.Fprintf(&b, "note: %s\n", n)
	}

	switch p.Bucket {
	case bucketServerAssigned:
		b.WriteString("\n--- paste into internal/live/lint/admission_cohort_<cohort>.go (see contributing/LIVE-TABLES.md) ---\n")
		b.WriteString(renderAdmissionLine(p.TFType))
		b.WriteString("\n--- paste into internal/live/identity/table_cohort_<cohort>.go (see contributing/LIVE-TABLES.md) ---\n")
		b.WriteString(renderServerAssignedEntry(p))
	case bucketClientNamed:
		b.WriteString("\n--- paste into internal/live/lint/admission_cohort_<cohort>.go (see contributing/LIVE-TABLES.md) ---\n")
		b.WriteString(renderAdmissionLine(p.TFType))
		b.WriteString("\n--- paste into internal/live/identity/table_cohort_<cohort>.go (see contributing/LIVE-TABLES.md) ---\n")
		b.WriteString(renderClientNamedEntry(p))
	case bucketComposite:
		b.WriteString("\n--- paste into internal/live/lint/admission_cohort_<cohort>.go (see contributing/LIVE-TABLES.md) ---\n")
		b.WriteString(renderAdmissionLine(p.TFType))
		b.WriteString("\n--- paste into internal/live/identity/table_cohort_<cohort>.go (see contributing/LIVE-TABLES.md) ---\n")
		b.WriteString(renderCompositeEntry(p))
	case bucketAssembled:
		b.WriteString("\n--- paste into internal/live/lint/admission_cohort_<cohort>.go (see contributing/LIVE-TABLES.md) ---\n")
		b.WriteString(renderAdmissionLine(p.TFType))
		b.WriteString("\n--- paste into internal/live/identity/table_cohort_<cohort>.go (see contributing/LIVE-TABLES.md) ---\n")
		b.WriteString(renderAssembledEntry(p))
	case bucketNeedsHandSeparator:
		b.WriteString("no pastable row: the composite separator is not registry evidence; a human chooses it.\n")
	case bucketFoldChild:
		b.WriteString("no pastable row: the fold-child admission path exists (issue #68), but the child's own Components - the parent's tuple, plus any further argument (e.g. status_code) the parent alone does not supply - still need a human's separator and shape choice, the same standard internal/live/identity/table.go's \"Fold-children (issue #68)\" section comment holds every entry to.\n")
	case bucketEvidenceOnly:
		b.WriteString("no pastable row.\n")
	}
	return b.String()
}

// quoteList renders a string slice as a bracketed, quoted, comma-separated
// list for the evidence lines - ["A", "B"] rather than Go's default %v
// formatting ([A B]).
func quoteList(names []string) string {
	if len(names) == 0 {
		return "[]"
	}
	return "[" + quoteArgs(names) + "]"
}

// quoteArgs renders a string slice as bare, quoted, comma-separated Go
// expressions - "a", "b" rather than quoteList's bracketed ["a", "b"] -
// which is what a variadic call site like serverAssigned's identityAttrs
// parameter needs pasted directly, not a slice literal.
func quoteArgs(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(quoted, ", ")
}

// renderAdmissionLine is the ready-to-paste admittedTypesV0 map entry.
func renderAdmissionLine(tfType string) string {
	return fmt.Sprintf("%q: {},\n", tfType)
}

// renderServerAssignedEntry is the ready-to-paste serverAssigned(...) call
// for DefaultTable. importSyntax is, by default, a documentation-only
// best-effort string built off the registry's own primaryIdentifier names,
// flagged the same as the reason since neither is provider-documentation-
// verified - unless applyImportGrammarPrecedence set DerivedImportSyntax,
// in which case pinned import-grammar evidence already disagreed with the
// registry and won (see importprecedence.go's tryArnVsIDOverride,
// tryOpaqueOverride, tryCompoundArnImportSyntax and
// applyIdentitySchemaAttrsCorrection), and
// DerivedIdentityAttrs is pasted too - the one case row-gen does propose an
// IdentityAttrs value despite issue #44's general non-goal, because this
// specific correction (which exported attribute, not whether one exists) is
// exactly what the pinned evidence settles.
func renderServerAssignedEntry(p proposal) string {
	service := p.Service
	reason := fmt.Sprintf("the %s service assigns this identity at create time; no argument reconstructs it.", service)

	importSyntax := strings.ToUpper(strings.Join(p.PrimaryIdentifier, "-"))
	importSyntaxComment := "TEMPLATED: registry primaryIdentifier name(s), not the provider's documented import syntax; verify"
	if p.DerivedImportSyntax != "" {
		importSyntax = p.DerivedImportSyntax
		importSyntaxComment = "import-grammar precedence: registry primaryIdentifier disagreed with the documented import example; this is the documented shape - still verify"
	}

	if len(p.DerivedIdentityAttrs) > 0 {
		return fmt.Sprintf(`serverAssigned(%q,
	%q, // TEMPLATED: rewrite or accept this reason during ratification
	%q, // %s
	%s, // import-grammar precedence: identity attribute recovered from the documented example - still verify
),
`, p.TFType, reason, importSyntax, importSyntaxComment, quoteArgs(p.DerivedIdentityAttrs))
	}

	return fmt.Sprintf(`serverAssigned(%q,
	%q, // TEMPLATED: rewrite or accept this reason during ratification
	%q, // %s
	// IdentityAttrs intentionally omitted: whether this type's own "id"
	// attribute equals the identity above is the id-alias inference row-gen
	// does not make (issue #44 non-goals). Add "id" and any other alias
	// only after confirming it against the provider schema or docs.
),
`, p.TFType, reason, importSyntax, importSyntaxComment)
}

// renderCompositeEntry is the ready-to-paste multi-component TypeIdentity{}
// literal for DefaultTable: applyImportGrammarPrecedence's bucketComposite
// result, an attr()/sep() chain built from CompositeArgs and CompositeSep -
// the composite shape bucketNeedsHandSeparator could never propose because
// the separator was, until this pass, never registry evidence.
// IdentityAttrs is left nil: whether the composite's own concatenation also
// equals an exported attribute is the id-alias inference issue #44 keeps as
// a human call (see aws_networkmanager_customer_gateway_association's
// ratified entry, whose comment states the type exports no single attribute
// equal to its comma-joined id, "hand out nothing rather than something
// that happens to look right" - exactly the standard this leaves to the
// ratifier).
func renderCompositeEntry(p proposal) string {
	var comps strings.Builder
	var syn []string
	for i, arg := range p.CompositeArgs {
		if i > 0 {
			fmt.Fprintf(&comps, "\n\t\tsep(%q),", p.CompositeSep)
		}
		def, hasDef := p.ArgDefaults[arg]
		cloud, hasCloud := p.ArgCloud[arg]
		switch {
		case hasDef || hasCloud:
			// attr() cannot carry a fallback of either kind; the component
			// is spelled in full so the paste stays unedited (see
			// Component.Default and Component.Cloud).
			fmt.Fprintf(&comps, "\n\t\tComponent{Attrs: []string{%q}", arg)
			if hasDef {
				fmt.Fprintf(&comps, ", Default: %q", def)
			}
			if hasCloud {
				fmt.Fprintf(&comps, ", Cloud: %q", cloud)
			}
			comps.WriteString(", IdentityAttr: SameNameIdentity},")
		default:
			fmt.Fprintf(&comps, "\n\t\tattr(%q),", arg)
		}
		syn = append(syn, strings.ToUpper(arg))
	}
	importSyntax := strings.Join(syn, strings.ToUpper(p.CompositeSep))
	return fmt.Sprintf(`TypeIdentity{
	Type: %q,
	Components: []Component{%s
	},
	ImportSyntax:  %q,
	IdentityAttrs: nil, // id-alias inference intentionally left to the ratifier; see issue #44 non-goals
},
`, p.TFType, comps.String(), importSyntax)
}

// renderAssembledEntry is bucketAssembled's ready-to-paste TypeIdentity{}
// literal (issue #172): the template's segments spelled as the explicit
// Component list the ratified table uses for this shape - {Literal: ...},
// {Cloud: "region"}, {Attrs: []string{...}} - with the per-component
// IdentityAttr derived from the leading scheme literal (identityattr.go's
// rule). IdentityAttrs is left nil for the same issue #44 id-alias reason
// renderCompositeEntry states.
func renderAssembledEntry(p proposal) string {
	serverAssigned, components, importSyntax, _, _ := proposedFields(p)
	_ = serverAssigned
	var comps strings.Builder
	for i, c := range components {
		if i > 0 {
			comps.WriteString(", ")
		}
		switch {
		case c.Cloud != "":
			fmt.Fprintf(&comps, "{Cloud: %q, IdentityAttr: %q}", string(c.Cloud), c.IdentityAttr)
		case len(c.Attrs) > 0:
			fmt.Fprintf(&comps, "{Attrs: []string{%s}, IdentityAttr: %q}", quoteArgs(c.Attrs), c.IdentityAttr)
		default:
			fmt.Fprintf(&comps, "{Literal: %q, IdentityAttr: %q}", c.Literal, c.IdentityAttr)
		}
	}
	return fmt.Sprintf(`TypeIdentity{
	Type:          %q,
	Components:    []Component{%s},
	ImportSyntax:  %q,
	IdentityAttrs: nil, // id-alias inference intentionally left to the ratifier; see issue #44 non-goals
},
`, p.TFType, comps.String(), importSyntax)
}

// renderClientNamedEntry is the ready-to-paste TypeIdentity{...} literal for
// DefaultTable. IdentityAttrs names only the resolved argument itself, for
// the same id-alias reason renderServerAssignedEntry's comment states.
//
// attr() carries neither kind of documented fallback, so a proposal whose
// argument has one is spelled in full - the same choice renderCompositeEntry
// makes per segment, and for the same reason: the paste has to stay
// unedited. Eight proposals need it, every one a per-account or per-region
// singleton whose configuration ordinarily omits the argument entirely; see
// setClientNamedEvidence for the list and the measurement.
func renderClientNamedEntry(p proposal) string {
	importSyntax := strings.ToUpper(p.ArgName)
	def, hasDef := p.ArgDefaults[p.ArgName]
	cloud, hasCloud := p.ArgCloud[p.ArgName]
	component := fmt.Sprintf("attr(%q)", p.ArgName)
	if hasDef || hasCloud {
		// Elided element type, matching renderAssembledEntry and the
		// ratified table's own single-line spelling; an explicit
		// Component{...} inside []Component{...} is what gofmt -s removes.
		var b strings.Builder
		fmt.Fprintf(&b, "{Attrs: []string{%q}", p.ArgName)
		if hasDef {
			fmt.Fprintf(&b, ", Default: %q", def)
		}
		if hasCloud {
			fmt.Fprintf(&b, ", Cloud: %q", cloud)
		}
		b.WriteString(", IdentityAttr: SameNameIdentity}")
		component = b.String()
	}
	return fmt.Sprintf(`TypeIdentity{
	Type:          %q,
	Components:    []Component{%s},
	ImportSyntax:  %q,
	IdentityAttrs: []string{%q}, // "id" intentionally omitted; see issue #44 non-goals
},
`, p.TFType, component, importSyntax, p.ArgName)
}

// summaryCounts is the acceptance criterion's headline: the bucket totals
// over every type live/mapping.json names - which, since loadMapping stopped
// filtering by via, is the provider's whole type roster and not only the
// CFN-modelled part of it.
func summaryCounts(proposals []proposal) string {
	counts := tally(proposals)
	return fmt.Sprintf(
		"summary (%d types)\n  proposed server-assigned:  %d\n  proposed client-named:     %d\n  proposed composite:        %d\n  proposed assembled (#172): %d\n  needs hand separator:      %d\n  fold-child (issue #68):    %d\n  evidence-only:             %d\n",
		len(proposals), counts.ServerAssigned, counts.ClientNamed, counts.Composite, counts.Assembled, counts.NeedsHandSeparator, counts.FoldChild, counts.EvidenceOnly)
}

// summary is the acceptance counts, shared by main.go's stderr line and the
// tests so neither has to re-derive the switch below.
type summary struct {
	ServerAssigned     int
	ClientNamed        int
	Composite          int
	Assembled          int
	NeedsHandSeparator int
	FoldChild          int
	EvidenceOnly       int
}

func tally(proposals []proposal) summary {
	var s summary
	for _, p := range proposals {
		switch p.Bucket {
		case bucketServerAssigned:
			s.ServerAssigned++
		case bucketClientNamed:
			s.ClientNamed++
		case bucketComposite:
			s.Composite++
		case bucketAssembled:
			s.Assembled++
		case bucketNeedsHandSeparator:
			s.NeedsHandSeparator++
		case bucketFoldChild:
			s.FoldChild++
		case bucketEvidenceOnly:
			s.EvidenceOnly++
		}
	}
	return s
}
