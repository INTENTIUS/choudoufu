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
// nowhere to go.
//
// This comment used to end that sentence with "and no enumeration mechanism
// rescues it", on the argument that every leg of discovery binds a live
// object by reading its markers, so for this population "unmarked" carries
// no signal and an estate's own object is indistinguishable from one created
// outside the tool. The premise was true of every leg that existed when it
// was written and is no longer true of all of them (GitHub issue #272):
// uniquename.go rescues a type whose client-supplied name AWS itself refuses
// to issue twice, and internal/live/discovery binds such an object by
// comparing that name against the listing. The conclusion was also stated
// too widely for its premise even then - "no mechanism rescues it" is a
// claim about mechanisms nobody had built, which is not a fact about the
// types.
//
// The rescue is narrow and it fails closed; see uniquename.go for the two
// independent texts it requires and the four further conditions. Everything
// it does not reach stays vetoed here, for the reason above.
//
// The rule is derived, per this generator's standing bar - it names no
// resource type. Taggability comes from live/survey-full.json's own
// signals.taggable, which tools/survey-gen computes from the provider
// schema by the same predicate internal/live/markers.Taggable applies at
// run time. Server-assignment comes from whichever of the two evidence
// sources actually decides admission for the type in question, and the
// distinction is load-bearing:
//
//   - for a type tools/row-gen/ratified.json already carries, the
//     RATIFIED ROW decides. That row is what ships, so it is what
//     determines whether resolution reaches discovery;
//   - for every other type, the CLASSIFIER's own bucket decides, because
//     that is the verdict a future admission batch would be acting on.
//
// Reading the classifier for an admitted type would be a defect, not a
// simplification. FIVE admitted untaggable rows - remeasured at 5502e8a3de,
// where this comment said six - are in the classifier's server-assigned
// bucket while their ratified row says they are not server-assigned at all:
// those types resolve from their own configuration today, never reach
// discovery, never need a marker, and vetoing them would remove five working
// types. The ratified row is the one that knows.
//
// Two independent measurements agree on the five, which is also the whole of
// what this file's corpus read is worth: dropping the tier and running
// -emit retracts exactly five types, and emitting from an emptied corpus
// grows MarkerlessTypes from 148 to 153 with the same five names.
//
// The corpus is tools/row-gen/ratified.json, passed in. It used to be
// [identity.DefaultTable] - this generator's own previous output - which is
// why the second of those measurements could only be taken by emptying the
// committed table. See ratified.go and issue #263.

// markerlessReason is the whole ruling, stated once. It is a rule and not
// 121 separate judgements, so it is written once and every vetoed type
// carries it - a per-type restatement would be the same sentence copied,
// with the extra property that a batch could edit one copy and diverge.
const markerlessReason = "the provider mints this type's identity and the type has no tags argument, " +
	"so every instance would need marker discovery to be found again and there is nowhere to write the marker"

// markerless reports whether the one rule vetoes this type: untaggable, and
// server-assigned by whichever source decides admission for it - unless
// issue #272's content-match rule has already proven the type findable
// without a marker at all.
//
// admittedRow is the ratified row and admitted says whether there is one;
// classifiedServerAssigned is the classifier's own verdict and
// docServerMinted the provider documentation's, both consulted only when
// no row exists. taggable is live/survey-full.json's signal. contentMatch
// is whether contentMatchRoster's two-source rule qualified this type
// (identity.ContentMatchTypes membership) - a marker is what every OTHER
// untaggable server-assigned type needs to be found again, and a type here
// has a substitute for it, so the veto's whole premise ("nowhere to write
// the marker, and nothing else identifies the object") does not hold.
//
// sourcesAgreeComposed is the two-sources-agreeing exception (issue #274):
// CloudFormation's registry model and the provider's own import
// documentation, read independently of the classifier's bucket, both say
// this type's identity is built from configuration rather than minted by
// the provider. It is checked only for an unadmitted type and only after
// classifiedServerAssigned/docServerMinted would otherwise fire, because
// those two evidence sources can themselves be wrong in the direction that
// matters here: a documentation heuristic can promote a composite
// primaryIdentifier to server-assigned on the strength of one import
// example that happens not to demonstrate every documented form, and a
// segment the doc's structured scrape could not attribute to any argument
// is not by itself proof the segment is server-minted. When the registry
// and the docs independently AGREE the identity is argument-built, that
// agreement outranks the single-source verdict that produced the veto in
// the first place. When they disagree - the registry says the identifier is
// supplied but the doc's own prose names it as the resource's own ID - this
// stays silent and the ordinary veto stands, because a wrong marker
// outranks a missing one.
//
// boundByName is the third exception (issue #272's unique-name rule), and
// it is a different claim from either of the other two: sourcesAgreeComposed
// says the identity is built from configuration after all, so the type was
// never in the veto's population; contentMatch says a substitute for the
// marker already exists; this one accepts every word of the veto - the
// identity IS minted and there IS nowhere to write a marker - and says the
// object is still recognisable, because AWS refuses to issue its name
// twice. It is checked LAST, after every other exception, because it is the
// only one that admits a type the veto's own premises hold of, and it must
// therefore be the one whose evidence is read most narrowly. uniquename.go
// computes it, and calls this function with it false to establish the veto
// would otherwise fire.
func markerless(taggable, contentMatch, admitted bool, admittedRow identity.TypeIdentity, classifiedServerAssigned, docServerMinted, sourcesAgreeComposed, boundByName bool) bool {
	if taggable || contentMatch {
		return false
	}
	if admitted {
		return admittedRow.ServerAssigned
	}
	if sourcesAgreeComposed {
		return false
	}
	if !classifiedServerAssigned && !docServerMinted {
		return false
	}
	return !boundByName
}

