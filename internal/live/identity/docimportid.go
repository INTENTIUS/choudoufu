// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// This file is the run-time half of GitHub issue #337's second qualification
// route. tools/row-gen/docimportid.go is the other half and states why an
// order read off a documentation page is not the invented order issue #105
// forbids; this file states what run time still has to check before acting on
// one.
//
// The split is the point. The generator holds the documentation and no
// schema; this holds the schema and no documentation. A name the page states
// is a proposal until the provider's own schema says the applied object
// carries it, and everything below is that check.

// DocumentedImportID is one type's documented import-ID grammar: the segments
// the page names, in the documented order, and the character joining them.
//
// [DocumentedImportIDs] is the generated set. The struct lives here rather
// than in the generated file because the generated file is data - a type
// declaration in it would be a definition nothing but a generator could
// change.
type DocumentedImportID struct {
	// Separator is the documented join character.
	Separator string

	// Parts are the segments in the documented order.
	Parts []DocumentedImportIDPart
}

// DocumentedImportIDPart is one documented segment.
type DocumentedImportIDPart struct {
	// Name is the segment's name in comparison form - lower case with
	// punctuation removed, so "REST-API-ID" and "rest_api_id" are one
	// name. [normalizeDocName] is the reduction, and it is applied to
	// schema attribute names here so the two sides meet in one form.
	Name string

	// Argument records whether the page's own Argument Reference names
	// this segment as a configuration argument. See
	// [resolveDocumentedImportID] for the one decision it makes.
	Argument bool
}

// NormalizeDocName is [normalizeDocName] for the generator that writes the
// names this package matches. It is exported for one caller and one purpose:
// tools/row-gen's TestNormalizeDocNameMatchesTheGenerator asserts the three
// copies of this reduction agree. A drift between them would be invisible
// otherwise - the generator would emit names run time could never match, and
// the whole route would silently reach nothing with every other test still
// passing.
func NormalizeDocName(s string) string { return normalizeDocName(s) }

