// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file answers issue #270's question at the layer that classifies:
// which of [MarkerlessTypes] may resolve [ClassRecordLocated], given the
// provider schemas this run actually holds.
//
// The distinction the whole issue turns on, restated here because this is
// the file that acts on it: a marker answers "may I delete this", and an
// identity answers "which object is this". [MarkerlessTypes] is a fact
// about the first - the provider mints the ID and the type has no tags
// argument, so there is nowhere to write a marker. It is not a fact about
// the second. For an object choudoufu itself created, the ID is known at
// the moment of creation, and a per-estate record can hold it. That is only
// true on a platform that has migrated, which is why every predicate here
// is gated on a record_store being declared: the store is the migration
// step, and nothing below admits a type without one.

// locatedImportIDAttr is the schema attribute a located identity is read
// out of after an apply, and therefore the attribute a type must have
// before this package will admit it as located.
//
// It is "id" because that is the attribute OpenTofu's own import path
// round-trips: ImportResourceState is given a string and returns an object
// whose "id" is that string, so a type carrying a top-level string "id" is
// exactly a type that can be re-found from one. A type without one may
// still be importable by an identity OBJECT, but a [locatedPayload] holds a
// single string and inventing a grammar to flatten several attributes into
// one is what issue #105 exists to prevent - so such a type stays refused,
// which is where it already was. Measured against hashicorp/aws 6.59.0,
// thirteen of the 145 markerless types carry no top-level string id, of
// which eleven reach this condition (the other two are credential material
// and are refused before it). Closing that residue means a located payload
// that can carry an identity OBJECT, not a wider rule here.
// TestLocatedTypePopulation records the counts so they cannot drift
// silently.
//
// It is a schema fact, not a type list: no resource type is named here or
// anywhere else this predicate reads.
const locatedImportIDAttr = "id"

// recordStoreConfiguredIn reports whether cfg's root module declares a live
// block with a record_store.
//
// The same fact internal/live/lint reads under the same name, from the same
// field, for the same decision. It is duplicated rather than shared because
// the dependency would run the wrong way - lint already imports this
// package - and it is three lines over a field that cannot be read two ways.
// What must not drift is the ANSWER, and
// TestLocatedAdmissionAgreesWithLint is what holds the two together.
func recordStoreConfiguredIn(cfg *configs.Config) bool {
	if cfg == nil || cfg.Module == nil || cfg.Module.Live == nil {
		return false
	}
	return cfg.Module.Live.RecordStore != nil
}

// LocatedType reports whether resourceType may be admitted as
// record-located: the object exists in the cloud, carries no marker, and
// its identity can be recorded in and recovered from the estate's record
// store.
//
// Three conditions, all from the provider's own schema and none from a
// roster:
//
//  1. The type is in [MarkerlessTypes] and has no row of its own. A type
//     with a ratified row is already admitted by an ordinary path and must
//     not be re-routed through the store; the two sets are disjoint today
//     (live/admission_coverage_test.go holds the overlap at zero) and this
//     check is what keeps the ordering safe if that ever changes.
//
//  2. The type's schema carries no live secret material. This is the
//     credential exclusion CLAUDE.md names, DERIVED rather than listed:
//     [credentialMaterial] applies the same evidence rule
//     lint.ClassSecretRefused applies to logical types - every attribute
//     reachable from the block, nested attribute types and nested blocks
//     included, minus the deprecated ones.
//
//  3. The type has a top-level string [locatedImportIDAttr], so an applied
//     object has an identity that can actually be written to the store.
//     Admitting a type whose identity could never be recorded would trade a
//     plan refusal for an apply-time failure, which is the trade this whole
//     mechanism is forbidden to make.
//
// # Failing closed
//
// schemas is what the run holds. A run with no schemas, or with no schema
// for this type, gets false - not "probably fine". That is deliberate and
// it is the only safe direction: conditions 2 and 3 are both readable ONLY
// from a schema, so an absent schema means the credential exclusion cannot
// run, and a predicate that cannot run must not admit. The visible cost is
// that tools/refusal-probe's schema-less mode reports these types refused
// where a real run admits them; the alternative cost would be admitting
// credential material whenever a schema failed to load, which is not a
// trade worth making for a cheaper measurement.
func LocatedType(resourceType string, schemas map[string]providers.Schema) bool {
	if _, ok := MarkerlessTypes[resourceType]; !ok {
		return false
	}
	if _, ok := LookupType(resourceType); ok {
		// A ratified row wins, exactly as lint.markerlessVetoed already
		// arranges for the veto itself. Such a type is admitted by its own
		// path and never becomes located.
		return false
	}
	schema, ok := schemas[resourceType]
	if !ok || schema.Block == nil {
		return false
	}
	if credentialMaterial(schema.Block) {
		return false
	}
	return hasLocatedImportID(schema.Block)
}

