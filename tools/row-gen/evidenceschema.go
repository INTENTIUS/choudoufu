// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is issue #428's row-gen change: "let the schema name the
// argument" for the bucketEvidenceOnly population, the same shape
// schemafirst.go (#387) gave the ALREADY-RATIFIED population, applied one
// admission step earlier.
//
// #387 measured, over every config-identified ratified row, whether the
// provider's own identity schema reproduces it - and left the ratified
// corpus untouched, because dropping an already-shipped row is a support
// change (that file's own doc comment: an earlier attempt to have this tool
// drop reproduced rows outright was reverted, "keep schemafirst.go as
// measurement only", in favour of a runtime precedence inversion instead).
// bucketEvidenceOnly carries no such risk in either direction: a proposal
// in this bucket is not in tools/row-gen/ratified.json and -emit never
// reads proposals at all (main.go's own doc comment - it copies every field
// verbatim from the ratified corpus), so reclassifying a fresh proposal
// here can never move anything DefaultTable or admittedTypesV0 already
// serve. It only changes what row-gen itself proposes: whether the printed
// report has a pastable block for a type that had none, and whether
// live/rowgen-buckets.json counts it client-named or evidence-only.
//
// # Why bucketEvidenceOnly ever misses a type the schema already answers
//
// classify.go's resolveArgName already prefers the provider's identity
// schema over every other argument-name source (its own doc comment: "the
// provider's own identity schema first"). But resolveArgName only ever
// runs from classifyMapped's rule 2, which is gated on the CFN registry's
// OWN shape first - len(PrimaryIdentifier)==1, createOnly, not readOnly.
// Three shapes reach bucketEvidenceOnly without that gate ever firing at
// all, so resolveArgName - and the schema it would have preferred - is
// never consulted:
//
//   - classifyUnmapped: live/mapping.json records no CloudFormation type at
//     all, so there is no registry PrimaryIdentifier to test rule 2
//     against. Its own doc comment already says the honest thing about
//     this shape - "there is no primaryIdentifier to reason from" - but
//     that was true of the REGISTRY, never of the provider's own schema.
//   - classifyFold, evidence-only leg: the fold parent is neither admitted
//     nor itself proposed, so [classifyFold] never reaches an argument-name
//     question for the child at all.
//   - classifyMapped's own default clause: a singleton primaryIdentifier
//     that is neither read-only nor create-only, or an empty
//     primaryIdentifier - "primaryIdentifier does not fit the
//     server-assigned or client-named shape" in the registry's terms,
//     which says nothing about the provider's own terms.
//
// # Why admission "schema", not merely Identity != nil
//
// live/survey-full.json's own Identity field is required_for_import
// attribute NAMES, nothing about whether the named attribute is a
// configuration argument a caller sets or one the provider computes and
// merely echoes back - many server-assigned types require "arn" or "id"
// for import too. tools/survey-gen already answers that question, with
// real provider schemas identity.Derivable reads directly (Required, not
// Computed; no nested-block flattening; every required-for-import attribute
// this way), and records the answer as admission "schema" - the same
// strict judgment identity.SynthesizeTypeIdentity's own single-attribute
// branch would make at runtime, computed offline once by survey-gen
// instead of per-run. Consulting it here rather than making a bare
// Identity != nil check is deliberate: reading "identity schema present" as
// "the schema names an argument" would misclassify a server-assigned type
// whose required_for_import happens to be a single Computed attribute as
// client-named, the false positive [markerlessRoster]'s own
// docMintedSegment leg exists to keep out of the veto's blind spot - see
// markerless.go's serverAssignmentVerdicts.
//
// Whether one of those attributes also names another managed type (an
// association's parent) does not matter here. [DerivableType]'s own doc
// comment says why: "A route's route_table_id is a required argument and is
// usually a reference to a live rtb- ID... aws_route is derivable in this
// sense and still resolves parent-derived per instance" - that is decided
// per INSTANCE, from the argument's expression, by [identity.Resolve]'s own
// classify step at plan time, never by the table row. A row-gen
// Components{Attrs: [argName]} row is exactly as correct either way.
//
// One population is excluded even when the schemas prove the declaration:
// a type whose identity-table entry builds the identity from configuration
// plus a cloud value (account or region), or binds it by an AWS-enforced
// unique name - [tableAssertedBinding]. The first needs a Cloud-valued
// Component, which [identity.SynthesizeTypeIdentity] and schemafirst.go's
// own comparison both structurally refuse to build (a hand-ratified table
// fact, not a schema-derivable one); the second needs
// tools/row-gen/uniquename.go's own cross-referenced
// provider-docs-plus-registry evidence, not the identity schema at all.
// Until #696 this exclusion came through survey-gen's path column, which
// read the same two table facts; it now reads the table directly.
//
// # Why exactly one required attribute
//
// A schema-proven identity can, in principle, name more than one required
// attribute.
// [identity.SynthesizeTypeIdentity]'s real runtime behaviour for that case
// is not classify.go's ordinary single-Attrs Component: it is
// [identity.TypeIdentity.IdentityObjectOnly] (issue #105), a shape that
// carries no import-string separator at all because none is invented -
// resolution imports by identity object instead. render.go's
// renderClientNamedEntry and [proposedFields]'s bucketClientNamed case
// build only the single-Attrs shape; extending them to emit an
// IdentityObjectOnly row is issue #105 territory, not this one, so a
// multi-attribute schema-proven row is left evidence-only here and
// ledgered by [schemaGapClass] as "multi-attribute" rather than silently
// mis-rendered as a single-argument row it is not.
//
// # What acts on this
//
// [applySchemaFirstArgName] is the mutation, run from classifyAll
// (main.go) AFTER applyImportGrammarPrecedence, not ahead of it - see
// main.go's own comment at the call site for why an earlier version that
// ran this pass first (on the "schema outranks import grammar" reasoning
// resolveArgName already states) measured zero net bucket movement: it
// only ever front-ran rows the grammar-precedence pass would have promoted
// on its own. Running last means a Covered row here is one every other
// evidence source in this file already had a turn on and still left
// bucketEvidenceOnly - genuinely new coverage, not a relabeling.
//
// Measured at 1eeda7c026 (this branch's base): rowgen-buckets.json's
// evidence_only count was 314 before this pass; live/survey-full.json
// named 479 types with an identity schema at provider v6.59.0. Re-run
// `go run ./tools/row-gen -evidence-gap` and read
// tools/row-gen/evidence-schema-gap.json rather than trusting either figure
// literally - both artifacts have moved under concurrent work all session,
// the way this file's own sibling (schemafirst.go) already warns its own
// count will.