// normalizeDocName reduces a name to the form both sides of a match are
// compared in: lower case, non-alphanumerics dropped.
//
// It is tools/importdocs-gen's normalize and tools/row-gen's normalizeName,
// re-declared here for the reason those two are re-declared from each other:
// this package reads a generated artifact rather than importing the generator
// that wrote it. TestNormalizeDocNameMatchesTheGenerator holds the three
// copies to one answer.
func normalizeDocName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// resolveDocumentedImportID resolves resourceType's documented segments
// against the schema block the provider actually serves, returning the
// top-level string attributes to read in the documented order and the
// character to join them with.
//
// This is the second way a type qualifies for a composite located identity.
// The first, [LocatedIdentityPlanFor]'s wire-schema branch, records the
// provider's own identity OBJECT and needs no order and no separator. This
// one exists for the types that branch cannot see at all: measured at
// 56481a4bbf, 42 of the 59 markerless types with a documented composite
// import carry no wire identity schema, so for them the wire branch is not a
// gate but a silence, and the string is the only carrier there is.
//
// # What has to be true, and why each part of it
//
// Every segment must resolve to a TOP-LEVEL STRING attribute of the block.
// That is what "readable off the applied object" means: internal/live's
// located write-back reads the finished object attribute by attribute, and a
// segment it cannot read is a segment that would be missing from the recorded
// string.
//
// The reduction is many-to-one, so a block with two top-level string
// attributes reducing to the same name makes a match AMBIGUOUS rather than
// absent. Such a name is treated as unresolved rather than picked between.
//
// # The one inference, and the two guards on it
//
// Exactly one segment may fail to resolve, and it is then read as the
// resource's own server-minted identifier, [locatedImportIDAttr]. The
// reasoning is the population's own definition: a type reaches here because
// its page documents a composite import that its `id` bullet does not
// corroborate, which is precisely the shape where the provider sets `id` to
// the minted leaf and the import path expects the whole string. The object
// carries that leaf under `id` and under no other name the page would know.
//
// Two guards keep that from becoming a guess:
//
//   - A segment the page's own Argument Reference calls a CONFIGURATION
//     ARGUMENT is never read as the minted leaf. If such a segment does not
//     resolve, the page and the schema disagree about a name, and binding
//     `id` into its position would put the right value in the wrong slot -
//     a wrong identity, composed confidently. The type is refused instead.
//
//   - `id` must be a top-level string on the block and must not already be
//     another segment's own resolution. A composite whose segments include
//     `id` explicitly (the page names it) has nothing left over to infer.
//
// Two or more unresolved segments are refused outright: there is no way to
// say which of them the leaf fills, and filling either is a coin toss with a
// wrong identity on one face.
//
// # Failing closed
//
// Every refusal here returns false, and false means [LocatedIdentityPlanFor]
// keeps the refusal [IDNotProvenWholeTypes] already made. This route can turn
// a refusal into an admission and can do nothing else: it is consulted only
// where the bare-`id` rule was already refusing, so no configuration that
// works today can be made to stop working by anything in this file.
func resolveDocumentedImportID(resourceType string, b *configschema.Block) (parts []string, separator string, ok bool) {
	g, known := DocumentedImportIDs[resourceType]
	if !known || b == nil || g.Separator == "" || len(g.Parts) < 2 {
		return nil, "", false
	}

	byName := attrsByDocName(b)

	resolved := make([]string, len(g.Parts))
	claimed := make(map[string]bool, len(g.Parts))
	inferred := -1
	for i, p := range g.Parts {
		attr, found := byName[p.Name]
		if !found {
			if p.Argument || inferred >= 0 {
				// See this function's doc comment: a named configuration
				// argument the schema does not carry is a disagreement,
				// and a second unresolved segment is a coin toss.
				return nil, "", false
			}
			if pluralCollectionCollision(b, p.Name) {
				// The segment's own name, pluralized, names a real
				// COLLECTION attribute on this very block - the schema is
				// telling us the concept it names is multi-valued, not a
				// single scalar `id` could ever stand in for. Inferring
				// `id` here would be a guess this package can already
				// disprove from its own schema, so it is refused instead -
				// see this function's doc comment on the "id" inference
				// and [pluralCollectionCollision] for the case that forced
				// this: aws_security_group_rule's documented "cidr_block"
				// segment names one element of the block's real
				// cidr_blocks LIST, and the provider's own `id` is a hash
				// of the whole rule (security_group_id + ports + protocol
				// + every source), not any one source - confirmed against
				// the provider's own securityGroupRuleCreateID and
				// resourceSecurityGroupRuleImport, not inferred. Recording
				// `id` in the source's place would compose a string the
				// provider's own importer refuses to parse, and the
				// concept the segment actually names - one element of a
				// list the configuration may set several of at once - has
				// no single-attribute representation this package's
				// grammar can read at all; that is new machinery, not a
				// corroboration gap, and is refused here rather than
				// guessed at.
				return nil, "", false
			}
			inferred = i
			continue
		}
		if claimed[attr] {
			return nil, "", false
		}
		claimed[attr] = true
		resolved[i] = attr
	}

	if inferred >= 0 {
		a, has := b.Attributes[locatedImportIDAttr]
		if !has || a == nil || a.Type != cty.String || claimed[locatedImportIDAttr] {
			return nil, "", false
		}
		resolved[inferred] = locatedImportIDAttr
	}

	return resolved, g.Separator, true
}

