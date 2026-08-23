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

// This file is rowgen-convergence's measurement half: for every type -emit
// admits, it regenerates
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
	// human ruling. Since issue #132's gate every mismatched row carries an
	// annotation regardless, so this flag no longer decides whether a row
	// is ruled - it stays the measure of WHICH rulings are extractor debt
	// rather than ratification judgments.
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

	// SchemaReproduces is ruling 2 of rfc/20260823-foundation-order-ruling.md
	// (#387): the config-identified ratified rows this run's own
	// schemaFirstReproduced (schemafirst.go) found the provider's identity
	// schema already reproduces, and so are held out of Types/Summary
	// entirely - [buildConvergence] never sees them, the same way a
	// RecordBacked or veto-excluded type never reaches it. Recorded here
	// rather than only in the shrunk admitted_total, so the drop is an
	// artifact fact and not a sentence in a PR description: the count is
	// len(Types), always, and TestConvergenceArtifactMatchesCommitted holds
	// the whole struct - this field included - to a fresh regeneration.
	SchemaReproduces schemaReproducesBucket `json:"schema_reproduces"`

	Types []convergenceRow `json:"types"`
}

// schemaReproducesBucket is [convergenceArtifact.SchemaReproduces]'s shape.
// Count and Types are the rows actually dropped from the emitted table
// today - [schemaFirstDrop]'s dropped half. CandidateCount and HeldByGolden
// are the fuller, offline-only measurement: every config-identified row the
// same-name comparison alone calls reproduced (schemafirst.go), and the
// ones of those internal/live/check's identity golden still exercises and
// so keeps in the table rather than let disappear from that schema-less
// instrument's output (goldenexercised.go). CandidateCount - Count ==
// len(HeldByGolden) always; HeldByGolden is the visible worklist for
// whatever later lands the schemas that path would need to drop safely too
// (the plan-node seam, #388, or a golden that loads them).
type schemaReproducesBucket struct {
	Count int      `json:"count"`
	Types []string `json:"types"`

	CandidateCount int      `json:"candidate_count"`
	HeldByGolden   []string `json:"held_by_golden"`
}

