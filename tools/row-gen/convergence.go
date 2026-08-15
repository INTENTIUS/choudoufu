// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is rowgen-convergence's measurement half: for every type
// internal/live/identity.DefaultTable already admits, it regenerates
// row-gen's own fresh proposal (the same classifyAll pipeline -service
// output uses) and diffs it against the ratified entry on the four fields
// the ratification batches actually correct - ServerAssigned, Components,
// ImportSyntax and IdentityAttrs - so the gap between what row-gen proposes
// today and what a human batch ends up writing down is a number, not a
// batch report's prose.
//
// "Diff" is deliberately not "any byte differs." ImportSyntax is, by
// TypeIdentity's own doc comment, "documentation only: Components is what
// the code follows" - so a wording difference there is recorded but never
// counted as a genuine mismatch on its own. IdentityAttrs is issue #44's
// own stated non-goal for every bucket except the narrow ARN-vs-id
// correction importprecedence.go's rule 4 makes (id-alias inference is a
// human call the tool declines to guess at) - so a proposal that claims no
// IdentityAttrs at all is not "wrong" about them, it simply made no claim,
// and only a proposal that DID claim a value and got it wrong counts. What
// remains after both softenings - ServerAssigned disagreeing, Components
// disagreeing, or a claimed IdentityAttrs value disagreeing - is what this
// file calls a genuine mismatch, and it is what the ratchet test in
// convergence_test.go gates on.

// convergenceRow is one admitted type's comparison result.
type convergenceRow struct {
	TFType  string `json:"tf_type"`
	Service string `json:"service"`

	ProposedBucket string `json:"proposed_bucket"`
	ProposedRule   string `json:"proposed_rule"`

	// Genuine field-level disagreements: ServerAssigned, Components and a
	// claimed IdentityAttrs value. Empty means row-gen's fresh proposal is
	// functionally identical to the ratified entry.
	MismatchClasses []string `json:"mismatch_classes,omitempty"`

	// Soft/cosmetic disagreements, recorded for the four-field diff the
	// task asks for but never gated on: ImportSyntax wording, and
	// IdentityAttrs when row-gen simply proposed none at all (issue #44's
	// declared non-goal, not a wrong guess).
	CosmeticNotes []string `json:"cosmetic_notes,omitempty"`

	// ScrapeGap is set when a mismatch's likely cause is
	// live/import-grammar.json's own scrape missing evidence a fuller read
	// of the provider's doc page (Argument Reference, an Identity Schema
	// block) would have supplied - the class the SageMaker batch's report
	// named: import-grammar.json was consulted and came up short, not a
	// human ruling. See convergence_test.go's own doc comment for why this
	// is surfaced rather than folded into annotations.
	ScrapeGap bool `json:"scrape_gap,omitempty"`

	Matched    bool   `json:"matched"`
	Annotated  bool   `json:"annotated"`
	Annotation string `json:"annotation_reason,omitempty"`
}

// convergenceSummary is the headline counts the tool's report output and
// the ratchet test both read.
type convergenceSummary struct {
	AdmittedTotal         int     `json:"admitted_total"`
	Compared              int     `json:"compared"`
	NotInMappedSet        int     `json:"not_in_mapped_set"`
	AdoptedUnchanged      int     `json:"adopted_unchanged"`
	AdoptedUnchangedPct   float64 `json:"adopted_unchanged_pct"`
	GenuineMismatches     int     `json:"genuine_mismatches"`
	Annotated             int     `json:"annotated"`
	UnannotatedMismatches int     `json:"unannotated_mismatches"`
	ScrapeGapMismatches   int     `json:"scrape_gap_mismatches"`
}

// serviceSummary is convergenceSummary's per-CFN-service breakdown - the
// closest mechanical proxy this repo has for "per batch": ratification
// batches are themselves scoped and named by CFN service (table.go's own
// section comments: "Cohort estate: live/e2e/estates/data-movement",
// "VPC Lattice, the corrected majority", ...), and row-gen's own report
// already batches every proposal by CFN service (renderReport), so this
// reuses that grouping rather than hand-building a batch roster nothing in
// the pinned sources carries.
type serviceSummary struct {
	Compared            int     `json:"compared"`
	AdoptedUnchanged    int     `json:"adopted_unchanged"`
	AdoptedUnchangedPct float64 `json:"adopted_unchanged_pct"`
}

// convergenceArtifact is live/rowgen-convergence.json's whole shape.
type convergenceArtifact struct {
	GeneratedBy string                    `json:"generated_by"`
	Summary     convergenceSummary        `json:"summary"`
	ByService   map[string]serviceSummary `json:"by_service"`
	Types       []convergenceRow          `json:"types"`
}