// applySchemaFirstArgName is classifyAll's issue #428 pass, mutating
// proposals in place. See this file's own doc comment for the population,
// the safety reasoning behind admission "schema", and why exactly one
// required attribute.
func applySchemaFirstArgName(proposals []proposal, survey map[string]surveyEntry) {
	for i := range proposals {
		p := &proposals[i]
		if p.Bucket != bucketEvidenceOnly {
			continue
		}
		s, ok := survey[p.TFType]
		if !ok || s.Identity == nil || !s.schemaProvesDeclaration() || tableAssertedBinding(p.TFType) {
			continue
		}
		if len(s.Identity.RequiredForImport) != 1 {
			continue // multi-attribute: identity-object-only shape, #105 - see this file's own doc comment
		}
		arg := s.Identity.RequiredForImport[0]
		p.Bucket = bucketClientNamed
		p.ArgName = arg
		p.ArgSource = argSourceIdentitySchemaEvidenceOnly
		p.Rule = fmt.Sprintf(
			"issue #428: evidence-only, but live/survey-full.json's admission %q (survey-gen's identity.Derivable check against the provider's real schemas) proves the identity is fully client-supplied and names %s",
			s.Admission, arg)
		p.Notes = append(p.Notes, "argument name sourced from the provider's own identity schema (live/survey-full.json), reached because this row never satisfied classifyMapped rule 2's CFN-registry-shaped gate that would otherwise have consulted it (see evidenceschema.go)")
	}
}

// tableAssertedBinding reports whether identity.DefaultTable's entry for
// typeName carries one of the two identity facts no provider schema states:
// a component naming a cloud value (the run's account or region), or a
// unique-name binding. Both are hand-ratified table facts; see this file's
// doc comment for why either keeps a type out of the schema-first pass.
func tableAssertedBinding(typeName string) bool {
	entry, ok := identity.LookupType(typeName)
	if !ok {
		return false
	}
	if entry.UniqueName.Set() {
		return true
	}
	for _, c := range entry.Components {
		if c.Cloud != identity.CloudNone {
			return true
		}
	}
	return false
}

// The has-schema gap families, keyed by tier where the tier settles it.
// Until #696 these were named after survey-gen's path tokens; the families
// now follow the readiness tiers (live/readiness.json): tier A for a
// taggable type, tier B for one whose declaration or the identity table
// carries its identity, tier C for the rest.
const (
	gapMultiAttribute     = "multi-attribute"
	gapTierBTableAsserted = "tier-b-table-asserted"
	gapTierATaggable      = "tier-a-taggable"
	gapTierCRecordCarried = "tier-c-record-carried"
)

// schemaGapClass labels why a bucketEvidenceOnly type carrying a provider
// identity schema is NotCovered - checked in the order below, first match
// wins, matching schemafirst.go's notReproducedClass in spirit: each class
// names what a later unit would need to actually close the gap, not merely
// that one exists.
func schemaGapClass(s surveyEntry) string {
	switch {
	case tableAssertedBinding(s.Type):
		return gapTierBTableAsserted
	case s.schemaProvesDeclaration():
		return gapMultiAttribute // len(RequiredForImport) != 1 - see applySchemaFirstArgName's own guard
	case s.Signals.Taggable:
		return gapTierATaggable
	default:
		return gapTierCRecordCarried
	}
}