// primaryIdentifierPartlyReadOnly is the veto's second registry question,
// and it is the mirror image of [registryComposedOfArguments]: CloudFormation
// says SOME part of this type's identity is a property the service hands back
// rather than one the caller supplies, and some other part is not.
//
// # Why the veto asks a wider question than the classifier's bucket
//
// classify.go's rule 1 fires only when the primary identifier is WHOLLY
// read-only, and that is the right bar for the thing rule 1 decides: whether
// a pastable `serverAssigned(...)` row describes the type. A type whose
// identity is a server-minted leaf under a config-known parent is not that
// row - its identity is composite, and rendering it as a single opaque
// server-assigned value would be a wrong row.
//
// But the VETO asks a different question, and this is the distinction issue
// #309 spent three scouting passes recovering. The veto asks whether
// [identity.Resolve] can compute the value that names an instance from
// configuration. It cannot, for exactly the same reason in both cases: a
// component of the identity is minted by the service. A partly read-only
// identifier fails that test as completely as a wholly read-only one does -
// one unknown component is enough - so every such type goes to
// NeedsDiscovery, and being untaggable it has nowhere for discovery to read
// a marker from. That is the veto's whole premise, and it holds.
//
// Reading rule 1's bucket as the veto's evidence conflated the two
// questions, and the cost is measurable: 18 untaggable types with a mixed
// primary identifier sat outside [identity.MarkerlessTypes], and so could
// never reach [identity.LocatedType] - whose FIRST condition is membership
// in that roster - no matter what the estate had recorded about them.
//
// One claim worth not repeating: this is NOT the "third bucket, no ledger
// entry saying why" population #309's scoping comment described. Checked at
// 986f970c25 rather than inherited: all 18 already carry a hand entry in
// tools/row-gen/rejected.json, so naming one was always a refusal with a
// recorded reason, and internal/live/harness's unreached-type burndown does
// not move by one. What moves is what an estate that has declared a
// record_store can do with them, which is the only thing this was ever for.
//
// # Why widening this was sequenced behind two other changes
//
// A type here reaches the located mechanism, which records the applied
// object's `id` and hands that string back to import on the next run. For a
// server-minted LEAF under a config-known parent that string is a fragment,
// and recording a fragment trades an honest refusal for a deferred import
// failure - the trade "a wrong marker outranks a missing one" forbids.
// #329 (a located record carrying the provider's own identity OBJECT) and
// #337 (the roster proving whether an exported `id` is the whole documented
// import string) are what make the destination safe; identity.LocatedType
// now refuses every type neither of them settles. Widening here without
// those in place would have moved 14 of the 18 from a refusal to a wrong
// record.
//
// It reads the same two fields registryComposedOfArguments reads, from the
// same source, and names no resource type.
func primaryIdentifierPartlyReadOnly(p proposal) bool {
	if len(p.PrimaryIdentifier) == 0 {
		return false
	}
	readOnly := toSet(p.ReadOnly)
	minted, supplied := false, false
	for _, name := range p.PrimaryIdentifier {
		if readOnly[name] {
			minted = true
		} else {
			supplied = true
		}
	}
	// Wholly read-only is rule 1's own case and reaches the veto through
	// the classifier's bucket already; this leg exists for the mixed shape.
	return minted && supplied
}

