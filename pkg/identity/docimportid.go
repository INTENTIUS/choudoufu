// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import "strings"

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
