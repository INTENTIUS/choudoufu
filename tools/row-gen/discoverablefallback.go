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

// This file is GitHub issue #289's derived gate: which ADMITTED types may
// answer an identity-VALUE failure with the ownership marker a migrated
// estate already carries, rather than with a hard refusal.
//
// internal/live/identity's resolver already treats one population this
// way - [identity.TypeIdentity.ServerAssigned] rows classify
// ClassNeedsDiscovery before a single configuration attribute is read,
// because the provider mints the value and there is nothing in
// configuration to try. The remaining admitted rows - the ones this file's
// roster reaches - build their identity from configuration when they can,
// and used to hard-refuse the instant a component would not fold: a
// sensitive value, an impure function, a reference that is not an identity
// attribute, and a dozen shapes like them (see
// internal/live/identity/markerfallback.go's own refusal table). Every one
// of those refusals is asking the ADOPTION question - can a stranger's
// unmarked configuration be taken over - of an estate that already
// answered it at create time: internal/live/stamp writes tofu-estate and
// tofu-address onto every taggable managed resource, reading taggability
// off the provider schema, so a migrated estate's copy of this resource
// already carries the marker discovery needs.
//
// A type belongs in the roster on three independent facts, none of them a
// type name:
//
//   - ADMITTED: it has a ratified row (tools/row-gen/ratified.json), i.e.
//     it is in the table [resolve.go] actually consults.
//   - TAGGABLE: live/survey-full.json's own taggable signal is true - the
//     same predicate markerless.go reads, and the same one
//     internal/live/stamp applies at run time. Untaggable types are a
//     different question entirely: their identity either already resolves
//     from configuration (nothing to fix) or markerless.go's veto already
//     covers them (no marker to fall back on).
//   - ENUMERABLE: discovery can actually find the object once it decides
//     to look, through a native list resource (live/survey-full.json's
//     own list_resource signal) or a CloudFormation list handler
//     (live/registry.json's handlers.list, joined through
//     live/mapping.json's tf_type -> cfn_type). A type with neither is
//     admitted and taggable but plan-time-undiscoverable: classifying it
//     ClassNeedsDiscovery would trade a lint refusal that names the type
//     for a plan-time discovery error nobody could see coming, which is
//     the exact failure mode markerless-veto narrowing was reverted for.
//     Measured at 93910f4f49 (issue #289's own population): 221 admitted
//     taggable types build their identity from configuration rather than
//     from a server-assigned value, and 23 of those 221 are enumerable by
//     neither route. Those 23 must stay refused, in configuration
//     language, exactly as they refuse today.
//
// The enumerability join deliberately reads live/mapping.json's OWN
// cfn_type field for each type rather than [proposal.CFNType]/
// [proposal.Enumeration] filled in for a via:fold row: a fold row shares
// its CloudFormation identity with a different TF type (its fold parent)
// and [classifyFold] never sets Enumeration at all, leaving it at its zero
// value - which this file's own enumerability check already treats as "not
// listable", so the two sources agree for every fold row without this file
// needing to know that classifyFold exists. Measured at 93910f4f49: zero of
// the 221 candidates are via:fold, so today the two read identically; the
// zero-value default is what keeps that true if one ever is.

// discoverableFallbackReason is why every type in [identity.DiscoverableFallbackTypes]
// may answer a failed configuration-derived identity with the marker.
const discoverableFallbackReason = "the type's identity does not fold from its own configuration, but the type is taggable and a live listing can find it - through a native list resource or a CloudFormation list handler - so the tofu-address marker a migrated estate already carries answers what configuration alone could not"

// cfnListable reports whether p's registry evidence says this type's CFN
// model can be listed, at all or scoped to a parent. It folds
// [enumerationStory]'s two positive outcomes together because both mean
// discovery has a live listing to search - only "not listable" and the
// zero value (no registry evidence at all, including every via:fold row)
// mean it does not.
func cfnListable(p proposal, hasProposal bool) bool {
	if !hasProposal {
		return false
	}
	return p.Enumeration == "list-free" || p.Enumeration == "parent-input"
}

