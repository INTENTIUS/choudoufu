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

// VariadicTrailingImportIDTypes is the ratified set of resource types whose
// documented composite import grammar ends in a VARIADIC tail - the
// provider's own error-message grammar for aws_security_group_rule spells
// it literally: "SECURITYGROUPID_TYPE_PROTOCOL_FROMPORT_TOPORT_SOURCE
// [_SOURCE]*" - one token per element of whichever of several SIBLING
// collection or scalar arguments the configuration set, rather than one
// token for a single named attribute. [pluralCollectionCollision] already
// detects the shape generically (a documented segment's singular name
// collides with a real plural collection attribute on the schema) and
// refuses it, correctly, for every type NOT in this set: detecting the
// shape is not the same as knowing it is SAFE to render every family member
// as a token in a fixed, ratified order, and that second fact is not
// readable from the schema at all - it is a fact about how the provider's
// own IMPORT FUNCTION parses the trailing tokens back, which only the
// provider's source states.
//
// # What has to be true before a type is named here, and how it was checked
// for aws_security_group_rule
//
// The importer must classify each trailing token independently of its
// POSITION, so that concatenating the sibling families in a fixed order -
// this package always uses the order the type's own [Component.SoleElement]
// row lists them in, which is how [variadicTrailingGroup] reads the roster -
// is something the provider can always parse back correctly regardless of
// which family happens to come first for a given instance. Read directly
// from hashicorp/aws's internal/service/ec2/vpc_security_group_rule.go
// (resourceSecurityGroupRuleImport, fetched 2026-08-23, no terraform in the
// loop):
//
//	sources := parts[5:]
//	...
//	for _, v := range sources {
//		switch {
//		case v == "self":
//			d.Set("self", true)
//		case strings.Contains(v, "sg-"):
//			d.Set("source_security_group_id", v)
//		case strings.Contains(v, ":"):
//			ipv6CIDRBlocks = append(ipv6CIDRBlocks, v)
//		case strings.Contains(v, "pl-"):
//			prefixListIDs = append(prefixListIDs, v)
//		default:
//			cidrBlocks = append(cidrBlocks, v)
//		}
//	}
//
// Every trailing token is sorted into its family by the SHAPE of its own
// content - "sg-", a colon, "pl-", or else a bare CIDR - never by which
// position it occupies among the other tokens. That is confirmed by the
// documentation this package already carries in live/import-grammar.json:
// the Import section's own second example joins four trailing tokens as
// "10.1.0.0/16_2001:db8::/48_10.2.0.0/16_2002:db8::/48" - an IPv4 CIDR, an
// IPv6 CIDR, a second IPv4 CIDR and a second IPv6 CIDR, interleaved rather
// than grouped by family, which the source above shows the importer
// re-groups on read regardless. So the CROSS-FAMILY order this package
// picks is free; what is NOT free is the order WITHIN one family's own
// list, because cidr_blocks/ipv6_cidr_blocks/prefix_list_ids are all
// schema.TypeList (ordered, not a set) on the real provider schema, and the
// importer appends each token to its bucket in the order it is encountered
// - so [locatedVariadicSegments] preserves each family's own element order
// and never sorts it, unlike [Component.PerElement]'s set case.
//
// Note what this does NOT establish: "self" (a bool, rendered as the
// literal token "self") is part of the provider's own source-token grammar
// but is not one of [Component.SoleElement]'s ratified Attrs for this type,
// so an instance combining self with another family member is unaffected by
// this mechanism either way - the same pre-existing gap
// [Component.SoleElement]'s own row has always had, not something this
// change widens or narrows.
//
// Reach: exactly the population [Component.SoleElement] itself reaches,
// because [variadicTrailingGroup] is keyed off that same field and refuses
// where no row names the colliding attribute as one of a SoleElement
// family. Measured against internal/live/identity/table_generated.go
// (`grep -c "SoleElement: true"`), that is ONE row today
// (aws_security_group_rule); a future SoleElement row reaches this
// mechanism automatically once its own type is added here after the same
// verification, never automatically before it.
var VariadicTrailingImportIDTypes = map[string]bool{
	"aws_security_group_rule": true,
}