// buildConvergence runs the comparison over every type identity.DefaultTable
// admits, using proposals (a fresh classifyAll run) and annotations (the
// per-type rulings tools/row-gen/annotations.json records).
func buildConvergence(proposals []proposal, annotations map[string]annotation) convergenceArtifact {
	byType := indexByType(proposals)

	admitted := make([]string, 0, len(identity.DefaultTable))
	for t := range identity.DefaultTable {
		admitted = append(admitted, t)
	}
	sort.Strings(admitted)

	art := convergenceArtifact{
		GeneratedBy: "tools/row-gen -convergence (go run ./tools/row-gen -convergence)",
		ByService:   map[string]serviceSummary{},
	}
	art.Summary.AdmittedTotal = len(admitted)

	svcCompared := map[string]int{}
	svcMatched := map[string]int{}

	for _, tf := range admitted {
		ratified := identity.DefaultTable[tf]
		p, ok := byType[tf]
		if !ok {
			art.Summary.NotInMappedSet++
			continue
		}
		art.Summary.Compared++

		row := compareOne(p, ratified)
		if ann, ok := annotations[tf]; ok {
			row.Annotated = true
			row.Annotation = ann.Reason
		}
		art.Types = append(art.Types, row)

		svcCompared[row.Service]++
		if row.Matched {
			art.Summary.AdoptedUnchanged++
			svcMatched[row.Service]++
		} else {
			art.Summary.GenuineMismatches++
			if row.Annotated {
				art.Summary.Annotated++
			} else {
				art.Summary.UnannotatedMismatches++
			}
			if row.ScrapeGap {
				art.Summary.ScrapeGapMismatches++
			}
		}
	}

	if art.Summary.Compared > 0 {
		art.Summary.AdoptedUnchangedPct = round2(100 * float64(art.Summary.AdoptedUnchanged) / float64(art.Summary.Compared))
	}
	for svc, n := range svcCompared {
		s := serviceSummary{Compared: n, AdoptedUnchanged: svcMatched[svc]}
		if n > 0 {
			s.AdoptedUnchangedPct = round2(100 * float64(svcMatched[svc]) / float64(n))
		}
		art.ByService[svc] = s
	}

	return art
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

// compareOne is the per-type field comparison: builds row-gen's fresh
// proposal's claim on the four fields (proposedFields), the ratified
// entry's own values, and classifies the disagreement.
func compareOne(p proposal, ratified identity.TypeIdentity) convergenceRow {
	row := convergenceRow{
		TFType:         p.TFType,
		Service:        p.Service,
		ProposedBucket: string(p.Bucket),
		ProposedRule:   p.Rule,
	}

	serverAssigned, components, importSyntax, identityAttrs, claimedAttrs := proposedFields(p)

	if serverAssigned != ratified.ServerAssigned {
		row.MismatchClasses = append(row.MismatchClasses, "server-assigned")
	}
	if !componentsEqual(components, ratified.Components) {
		row.MismatchClasses = append(row.MismatchClasses, "components")
	}
	if claimedAttrs {
		if !reflect.DeepEqual(identityAttrs, ratified.IdentityAttrs) {
			row.MismatchClasses = append(row.MismatchClasses, "identity-attrs")
		}
	} else if len(ratified.IdentityAttrs) > 0 {
		row.CosmeticNotes = append(row.CosmeticNotes, "identity-attrs-not-proposed (issue #44 non-goal)")
	}
	if importSyntax != ratified.ImportSyntax {
		row.CosmeticNotes = append(row.CosmeticNotes, "import-syntax-wording")
	}

	row.Matched = len(row.MismatchClasses) == 0

	if !row.Matched {
		row.ScrapeGap = isScrapeGap(p, row.MismatchClasses, ratified)
	}

	return row
}

// isScrapeGap flags the mismatch classes the SageMaker ratification batch's
// own report named: a case live/import-grammar.json's Import-section-only
// scrape was already consulted for and came up short of what the
// provider's fuller doc page (an Argument Reference section, a Terraform
// 1.12+ Identity Schema block) would have settled - not a maintainer's own
// scoping judgment the way keeping aws_elasticsearch_domain separate from
// aws_opensearch_domain is. Two shapes:
//
//   - A GUESSED client-named/composite proposal (classify.go's own "argument
//     name GUESSED" note, or applyImportGrammarDemotions' "argument-composed
//     ID" note) that the ratified entry confirms was right all along - row-
//     gen already had the correct answer but not the confidence to propose
//     it, which is exactly an argument-reference-detection gap, not a
//     judgment call.
//   - A "server-assigned" mismatch: the registry's readOnlyProperties claim
//     was wrong not just about which attribute (importprecedence.go's rules
//     2 and 4 already fix that shape) but about server assignment itself -
//     the ratified entry is client-named or composite instead. That can
//     only be caught by reading further into the provider's own Argument
//     Reference than live/import-grammar.json's Import-section scrape goes
//     (aws_vpc_dhcp_options_association's grammar row, for one, has no
//     composed_of_arguments detection at all even though the type client-
//     names by a plain vpc_id).
//   - A server-assigned proposal whose ONLY disagreement is IdentityAttrs,
//     AND whose ratified entry actually claims some: rules 2 and 4 already
//     resolve the ARN-vs-short-id shape, but a type like
//     aws_batch_compute_environment has both a legacy Import section (a
//     plain name-based example, which is where live/import-grammar.json's
//     own scrape reads ImportIDExample from) and a newer Terraform 1.12+
//     Identity Schema block naming "arn" as the real required attribute.
//
// The qualifier on that last bullet is issue #132's correction, and it
// matters at scale. A scrape gap means "fuller extraction would have
// produced the ratified answer". When the RATIFIED entry claims no identity
// attributes at all - table.go's own "no attribute of this type is usable as
// an identity source", the honest answer for a type whose id is a
// provider-synthesized value distinct from the import ID - no amount of
// further extraction gets there, because the proposal's fault is claiming
// too much rather than knowing too little. Those are ratification judgments
// and belong in the annotation ledger, not in a count of extractor debt.
//
// Measured when the qualifier went in: 33 of the 36 identity-attrs-only
// mismatches had an empty ratified IdentityAttrs, and 13 of them had a
// scraped identity_schema_required that the ratified entry still declined to
// follow. The evidence was there and the judgment went the other way.
func isScrapeGap(p proposal, mismatchClasses []string, ratified identity.TypeIdentity) bool {
	if len(mismatchClasses) == 1 && mismatchClasses[0] == "identity-attrs" && p.Bucket == bucketServerAssigned {
		// Only when the ratified entry claims something a fuller scrape
		// could have found. A ratified empty is a decision, not a gap.
		return len(ratified.IdentityAttrs) > 0
	}
	for _, c := range mismatchClasses {
		if c == "server-assigned" {
			return true
		}
	}
	if p.Bucket != bucketEvidenceOnly && p.Bucket != bucketNeedsHandSeparator {
		return false
	}
	for _, n := range p.Notes {
		if strings.Contains(n, "GUESSED") || strings.Contains(n, "argument-composed ID") {
			return true
		}
	}
	return false
}

// proposedFields turns a proposal into the same four-field shape a ratified
// identity.TypeIdentity carries, plus whether it actually claimed an
// IdentityAttrs value (as opposed to simply never attempting one - see this
// file's own doc comment on why that distinction matters).
func proposedFields(p proposal) (serverAssigned bool, components []identity.Component, importSyntax string, identityAttrs []string, claimedAttrs bool) {
	switch p.Bucket {
	case bucketServerAssigned:
		serverAssigned = true
		if p.DerivedImportSyntax != "" {
			importSyntax = p.DerivedImportSyntax
		} else {
			importSyntax = strings.ToUpper(strings.Join(p.PrimaryIdentifier, "-"))
		}
		if len(p.DerivedIdentityAttrs) > 0 {
			identityAttrs = p.DerivedIdentityAttrs
			claimedAttrs = true
		}
	case bucketClientNamed:
		components = []identity.Component{{Attrs: []string{p.ArgName}, IdentityAttr: identity.SameNameIdentity}}
		importSyntax = strings.ToUpper(p.ArgName)
	case bucketComposite:
		for i, arg := range p.CompositeArgs {
			if i > 0 {
				components = append(components, identity.Component{Literal: p.CompositeSep})
			}
			components = append(components, identity.Component{Attrs: []string{arg}, IdentityAttr: identity.SameNameIdentity})
		}
		// #106 criterion 3: an assembled row whose leading literal names its
		// own scheme carries the derived IdentityAttr on every component. A
		// separator-joined composite never has a leading literal, so this is
		// inert for every proposal shape that exists today; see
		// identityattr.go for why it is wired anyway.
		components = applyDerivedIdentityAttrs(components)
		syn := make([]string, len(p.CompositeArgs))
		for i, a := range p.CompositeArgs {
			syn[i] = strings.ToUpper(a)
		}
		importSyntax = strings.Join(syn, strings.ToUpper(p.CompositeSep))
	default:
		// bucketNeedsHandSeparator, bucketFoldChild, bucketEvidenceOnly:
		// no pastable claim on any of the four fields - the zero values
		// above are the honest "row-gen proposes nothing here" answer,
		// which compareOne diffs against the ratified entry the same as
		// any other disagreement.
	}
	return
}

// componentsEqual compares two Component slices structurally, treating nil
// and empty as equal (both mean "no components").
func componentsEqual(a, b []identity.Component) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
