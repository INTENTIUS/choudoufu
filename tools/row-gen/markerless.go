// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"go/format"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is the one admission bound this generator states as a rule
// rather than discovering one type at a time (issue #249): a type whose
// identity the provider mints, and which has no tags argument to write an
// ownership marker into, cannot be admitted, because there is no way to
// find the object again after it is created.
//
// The two halves of the rule and why each is necessary:
//
//   - SERVER-ASSIGNED. The value that names an instance is not in the
//     configuration, so [identity.Resolve] cannot compute it. Every
//     instance goes to NeedsDiscovery, and discovery finds an object by
//     reading the two marker tags off it.
//   - UNTAGGABLE. There is no tags argument, so the markers cannot be
//     written. internal/live/stamp's SkipUntaggable fires, mustStamp is
//     true because the block is in NeedsDiscovery, and the apply refuses.
//
// Either half alone is fine and both halves are common. 442 admitted rows
// are server-assigned and taggable: the marker is exactly what finds them,
// which is the mechanism working as designed. Rows that are untaggable and
// name themselves from configuration resolve without discovery ever
// running, so nothing needs a marker. It is only the conjunction that has
// nowhere to go, and no enumeration mechanism rescues it: every leg of
// discovery binds a live object by reading the markers, and for this
// population every candidate's marker is absent by definition, so
// "unmarked" carries no signal and an estate's own object is
// indistinguishable from one created outside the tool.
//
// The rule is derived, per this generator's standing bar - it names no
// resource type. Taggability comes from live/survey-full.json's own
// signals.taggable, which tools/survey-gen computes from the provider
// schema by the same predicate internal/live/markers.Taggable applies at
// run time. Server-assignment comes from whichever of the two evidence
// sources actually decides admission for the type in question, and the
// distinction is load-bearing:
//
//   - for a type internal/live/identity.DefaultTable already admits, the
//     RATIFIED ROW decides. That row is what ships, so it is what
//     determines whether resolution reaches discovery;
//   - for every other type, the CLASSIFIER's own bucket decides, because
//     that is the verdict a future admission batch would be acting on.
//
// Reading the classifier for an admitted type would be a defect, not a
// simplification. Six admitted untaggable rows - measured, not asserted -
// are in the classifier's server-assigned bucket while their ratified row
// says they are not server-assigned at all: those types resolve from their
// own configuration today, never reach discovery, never need a marker, and
// vetoing them would remove six working types. The ratified row is the one
// that knows.

// markerlessReason is the whole ruling, stated once. It is a rule and not
// 121 separate judgements, so it is written once and every vetoed type
// carries it - a per-type restatement would be the same sentence copied,
// with the extra property that a batch could edit one copy and diverge.
const markerlessReason = "the provider mints this type's identity and the type has no tags argument, " +
	"so every instance would need marker discovery to be found again and there is nowhere to write the marker"

// markerless reports whether the one rule vetoes this type: untaggable, and
// server-assigned by whichever source decides admission for it.
//
// admittedRow is the ratified row and admitted says whether there is one;
// classifiedServerAssigned is the classifier's own verdict and
// docServerMinted the provider documentation's, both consulted only when
// no row exists. taggable is live/survey-full.json's signal.
func markerless(taggable, admitted bool, admittedRow identity.TypeIdentity, classifiedServerAssigned, docServerMinted bool) bool {
	if taggable {
		return false
	}
	if admitted {
		return admittedRow.ServerAssigned
	}
	return classifiedServerAssigned || docServerMinted
}

// markerlessRoster is every provider type the rule vetoes, sorted.
//
// It iterates live/survey-full.json's own entries rather than the union of
// this tool's inputs, so a type the survey does not cover can never be
// vetoed by the absence of a signal: a missing entry would decode as
// taggable=false, and reading that as evidence of untaggability would veto
// on silence. Survey membership is the precondition, not a fallback.
func markerlessRoster(survey map[string]surveyEntry, proposals []proposal, importGrammar map[string]importGrammarRow) []string {
	classified := make(map[string]bool, len(proposals))
	documented := make(map[string]bool, len(proposals))
	for _, p := range proposals {
		if p.Bucket == bucketServerAssigned {
			classified[p.TFType] = true
		}
		// The documentation leg answers only where the classifier
		// reached no verdict at all. bucketEvidenceOnly is that state
		// stated in the classifier's own vocabulary, and it is the only
		// bucket where a doc sentence cannot contradict something the
		// registry already settled: bucketClientNamed, bucketComposite
		// and bucketAssembled are all positive findings that the ID
		// reconstructs from configuration, and vetoing over one of them
		// would be reading the weaker source over the stronger. It is
		// also where every no-CFN-model row starts and, absent an
		// import-grammar precedence rule promoting it, stays - which is
		// the population that has no registry verdict to reach.
		if p.Bucket == bucketEvidenceOnly {
			if _, ok := docMintedSegment(importGrammar[p.TFType]); ok {
				documented[p.TFType] = true
			}
		}
	}
	var out []string
	for typeName, entry := range survey {
		row, admitted := identity.DefaultTable[typeName]
		if markerless(entry.Signals.Taggable, admitted, row, classified[typeName], documented[typeName]) {
			out = append(out, typeName)
		}
	}
	sort.Strings(out)
	return out
}

// markerlessTableRel is the generated roster's home. It sits beside the
// identity table because it answers the same question that table does -
// may this type be admitted - and because internal/live's own accounting
// (live/admission_coverage_test.go) has to read both to know that a type
// outside the identity table is outside it for a recorded reason rather
// than because nobody has looked.
const markerlessTableRel = "internal/live/identity/markerless_generated.go"

// renderMarkerlessFile renders internal/live/identity's veto roster.
func renderMarkerlessFile(types []string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(licenseHeader)
	b.WriteString("\n")
	b.WriteString(emitGeneratedByComment)
	b.WriteString("\n\n")
	b.WriteString("package identity\n\n")
	b.WriteString(markerlessDoc)
	fmt.Fprintf(&b, "const MarkerlessReason = %q\n\n", markerlessReason)
	b.WriteString(markerlessTypesDoc)
	b.WriteString("var MarkerlessTypes = map[string]struct{}{\n")
	for _, t := range types {
		fmt.Fprintf(&b, "%q: {},\n", t)
	}
	b.WriteString("}\n")
	return format.Source([]byte(b.String()))
}

// markerlessDoc is MarkerlessReason's own doc comment, carried by the
// generator for the reason defaultTableDoc is.
const markerlessDoc = `// MarkerlessReason is why every type in [MarkerlessTypes] is outside
// [DefaultTable]. It is one ruling covering the whole set, not a summary of
// many: the two facts it names are the only two the rule reads.
`

// markerlessTypesDoc is MarkerlessTypes' own doc comment.
const markerlessTypesDoc = `// MarkerlessTypes is every provider resource type tools/row-gen refuses to
// admit because the provider mints its identity and it carries no tags
// argument - see [MarkerlessReason].
//
// This is a veto, not a gap. A type here is absent from [DefaultTable]
// deliberately and by a derived rule, which is what distinguishes it from a
// type no ratification batch has reached yet. tools/row-gen/rejected.json
// records the rulings a batch made by hand; this records the one a rule
// makes, and the two overlap wherever a batch had already reached the same
// answer about a type.
//
// The set is derived on every generator run from live/survey-full.json's
// taggability signal and the server-assignment verdict that decides
// admission for each type. Nothing here is maintained by hand.
`
