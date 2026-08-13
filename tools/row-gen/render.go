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
snake-cased CFN property name, backed by neither a provider identity schema
nor the carve seed) is evidence-only, not a proposal - see the argument
line in each such block.
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

	switch p.Bucket {
	case bucketServerAssigned:
		fmt.Fprintf(&b, "## %s -> %s [proposed: server-assigned]\n", p.TFType, p.CFNType)
	case bucketClientNamed:
		fmt.Fprintf(&b, "## %s -> %s [proposed: client-named]\n", p.TFType, p.CFNType)
	case bucketNeedsHandSeparator:
		fmt.Fprintf(&b, "## %s -> %s [needs hand separator]\n", p.TFType, p.CFNType)
	case bucketEvidenceOnly:
		if p.FoldParent != "" {
			fmt.Fprintf(&b, "## %s -> (property-child of %s) [evidence-only]\n", p.TFType, p.FoldParent)
		} else {
			fmt.Fprintf(&b, "## %s -> %s [evidence-only]\n", p.TFType, p.CFNType)
		}
	}

	fmt.Fprintf(&b, "rule: %s\n", p.Rule)
	if p.FoldParent == "" {
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
	for _, n := range p.Notes {
		fmt.Fprintf(&b, "note: %s\n", n)
	}

	switch p.Bucket {
	case bucketServerAssigned:
		b.WriteString("\n--- paste into internal/live/lint/admission.go (admittedTypesV0) ---\n")
		b.WriteString(renderAdmissionLine(p.TFType))
		b.WriteString("\n--- paste into internal/live/identity/table.go (DefaultTable) ---\n")
		b.WriteString(renderServerAssignedEntry(p))
	case bucketClientNamed:
		b.WriteString("\n--- paste into internal/live/lint/admission.go (admittedTypesV0) ---\n")
		b.WriteString(renderAdmissionLine(p.TFType))
		b.WriteString("\n--- paste into internal/live/identity/table.go (DefaultTable) ---\n")
		b.WriteString(renderClientNamedEntry(p))
	case bucketNeedsHandSeparator:
		b.WriteString("no pastable row: the composite separator is not registry evidence; a human chooses it.\n")
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
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// renderAdmissionLine is the ready-to-paste admittedTypesV0 map entry.
func renderAdmissionLine(tfType string) string {
	return fmt.Sprintf("%q: {},\n", tfType)
}

// renderServerAssignedEntry is the ready-to-paste serverAssigned(...) call
// for DefaultTable. importSyntax is a documentation-only best-effort string
// built off the registry's own primaryIdentifier names, flagged the same as
// the reason since neither is provider-documentation-verified.
func renderServerAssignedEntry(p proposal) string {
	service := p.Service
	reason := fmt.Sprintf("the %s service assigns this identity at create time; no argument reconstructs it.", service)
	importSyntax := strings.ToUpper(strings.Join(p.PrimaryIdentifier, "-"))
	return fmt.Sprintf(`serverAssigned(%q,
	%q, // TEMPLATED: rewrite or accept this reason during ratification
	%q, // TEMPLATED: registry primaryIdentifier name(s), not the provider's documented import syntax; verify
	// IdentityAttrs intentionally omitted: whether this type's own "id"
	// attribute equals the identity above is the id-alias inference row-gen
	// does not make (issue #44 non-goals). Add "id" and any other alias
	// only after confirming it against the provider schema or docs.
),
`, p.TFType, reason, importSyntax)
}

// renderClientNamedEntry is the ready-to-paste TypeIdentity{...} literal for
// DefaultTable. IdentityAttrs names only the resolved argument itself, for
// the same id-alias reason renderServerAssignedEntry's comment states.
func renderClientNamedEntry(p proposal) string {
	importSyntax := strings.ToUpper(p.ArgName)
	return fmt.Sprintf(`TypeIdentity{
	Type:          %q,
	Components:    []Component{attr(%q)},
	ImportSyntax:  %q,
	IdentityAttrs: []string{%q}, // "id" intentionally omitted; see issue #44 non-goals
},
`, p.TFType, p.ArgName, importSyntax, p.ArgName)
}

// summaryCounts is the acceptance criterion's headline: the four bucket
// totals over the whole mapped set.
func summaryCounts(proposals []proposal) string {
	counts := tally(proposals)
	return fmt.Sprintf(
		"summary (mapped set: %d types)\n  proposed server-assigned:  %d\n  proposed client-named:     %d\n  needs hand separator:      %d\n  evidence-only:             %d\n",
		len(proposals), counts.ServerAssigned, counts.ClientNamed, counts.NeedsHandSeparator, counts.EvidenceOnly)
}

// summary is the four acceptance counts, shared by main.go's stderr line
// and the tests so neither has to re-derive the switch below.
type summary struct {
	ServerAssigned     int
	ClientNamed        int
	NeedsHandSeparator int
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
		case bucketNeedsHandSeparator:
			s.NeedsHandSeparator++
		case bucketEvidenceOnly:
			s.EvidenceOnly++
		}
	}
	return s
}