// attrsByDocName indexes b's top-level string and number attributes by
// [normalizeDocName], dropping any name two attributes reduce to.
//
// Number is admitted alongside string because a documented import segment
// is exactly as readable off a top-level number as off a top-level string:
// [locatedAttrSegment] renders a resolved number attribute back into the
// plain decimal form the provider's own import string uses at write-back
// time (issue #384's regression - aws_security_group_rule's from_port and
// to_port are cty.Number on the real hashicorp/aws schema, and a segment
// this index cannot see is a segment [resolveDocumentedImportID] can never
// resolve, which is exactly what left that type unable to reach the record
// rung). No other cty type is admitted: a bool or a collection is not a
// single token an import string could hold, and there is no established
// rendering to check against.
//
// Dropping rather than picking is the same posture the rest of this package
// takes towards an ambiguous reading: a name that could mean two attributes
// means neither, and the caller treats it as unresolved. Two attributes of
// DIFFERENT admitted types that reduce to the same name are ambiguous the
// same way - the reduction has already lost which one the segment means, so
// which type it would render as is not decidable either.
func attrsByDocName(b *configschema.Block) map[string]string {
	out := make(map[string]string, len(b.Attributes))
	ambiguous := make(map[string]bool)
	for name, a := range b.Attributes {
		if a == nil || (a.Type != cty.String && a.Type != cty.Number) {
			continue
		}
		n := normalizeDocName(name)
		if n == "" {
			continue
		}
		if _, seen := out[n]; seen {
			ambiguous[n] = true
			continue
		}
		out[n] = name
	}
	for n := range ambiguous {
		delete(out, n)
	}
	return out
}

// pluralCollectionCollision reports whether b carries a top-level LIST, SET
// or MAP attribute whose [normalizeDocName] equals segmentName's own
// reduction with a trailing "s" appended - the ordinary AWS provider
// pluralization of a singular concept name (cidr_block -> cidr_blocks,
// subnet_id -> subnet_ids, and so on across dozens of schemas).
//
// It exists purely to make [resolveDocumentedImportID]'s "id" inference
// MORE conservative, never less: it only ever turns a would-be inference
// into a refusal, and only for the one segment already about to be
// inferred, so it cannot make anything this package accepts today less
// safe - see [TestResolveDocumentedImportIDCorroboratesEveryNameAgainstTheSchema]'s
// containment cases. A segment whose reduced name has no such collision is
// unaffected.
//
// The English-pluralization check is deliberately narrow rather than
// clever: it is exactly the relationship the case that forced this
// (aws_security_group_rule's "cidr_block" segment against its real
// cidr_blocks list) exhibits, it is common across the provider's own
// naming (see the doc comment where this is called), and a narrower net
// than this would still catch that case while a cleverer one would be
// harder to trust without more of them to measure against.
func pluralCollectionCollision(b *configschema.Block, segmentName string) bool {
	plural := segmentName + "s"
	for name, a := range b.Attributes {
		if a == nil || !a.Type.IsCollectionType() {
			continue
		}
		if normalizeDocName(name) == plural {
			return true
		}
	}
	return false
}

// LocatedComposedImportID composes an applied object's documented import
// string out of the attributes [resolveDocumentedImportID] named, in the
// order it named them.
//
// It is the write-back counterpart of that resolution, and it is
// all-or-nothing for [LocatedIdentity]'s reason: a string missing a segment
// is not a shorter identity, it is a different one, and it would be handed to
// a later import as though it were whole.
//
// The extra refusal here is the separator collision. A segment whose own
// value contains the join character makes the composed string ambiguous - the
// provider's importer splits on that character and would see more segments
// than the object has - so a value like that is refused rather than joined.
// That is a check the documentation cannot make and the values can, which is
// why it lives at write-back rather than in the roster.
//
// A segment [attrsByDocName] resolved against a number attribute is rendered
// by [locatedAttrSegment], not read as a string directly - see that
// function's doc comment for what "rendered" means and what it refuses.
func LocatedComposedImportID(obj cty.Value, parts []string, separator string) (string, bool) {
	if len(parts) < 2 || separator == "" {
		return "", false
	}
	segments := make([]string, 0, len(parts))
	for _, name := range parts {
		v, ok := locatedAttrSegment(obj, name)
		if !ok || strings.Contains(v, separator) {
			return "", false
		}
		segments = append(segments, v)
	}
	return strings.Join(segments, separator), true
}