// registryComposedOfArguments is the registry half of sourcesAgreeComposed's
// two-source check: CloudFormation's own primary identifier, read off this
// proposal's PrimaryIdentifier/ReadOnly fields exactly as classifyMapped
// populated them from live/registry.json, contains no read-only property.
// That is CloudFormation's own model saying every part of the identity is a
// property the caller supplies, not one the service hands back - the same
// fact classify.go's own rule 1 (isSubset(PrimaryIdentifier,
// ReadOnlyProperties)) tests for the opposite verdict. A type can reach
// bucketServerAssigned or bucketEvidenceOnly without rule 1 ever firing -
// over a documentation-driven precedence rule instead - and this is what
// lets the veto ask the registry's own, unmediated question rather than
// trusting whichever bucket the classifier landed the type in.
func registryComposedOfArguments(p proposal) bool {
	if len(p.PrimaryIdentifier) == 0 {
		return false
	}
	readOnly := toSet(p.ReadOnly)
	for _, name := range p.PrimaryIdentifier {
		if readOnly[name] {
			return false
		}
	}
	return true
}

// sourcesAgree computes markerless's sourcesAgreeComposed argument for one
// type: registryComposedOfArguments agrees with docMintedSegment's own
// negative - the provider's Import section names no segment as the
// resource's own server-provided attribute. A type outside the mapped set
// (no proposal) or outside live/import-grammar.json (no grammar row) has
// only one source, or none, and a lone source is not the agreement this
// rule requires - see markerless's own doc comment for why either alone is
// not enough.
func sourcesAgree(typeName string, byType map[string]proposal, importGrammar map[string]importGrammarRow) bool {
	p, hasProposal := byType[typeName]
	if !hasProposal || !registryComposedOfArguments(p) {
		return false
	}
	g, hasGrammar := importGrammar[typeName]
	if !hasGrammar {
		return false
	}
	_, minted := docMintedSegment(g)
	return !minted
}

// markerlessRoster is every provider type the rule vetoes, sorted.
//
// It iterates live/survey-full.json's own entries rather than the union of
// this tool's inputs, so a type the survey does not cover can never be
// vetoed by the absence of a signal: a missing entry would decode as
// taggable=false, and reading that as evidence of untaggability would veto
// on silence. Survey membership is the precondition, not a fallback.
// ratified is the ratified corpus, passed in rather than read out of
// [identity.DefaultTable] here (issue #263): a type's admission is decided by
// tools/row-gen/ratified.json, not by whatever -emit last wrote.
// boundByName is uniqueNameRows' key set: the types issue #272's unique-name
// rule rescues, computed once by that file and handed in rather than
// recomputed, so the roster and the rows can never disagree about which
// types they are talking about. contentMatch is contentMatchRoster's own
// qualifying set, keyed by TF type - issue #272's other bypass. Passed in
// rather than recomputed here for the same reason: buildEmitFiles computes
// it once and hands the same set to this function and to the content-match
// table's own render.
func markerlessRoster(ratified map[string]identity.TypeIdentity, survey map[string]surveyEntry, proposals []proposal, importGrammar map[string]importGrammarRow, boundByName map[string]identity.TypeIdentity, contentMatch map[string]bool) []string {
	byType := indexByType(proposals)
	classified, documented := serverAssignmentVerdicts(proposals, importGrammar)
	var out []string
	for typeName, entry := range survey {
		row, admitted := ratified[typeName]
		agree := sourcesAgree(typeName, byType, importGrammar)
		_, named := boundByName[typeName]
		if markerless(entry.Signals.Taggable, contentMatch[typeName], admitted, row, classified[typeName], documented[typeName], agree, named) {
			out = append(out, typeName)
		}
	}
	sort.Strings(out)
	return out
}

// serverAssignmentVerdicts is the two evidence sources [markerless] consults
// for a type with no ratified row, indexed by TF type: the registry's own
// verdict that some part of this type's identity is server-minted, and the
// provider documentation's named server segment for a type the registry
// reached no verdict on.
//
// The registry leg has two shapes and they are the same finding at two
// strengths. classify.go's rule 1 is the whole-identifier one, already
// carried as [bucketServerAssigned]; [primaryIdentifierPartlyReadOnly] is
// the mixed one - see its doc comment for why the veto asks a wider question
// than the bucket does, and why widening it had to wait for #329 and #337.
//
// It is split out of [markerlessRoster] so uniquename.go can ask the veto's
// question through [markerless] itself rather than through a paraphrase of
// it. A second implementation of "would this type be vetoed" is the shape
// that lets a rescue and a veto disagree, which would leave a type both
// absent from the table and absent from the roster explaining why.
func serverAssignmentVerdicts(proposals []proposal, importGrammar map[string]importGrammarRow) (classified, documented map[string]bool) {
	classified = make(map[string]bool, len(proposals))
	documented = make(map[string]bool, len(proposals))
	for _, p := range proposals {
		if p.Bucket == bucketServerAssigned || primaryIdentifierPartlyReadOnly(p) {
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
	return classified, documented
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
