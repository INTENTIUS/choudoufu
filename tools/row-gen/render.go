// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// reportHeader is printed once, before any service batch. It states the
// tool's non-goals in its own output, per the issue's acceptance criterion,
// so a reader of the report alone - not just this file's doc comment - sees
// the boundary.
const reportHeader = `row-gen: registry-evidence admission proposals (issue #44)

Turns live/registry.json's per-type evidence, joined against live/mapping.json,
into proposed rows for tools/row-gen/ratified.json, the hand-owned corpus
-emit copies every non-RecordBacked row out of. Nothing is written to that
file by this tool: every block below is printed for a human to paste, edit
and ratify, and -emit then renders the generated tables from it. A wrong row
touches live infrastructure, so the generator proposes and humans decide -
see issue #37.

Paste a block into tools/row-gen/ratified.json and re-run
"go run ./tools/row-gen -emit". There is no second paste: admittedTypesV0 in
internal/live/lint/admission_generated.go is DERIVED from the emitted table's
own key set, so admitting a type is exactly the act of giving it a row here
(issue #263).

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
	case bucketServerAssigned, bucketClientNamed, bucketComposite, bucketAssembled:
		for _, n := range ratifierChecks(p) {
			fmt.Fprintf(&b, "before ratifying: %s\n", n)
		}
		fmt.Fprintf(&b, "\n--- paste into %s (see contributing/LIVE-TABLES.md), then re-run -emit ---\n", ratifiedJSONRel)
		entry, err := renderRatifiedEntry(p)
		if err != nil {
			// Unreachable for any proposal classifyAll produces: the row is
			// built from proposal fields that are already strings and bools.
			// Reported rather than dropped, because a block silently missing
			// its paste is the one failure a reader cannot see.
			fmt.Fprintf(&b, "no pastable row: rendering it as JSON failed: %v\n", err)
			break
		}
		b.WriteString(entry)
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

// proposedRatifiedRow is one proposal as the ratified row it proposes: the
// exact [identity.TypeIdentity] a ratifier who accepts the printed block
// unedited would be adding to tools/row-gen/ratified.json.
//
// It is [proposedFields] plus the two fields that function deliberately does
// not return, because convergence.go does not compare them:
//
//   - Reason, on a server-assigned row. All 445 server-assigned rows in the
//     committed corpus carry one, and the fresh classifier must never
//     regenerate it (see emit.go's doc comment on why templated Reason prose
//     is copied rather than rebuilt), so the ratifier gets a templated
//     sentence to rewrite or accept.
//   - IdentityAttrs, on a client-named row, naming the resolved argument
//     itself. 274 of the corpus's 325 single-component rows carry one. The
//     "id" alias stays out: that pairing is the inference issue #44 keeps as
//     a human call.
//
// Composite and assembled rows get no IdentityAttrs for that same id-alias
// reason, which is also the corpus's own shape - 217 of its 248
// multi-component rows carry none.
//
// Building the block out of [proposedFields] rather than from a second
// reading of the proposal is what keeps the paste and the convergence
// measurement in agreement by construction: a row pasted unedited is a row
// compareOne reports as matched, because both sides are the same function.
func proposedRatifiedRow(p proposal) identity.TypeIdentity {
	serverAssigned, components, importSyntax, identityAttrs, _ := proposedFields(p)
	row := identity.TypeIdentity{
		Type:           p.TFType,
		ServerAssigned: serverAssigned,
		Components:     components,
		ImportSyntax:   importSyntax,
		IdentityAttrs:  identityAttrs,
	}
	switch p.Bucket {
	case bucketServerAssigned:
		row.Reason = fmt.Sprintf("the %s service assigns this identity at create time; no argument reconstructs it.", p.Service)
	case bucketClientNamed:
		row.IdentityAttrs = []string{p.ArgName}
	}
	return row
}

// renderRatifiedEntry is the pastable block: one type's member of
// tools/row-gen/ratified.json, in that file's own canonical spelling.
//
// It renders through [renderRatified] - the same function
// TestRatifiedJSONIsCanonical holds the committed file equal to - so a
// pasted block lands in the one spelling loadRatified round-trips rather
// than in a second one that merely parses. The outer object braces are
// stripped, leaving the `"type": { ... },` member at the indentation the
// file's other members already sit at.
//
// The trailing comma is deliberate: ratified.json has never been empty, so
// every real paste lands beside an existing member and needs one. It is what
// stops the block being standalone JSON, which is why
// TestPastableSnippetsLoadAsRatifiedRows re-wraps before parsing.
func renderRatifiedEntry(p proposal) (string, error) {
	out, err := renderRatified(map[string]identity.TypeIdentity{p.TFType: proposedRatifiedRow(p)})
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) < 3 || lines[0] != "{" || lines[len(lines)-1] != "}" {
		return "", fmt.Errorf("renderRatified produced an unexpected envelope for %s: %q", p.TFType, out)
	}
	return strings.Join(lines[1:len(lines)-1], "\n") + ",\n", nil
}

// ratifierChecks is the guidance that used to ride along as Go comments
// inside the pasted literal, back when the paste target was a Go file.
//
// JSON carries no comments, so it prints above the block instead. None of it
// is optional reading - each line names a field the generator filled in
// without the evidence to settle it - and dropping it during the move off Go
// source would have made the paste look more settled than it is.
func ratifierChecks(p proposal) []string {
	const idAlias = `"identity_attrs" is intentionally absent: whether this type's own "id" attribute equals the identity above is the id-alias inference row-gen does not make (issue #44 non-goals). Add "id" and any other alias only after confirming it against the provider schema or docs.`

	var out []string
	switch p.Bucket {
	case bucketServerAssigned:
		out = append(out, `"reason" is TEMPLATED - it names the CFN service and asserts server assignment, nothing more specific. Rewrite it, or accept it deliberately.`)
		if p.DerivedImportSyntax != "" {
			out = append(out, `"import_syntax" comes from import-grammar precedence: the registry's primaryIdentifier disagreed with the documented import example, and the documented shape won. Still verify it.`)
		} else {
			out = append(out, `"import_syntax" is TEMPLATED from the registry's primaryIdentifier name(s), not from the provider's documented import syntax. Verify it.`)
		}
		if len(p.DerivedIdentityAttrs) > 0 {
			out = append(out, `"identity_attrs" was recovered from the documented import example by import-grammar precedence. Still verify it.`)
		} else {
			out = append(out, idAlias)
		}
	case bucketClientNamed:
		out = append(out, `"identity_attrs" names the resolved argument only; "id" is intentionally omitted (issue #44 non-goals).`)
	case bucketComposite, bucketAssembled:
		out = append(out, `"identity_attrs" is intentionally absent: whether the composed value also equals an exported attribute is the id-alias inference issue #44 keeps as a human call - hand out nothing rather than something that happens to look right.`)
	}
	return out
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