// discoverableFallbackEnumerable reports whether discovery has ANY live
// listing to search for this type: survey-gen's own native list-resource
// signal, or the CloudFormation route [cfnListable] reads.
func discoverableFallbackEnumerable(s surveyEntry, p proposal, hasProposal bool) bool {
	return s.Signals.ListResource || cfnListable(p, hasProposal)
}

// discoverableFallbackRoster is every admitted type [markerFallback] (see
// internal/live/identity/markerfallback.go) may answer with the marker:
// admitted, taggable, config-derived (not ServerAssigned, not
// RecordBacked), and enumerable. See this file's own doc comment for what
// each condition rules out and why.
//
// It iterates ratified - the corpus that decides admission, per emit.go's
// own doc comment and issue #263 - rather than survey or the mapped set, so
// a type outside the admitted table can never appear here no matter what
// its signals say.
func discoverableFallbackRoster(ratified map[string]identity.TypeIdentity, survey map[string]surveyEntry, proposals []proposal) []string {
	byType := indexByType(proposals)
	var out []string
	for typeName, entry := range ratified {
		if entry.ServerAssigned || entry.RecordBacked {
			continue
		}
		s, ok := survey[typeName]
		if !ok || !s.Signals.Taggable {
			continue
		}
		p, hasProposal := byType[typeName]
		if !discoverableFallbackEnumerable(s, p, hasProposal) {
			continue
		}
		out = append(out, typeName)
	}
	sort.Strings(out)
	return out
}

// discoverableFallbackTableRel is the generated roster's home, beside
// markerless_generated.go for the same reason that file sits beside the
// identity table: it answers a question about the same admitted rows.
const discoverableFallbackTableRel = "internal/live/identity/discoverablefallback_generated.go"

// renderDiscoverableFallbackFile renders internal/live/identity's marker-
// fallback roster.
func renderDiscoverableFallbackFile(types []string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(licenseHeader)
	b.WriteString("\n")
	b.WriteString(emitGeneratedByComment)
	b.WriteString("\n\n")
	b.WriteString("package identity\n\n")
	b.WriteString(discoverableFallbackDoc)
	fmt.Fprintf(&b, "const DiscoverableFallbackReason = %q\n\n", discoverableFallbackReason)
	b.WriteString(discoverableFallbackTypesDoc)
	b.WriteString("var DiscoverableFallbackTypes = map[string]struct{}{\n")
	for _, t := range types {
		fmt.Fprintf(&b, "%q: {},\n", t)
	}
	b.WriteString("}\n")
	return format.Source([]byte(b.String()))
}

// discoverableFallbackDoc is DiscoverableFallbackReason's own doc comment,
// carried by the generator for the reason defaultTableDoc is.
const discoverableFallbackDoc = `// DiscoverableFallbackReason is why every type in
// [DiscoverableFallbackTypes] may answer a failed configuration-derived
// identity with the tofu-address marker instead of refusing. It is one
// ruling covering the whole set, not a summary of many.
`

// discoverableFallbackTypesDoc is DiscoverableFallbackTypes' own doc
// comment.
const discoverableFallbackTypesDoc = `// DiscoverableFallbackTypes is every admitted provider resource type whose
// identity is built from configuration (not [TypeIdentity.ServerAssigned],
// not [TypeIdentity.RecordBacked]) but which may still fall back to the
// tofu-address marker when configuration does not settle it: the type is
// taggable, per live/survey-full.json's own signal, and enumerable through
// either a native list resource or a CloudFormation list handler - see
// [DiscoverableFallbackReason] and GitHub issue #289.
//
// [resolver.markerFallback] is the sole reader: it converts one of a fixed
// set of identity-VALUE refusals (see markerfallback.go's own refusal
// table) into [ClassNeedsDiscovery] with [DiscoveryMarkerFallback] for a
// type in this set, and leaves every other type refusing exactly as
// before. A type absent from this set - untaggable, or taggable but
// unlistable by either route - never reaches that fallback; it keeps
// refusing in configuration language, the same as it always has.
//
// The set is derived on every generator run from live/survey-full.json's
// taggability and native-list signals and live/registry.json's
// CloudFormation list handler, joined through live/mapping.json. Nothing
// here is maintained by hand.
`