// buildConvergence runs the comparison over emitted - every row -emit would
// write, from [emittedRows] - using proposals (a fresh classifyAll run) and
// annotations (the per-type rulings tools/row-gen/annotations.json records).
//
// The population is emitted rather than [identity.DefaultTable] because of
// issue #263. This verdict feeds -emit's unruled gate (emit.go), which
// refuses any emitted row the fresh classifier does not reproduce and
// annotations.json does not rule. Reading the population out of the
// committed table meant a row that is ratified but not yet in that table -
// which is now the normal shape of adding one, a hand edit to
// tools/row-gen/ratified.json followed by a regeneration - was never
// compared at all, so matched came back false and the operator was made to
// write an annotation for a row the classifier may well agree with.
//
// summary.admitted_total keeps its meaning across the change, and that is
// deliberate: internal/live/harness's rowgen-annotation-ledger entry uses it
// as a denominator whose stated job is to make un-admitting a type visible.
// [emittedRows]' key set is the same 892 types the committed table holds, so
// the artifact is byte-identical - but it now counts what -emit admits
// rather than what -emit last wrote, which is the number that denominator
// was always reaching for.
func buildConvergence(emitted map[string]identity.TypeIdentity, proposals []proposal, annotations map[string]annotation) convergenceArtifact {
	byType := indexByType(proposals)

	admitted := make([]string, 0, len(emitted))
	for t := range emitted {
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
		ratified := emitted[tf]
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
		components = []identity.Component{{
			Attrs:        []string{p.ArgName},
			Default:      p.ArgDefaults[p.ArgName],
			Cloud:        identity.CloudValue(p.ArgCloud[p.ArgName]),
			IdentityAttr: identity.SameNameIdentity,
		}}
		importSyntax = strings.ToUpper(p.ArgName)
	case bucketComposite:
		for i, arg := range p.CompositeArgs {
			if i > 0 {
				components = append(components, identity.Component{Literal: p.CompositeSep})
			}
			components = append(components, identity.Component{
				Attrs:        []string{arg},
				Default:      p.ArgDefaults[arg],
				Cloud:        identity.CloudValue(p.ArgCloud[arg]),
				IdentityAttr: identity.SameNameIdentity,
			})
		}
		// #106 criterion 3: an assembled row whose leading literal names its
		// own scheme carries the derived IdentityAttr on every component. A
		// separator-joined composite never has a leading literal, so this is
		// inert here; bucketAssembled below is the shape it was wired for.
		components = applyDerivedIdentityAttrs(components)
		syn := make([]string, len(p.CompositeArgs))
		for i, a := range p.CompositeArgs {
			syn[i] = strings.ToUpper(a)
		}
		importSyntax = strings.Join(syn, strings.ToUpper(p.CompositeSep))
	case bucketAssembled:
		// Issue #172's shape: the template's segments become Components
		// verbatim - Literal, Cloud slot, or a single-argument Attrs - and
		// the leading scheme literal derives the per-component IdentityAttr
		// (identityattr.go's rule, inert until this bucket existed). No
		// IdentityAttrs claim: the id-alias pairing stays issue #44's
		// declared non-goal here as everywhere.
		var syn strings.Builder
		for _, s := range p.Assembled {
			switch {
			case s.Cloud != "":
				components = append(components, identity.Component{Cloud: identity.CloudValue(s.Cloud)})
				syn.WriteString(strings.ToUpper(strings.ReplaceAll(s.Cloud, "-", "_")))
			case s.Argument != "":
				components = append(components, identity.Component{Attrs: []string{s.Argument}})
				syn.WriteString(strings.ToUpper(s.Argument))
			default:
				components = append(components, identity.Component{Literal: s.Literal})
				syn.WriteString(s.Literal)
			}
		}
		components = applyDerivedIdentityAttrs(components)
		importSyntax = syn.String()
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
// componentsEqual compares two component lists ignoring
// [identity.Component.ServerAssignedIfAbsent] (#190). Every other field this
// package's classifyAll pipeline reconstructs the way a ratification batch
// would have chosen it; ServerAssignedIfAbsent is not one of those - no
// proposal bucket ever claims it, because it is not a classification
// judgment at all, only emit.go's own mergeServerAssigned annotation of an
// already-ratified row from live/import-grammar.json's Argument Reference
// evidence (renderIdentityFile's doc comment explains why that lives in
// emit.go rather than here). Comparing it here would fail every ratified row
// mergeServerAssigned touches the moment #190 landed, for a field
// compareOne's caller never asked classifyAll to propose - the same
// "claimed nothing, so it is not wrong" reasoning compareOne already applies
// to an unclaimed IdentityAttrs value.
func componentsEqual(a, b []identity.Component) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(stripMergedFields(a), stripMergedFields(b))
}

// stripMergedFields returns a copy of comps with every field emit.go merges
// in from live/import-grammar.json cleared, for componentsEqual's own
// comparison: [identity.Component.ServerAssignedIfAbsent] (#190) and
// [identity.Component.Attrs] on a component that carries a
// [identity.Component.Cloud] value (#241, emit.go's mergeCloudDefault).
// Neither is a classification judgment any proposal bucket makes - a cloud
// slot's ARGUMENT NAME is emit.go's to fill in, whether the proposal
// rendered the slot with no Attrs at all (bucketAssembled, whose template
// segment carries only the cloud property) or with the argument the
// cloud_default bullet names (bucketComposite, via ArgCloud) - so
// comparing either would fail every ratified row the merge touches for a
// field compareOne's caller never asked classifyAll to propose. The Cloud
// value itself is still compared, and that is the point: whether a segment
// is an account/region slot at all is a claim the classifier now makes.
// Only the Attrs of a cloud-bearing component are cleared; an ordinary
// argument component's Attrs is the classifier's own claim and is compared
// in full.
//
// Never mutates its argument: a is [identity.DefaultTable]'s own slice by
// way of compareOne's ratified parameter, and mutating it in place would
// corrupt the table every other caller in this process still reads.
func stripMergedFields(comps []identity.Component) []identity.Component {
	if len(comps) == 0 {
		return comps
	}
	out := make([]identity.Component, len(comps))
	for i, c := range comps {
		c.ServerAssignedIfAbsent = false
		if c.Cloud != identity.CloudNone {
			c.Attrs = nil
		}
		out[i] = c
	}
	return out
}