// variadicTrailingGroup resolves p's trailing, variadic segment to the
// ratified family of sibling attributes resourceType's own identity-table
// row already names - see [VariadicTrailingImportIDTypes] for the
// provider-side verification this requires before a type may reach here at
// all, and [Component.SoleElement] for where the family itself was
// ratified (issue #369).
//
// The family is not rediscovered from the schema's own shape: knowing THAT
// cidr_blocks collides with the documented "cidr_block" segment does not
// say that ipv6_cidr_blocks, prefix_list_ids and source_security_group_id
// belong in the same variadic tail rather than being separate arguments
// that merely happen to be collections. Component.SoleElement's own Attrs
// list already answers that question, audited once against the provider's
// real schema (issue #369's own history), and reusing it here is reading
// domain knowledge this package already carries rather than guessing it
// again from naming shape alone.
//
// Every attribute the family names must be a top-level string, or a
// top-level list or set of strings, on the block actually served - the
// same "readable off the applied object" requirement every other segment
// in this file is held to. A family member the schema does not carry in one
// of those shapes makes the whole family unsafe to render this run, and the
// caller refuses rather than rendering a partial one.
func variadicTrailingGroup(resourceType string, b *configschema.Block, segmentName string) ([]string, bool) {
	if !VariadicTrailingImportIDTypes[resourceType] {
		return nil, false
	}
	row, ok := LookupType(resourceType)
	if !ok {
		return nil, false
	}
	plural := segmentName + "s"
	for _, comp := range row.Components {
		if !comp.SoleElement || len(comp.Attrs) < 2 {
			continue
		}
		named := false
		for _, attr := range comp.Attrs {
			if normalizeDocName(attr) == plural {
				named = true
				break
			}
		}
		if !named {
			continue
		}
		group := make([]string, 0, len(comp.Attrs))
		for _, attr := range comp.Attrs {
			a := b.Attributes[attr]
			if a == nil {
				return nil, false
			}
			if a.Type == cty.String {
				group = append(group, attr)
				continue
			}
			if (a.Type.IsListType() || a.Type.IsSetType()) && a.Type.ElementType() == cty.String {
				group = append(group, attr)
				continue
			}
			// Something in the ratified family is not a shape this file
			// knows how to render one token per element from; the family
			// as a whole is not safe to treat as variadic on this schema.
			return nil, false
		}
		return group, true
	}
	return nil, false
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
//
// # The variadic tail
//
// A grammar's LAST segment is a special case when it collides with a real
// plural collection attribute AND [VariadicTrailingImportIDTypes] has
// ratified resourceType for the reason that map's own doc comment states:
// rather than being inferred as the minted `id` (refused,
// [pluralCollectionCollision]'s ordinary outcome) or resolved to one
// attribute, it is resolved to variadicGroup - the ordered sibling
// attributes [variadicTrailingGroup] reads off the type's own identity-table
// row - and [LocatedComposedImportID] renders one token per element of
// whichever of them the applied object actually sets. Only the trailing
// segment may take this path: a variadic grammar is documented as
// "SOURCE[_SOURCE]*", always at the end, because a delimited string has no
// other way to say "this and the following N tokens are one family".
//
// # The any-of segment
//
// A segment that names no single argument can ALSO be the page's own way of
// describing several MUTUALLY EXCLUSIVE arguments in one place - aws_route's
// "DESTINATION" is the type this closes (GitHub issue #364): the Import
// section's own grammar is "ROUTETABLEID_DESTINATION", and "destination" is
// not one argument but whichever of destination_cidr_block,
// destination_ipv6_cidr_block or destination_prefix_list_id the route
// actually carries (an ExactlyOneOf on the real hashicorp/aws 6.59.0
// schema). Before this existed, such a segment fell through to the same "id
// inference" the case above makes for a genuinely absent segment - and for
// aws_route that inference is not a guess this package failed to
// disambiguate, it is PROVABLY WRONG: read directly from the provider's own
// source (hashicorp/terraform-provider-aws, internal/service/ec2/id.go and
// vpc_route.go, no terraform in the loop), `id` is
// `fmt.Sprintf("r-%s%d", routeTableID, create.StringHashcode(destination))`
// - an opaque hash, never the "ROUTETABLEID_DESTINATION" string the page
// documents and routeImportID.Parse expects back. [namedAlternativeGroup]
// is what recognises this shape instead of guessing `id`: the type's own
// ratified [Component] for the SAME import syntax already models the
// alternation on the config side ({Attrs: [destination_cidr_block,
// destination_ipv6_cidr_block, destination_prefix_list_id]}), and this
// reuses that same roster rather than re-deriving it, for
// [variadicTrailingGroup]'s own reason: knowing a segment's name collides
// with several schema attributes does not say WHICH several belong
// together, and the ratified row already answers that, audited once.
//
// Unlike the variadic tail, an any-of segment is not restricted to the
// grammar's last position - nothing about a single mutually-exclusive
// choice needs the "and everything after" grammar a delimited string
// reserves for a trailing family - but [namedAlternativeGroup]'s positional
// correlation against the ratified row still requires the two sides to
// agree on how many non-literal segments the import string has, so a
// mismatched row refuses rather than guesses which position lines up with
// which.
//
// resolveDocumentedImportID resolves resourceType's real value only at
// write time - alternatives is a set of CANDIDATE attribute names for that
// segment, not a resolved one, and [LocatedComposedImportID] is what picks
// the single populated member off the applied object, refusing rather than
// choosing when zero or more than one are set.
func resolveDocumentedImportID(resourceType string, b *configschema.Block) (parts []string, variadicGroup []string, alternatives [][]string, separator string, ok bool) {
	g, known := DocumentedImportIDs[resourceType]
	if !known || b == nil || g.Separator == "" || len(g.Parts) < 2 {
		return nil, nil, nil, "", false
	}

	byName := attrsByDocName(b)

	resolved := make([]string, len(g.Parts))
	claimed := make(map[string]bool, len(g.Parts))
	inferred := -1
	special := false
	for i, p := range g.Parts {
		attr, found := byName[p.Name]
		if !found {
			if p.Argument || special {
				// See this function's doc comment: a named configuration
				// argument the schema does not carry is a disagreement,
				// and a second unresolved segment is a coin toss - whether
				// the first one used the id-inference or the any-of route.
				return nil, nil, nil, "", false
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
				// provider's own importer refuses to parse.
				//
				// The concept the segment actually names - one element of
				// a list the configuration may set several of at once, or
				// several such lists at once - IS renderable when the
				// segment is the grammar's last one and the type is
				// ratified variadic; see this function's own doc comment
				// on the variadic tail and [variadicTrailingGroup].
				if i == len(g.Parts)-1 {
					if group, gok := variadicTrailingGroup(resourceType, b, p.Name); gok {
						return resolved[:i], group, nil, g.Separator, true
					}
				}
				return nil, nil, nil, "", false
			}
			if group, gok := namedAlternativeGroup(resourceType, i, len(g.Parts), b); gok {
				// See this function's doc comment, "The any-of segment":
				// the ratified row already models this position as several
				// mutually-exclusive arguments rather than one, so the
				// bare-id guess below is not the best available answer -
				// resolveAlternativeSegment picks the real one at write
				// time, off the applied object.
				if alternatives == nil {
					alternatives = make([][]string, len(g.Parts))
				}
				alternatives[i] = group
				special = true
				continue
			}
			inferred = i
			special = true
			continue
		}
		if claimed[attr] {
			return nil, nil, nil, "", false
		}
		claimed[attr] = true
		resolved[i] = attr
	}

	if inferred >= 0 {
		a, has := b.Attributes[locatedImportIDAttr]
		if !has || a == nil || a.Type != cty.String || claimed[locatedImportIDAttr] {
			return nil, nil, nil, "", false
		}
		resolved[inferred] = locatedImportIDAttr
	}

	return resolved, nil, alternatives, g.Separator, true
}

// namedAlternativeGroup reports the several attributes a documented import
// segment at index `position` (of `partCount` total, non-literal segments)
// may resolve to, when resourceType's own ratified identity-table row
// already models that SAME position as an ANY-OF over more than one
// argument - GitHub issue #364's aws_route is the type that exposed this;
// see [resolveDocumentedImportID]'s "any-of segment" doc section for the
// full account of why bare `id` is provably wrong for it.
//
// The correlation is POSITIONAL, not by name: [DocumentedImportIDs]' Parts
// and the ratified row's own [TypeIdentity.Components] - with pure-Literal
// separators dropped - describe the SAME documented import string, built by
// two independent tools from two independent sources (docimportid-gen
// scrapes the Import section's prose; row-gen ratifies table.go's
// Components from the same page plus the schema), so a row whose
// non-literal component count matches the documented part count names each
// documented segment's real candidate set at the identical index. A row
// with a different shape - fewer or more non-literal components than the
// grammar has segments - refuses rather than guesses which position lines
// up with which.
//
// Only a component whose Attrs names more than one attribute, and which is
// neither [Component.SoleElement] (a list collapsed to its one element,
// aws_security_group_rule's own different shape) nor [Component.PerElement]
// (one segment per element of a set), is this shape: a bare, ordinary
// alternation where the CONFIG side already picks "whichever the
// configuration set" and this function's caller must instead pick
// "whichever the APPLIED OBJECT set".
//
// Every candidate the group names must be a top-level string attribute on
// the schema actually served, or the whole group is unsafe to render this
// run - [variadicTrailingGroup]'s own reason.
func namedAlternativeGroup(resourceType string, position, partCount int, b *configschema.Block) ([]string, bool) {
	row, ok := LookupType(resourceType)
	if !ok {
		return nil, false
	}
	var attrComps []Component
	for _, c := range row.Components {
		if len(c.Attrs) > 0 {
			attrComps = append(attrComps, c)
		}
	}
	if len(attrComps) != partCount || position < 0 || position >= len(attrComps) {
		return nil, false
	}
	comp := attrComps[position]
	if len(comp.Attrs) < 2 || comp.SoleElement || comp.PerElement {
		return nil, false
	}
	group := make([]string, 0, len(comp.Attrs))
	for _, attr := range comp.Attrs {
		a := b.Attributes[attr]
		if a == nil || a.Type != cty.String {
			return nil, false
		}
		group = append(group, attr)
	}
	return group, true
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
// order it named them, followed by variadicGroup's rendering when
// [resolveDocumentedImportID] resolved a variadic tail (nil otherwise).
// alternatives, when non-nil, names - for the SAME indices as parts - the
// several candidate attributes a position resolves to when
// [namedAlternativeGroup] found an any-of segment there instead of a single
// named one; parts[i] is then empty and resolveAlternativeSegment picks the
// one candidate the applied object actually populated.
//
// It is the write-back counterpart of that resolution, and it is
// all-or-nothing for [LocatedIdentity]'s reason: a string missing a segment
// is not a shorter identity, it is a different one, and it would be handed to
// a later import as though it were whole. For the variadic tail, "missing a
// segment" means an element [locatedVariadicSegments] cannot safely read -
// unknown, marked, or of an unrecognised shape - refuses the object outright
// rather than dropping just that element, for the identical reason: a
// shortened tail is a different, not merely a smaller, identity. An entirely
// ABSENT family member (the configuration never set it, so the applied
// object reads it back null) is not that failure and contributes zero
// segments, because [Component.SoleElement]'s own family is `AtLeastOneOf`
// on the real provider schema - some members being unset is the ordinary
// case for every instance, not a defect in this one.
//
// An any-of segment is held to the SAME all-or-nothing rule from the other
// direction: [resolveAlternativeSegment] refuses when zero or more than one
// candidate is populated, because picking between two live candidates, or
// inventing a value for none, is exactly the wrong identity this whole
// mechanism exists to never write.
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
func LocatedComposedImportID(obj cty.Value, parts []string, variadicGroup []string, alternatives [][]string, separator string) (string, bool) {
	if separator == "" || (len(parts) == 0 && len(variadicGroup) == 0) {
		return "", false
	}
	segments := make([]string, 0, len(parts)+len(variadicGroup))
	for i, name := range parts {
		if i < len(alternatives) && alternatives[i] != nil {
			v, ok := resolveAlternativeSegment(obj, alternatives[i])
			if !ok || strings.Contains(v, separator) {
				return "", false
			}
			segments = append(segments, v)
			continue
		}
		v, ok := locatedAttrSegment(obj, name)
		if !ok || strings.Contains(v, separator) {
			return "", false
		}
		segments = append(segments, v)
	}
	tailTokens := 0
	for _, name := range variadicGroup {
		vals, ok := locatedVariadicSegments(obj, name)
		if !ok {
			return "", false
		}
		for _, v := range vals {
			if strings.Contains(v, separator) {
				return "", false
			}
			segments = append(segments, v)
			tailTokens++
		}
	}
	if len(variadicGroup) > 0 && tailTokens == 0 {
		// Every family member came back absent. The grammar names a
		// variadic tail because the documented import ID requires at
		// least one source token, so a string with none - however many
		// FIXED segments precede it - is missing the segment the
		// provider's own importer requires, not a shorter identity for a
		// type that happens to have none.
		return "", false
	}
	if len(segments) < 2 {
		return "", false
	}
	return strings.Join(segments, separator), true
}

// resolveAlternativeSegment picks the single populated attribute out of a
// [namedAlternativeGroup]-shaped set of candidates on an applied object,
// refusing - rather than guessing - when zero or more than one member is
// populated.
//
// This is HANDOFF's safety rule applied to a WRITE rather than a read: a
// wrong identity is silent, and choosing between two live candidates would
// produce exactly that with no way for a later reader to tell. Every type
// this reaches today (aws_route, per [resolveDocumentedImportID]'s doc
// comment) documents the group as mutually exclusive on the real provider
// schema (ExactlyOneOf), so "more than one populated" is not a shape the
// provider itself should ever produce - refusing it anyway, rather than
// picking the first match, is what keeps a future schema change from
// silently starting to guess.
func resolveAlternativeSegment(obj cty.Value, group []string) (string, bool) {
	value := ""
	found := false
	for _, name := range group {
		v, ok := locatedAttrSegment(obj, name)
		if !ok {
			continue
		}
		if found {
			return "", false
		}
		value = v
		found = true
	}
	if !found {
		return "", false
	}
	return value, true
}

// locatedVariadicSegments is [locatedAttrSegment]'s counterpart for one
// member of a [variadicTrailingGroup] family: it renders zero tokens for an
// absent (null) member, one token for a scalar string member, or one token
// per element for a top-level list or set of strings - preserving the
// list's OWN element order rather than sorting it, because
// cidr_blocks/ipv6_cidr_blocks/prefix_list_ids are schema.TypeList on the
// real provider schema (order-preserving, not a set), and
// [VariadicTrailingImportIDTypes]' own doc comment is where that was
// confirmed against the provider's source.
//
// An unknown or marked value - the member's own value or, for a
// collection, any one element of it - refuses the whole render rather than
// guessing or unmarking it, the same posture [locatedAttrSegment] takes:
// this function is called only on an APPLIED object, where every value
// should already be known, and a marked value must never flow into an
// identity component or be forcibly unmarked to make one.
func locatedVariadicSegments(obj cty.Value, name string) (segs []string, ok bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(name) {
		return nil, false
	}
	v := obj.GetAttr(name)
	if v.IsNull() {
		// This family member was never set. [Component.SoleElement]'s own
		// family is AtLeastOneOf on the real schema, never AllOf, so this
		// is the ordinary case for every member but the ones actually set.
		return nil, true
	}
	if !v.IsKnown() || v.IsMarked() {
		return nil, false
	}
	if v.Type() == cty.String {
		s := v.AsString()
		if s == "" {
			return nil, true
		}
		return []string{s}, true
	}
	if !v.Type().IsListType() && !v.Type().IsSetType() {
		return nil, false
	}
	out := make([]string, 0, v.LengthInt())
	for it := v.ElementIterator(); it.Next(); {
		_, ev := it.Element()
		if ev.IsNull() || !ev.IsKnown() || ev.IsMarked() || ev.Type() != cty.String {
			return nil, false
		}
		s := ev.AsString()
		if s == "" {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}