// hasLocatedImportID reports whether b carries a top-level string
// [locatedImportIDAttr].
func hasLocatedImportID(b *configschema.Block) bool {
	a, ok := b.Attributes[locatedImportIDAttr]
	return ok && a != nil && a.Type == cty.String
}

// credentialMaterial reports whether b describes a resource that holds
// secret material: any attribute the provider marks Sensitive and does not
// also mark Deprecated, anywhere in the block - nested attribute object
// types and nested blocks included.
//
// This is the run-time form of the exclusion CLAUDE.md states as a roster
// of four type names. Deriving it instead of listing it is a maintainer
// ruling (2026-08-17) and it is the better rule for the ordinary reason: a
// roster covers the four types someone thought of, and this covers every
// type whose schema says the same thing, including the ones a future
// provider release adds.
//
// The rule is copied in SUBSTANCE, not in code, from
// tools/row-gen's liveSensitiveAttrs - the evidence behind
// lint.ClassSecretRefused - and the deprecation subtraction comes with it
// for that rule's own reason: a deprecated attribute is one the provider
// tells you not to use, and hashicorp/local deprecated
// local_file.sensitive_content precisely in order to move the sensitive
// case out into a different type, so counting it would classify a type by
// an attribute whose whole purpose is to no longer be its.
func credentialMaterial(b *configschema.Block) bool {
	found := false
	walkSchemaAttrs(b, func(a *configschema.Attribute) {
		if a.Sensitive && !a.Deprecated {
			found = true
		}
	})
	return found
}

// walkSchemaAttrs visits every attribute reachable from b, including those
// inside nested attribute object types and nested blocks.
func walkSchemaAttrs(b *configschema.Block, visit func(*configschema.Attribute)) {
	if b == nil {
		return
	}
	for _, a := range b.Attributes {
		if a == nil {
			continue
		}
		visit(a)
		walkSchemaObjectAttrs(a.NestedType, visit)
	}
	for _, nested := range b.BlockTypes {
		if nested == nil {
			continue
		}
		walkSchemaAttrs(&nested.Block, visit)
	}
}

// walkSchemaObjectAttrs is [walkSchemaAttrs] for a nested attribute type.
func walkSchemaObjectAttrs(o *configschema.Object, visit func(*configschema.Attribute)) {
	if o == nil {
		return
	}
	for _, a := range o.Attributes {
		if a == nil {
			continue
		}
		visit(a)
		walkSchemaObjectAttrs(a.NestedType, visit)
	}
}

// LocatedImportID reads the identity a located instance's applied object
// carries, for writing back to the store.
//
// It is the inverse of the import that [LocatedType]'s third condition
// describes, and it reads the same attribute, so a type this returns false
// for is a type [LocatedType] already refused - there is no shape where a
// located instance applies and then has no identity to record.
//
// The second return is false when the object has no such attribute, when it
// is null, or when it is not yet known, which is what a value read from a
// plan rather than from a finished apply looks like.
func LocatedImportID(obj cty.Value) (string, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.Type().IsObjectType() {
		return "", false
	}
	if !obj.Type().HasAttribute(locatedImportIDAttr) {
		return "", false
	}
	v := obj.GetAttr(locatedImportIDAttr)
	if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return "", false
	}
	if v.IsMarked() {
		// A marked value is refused, never unmarked. cty panics rather
		// than errors on a marked receiver, so this guard is what keeps
		// AsString below safe (internal/live/marksafe) - but the reason it
		// REFUSES is the stronger one: an identity derived from a
		// sensitive value would be written into the estate's record store
		// in clear, which is the no-secrets rule. Refusing produces the
		// "no usable identity to record" error, which stops the run.
		return "", false
	}
	s := v.AsString()
	if s == "" {
		return "", false
	}
	return s, true
}
