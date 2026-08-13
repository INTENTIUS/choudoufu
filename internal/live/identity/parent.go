// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The parent-read sweep leg (issue #60): an untaggable admitted type cannot
// carry an ownership marker of its own, but several of them do not need
// one, because their whole identity is already built out of an admitted
// parent's own identity - a bucket policy's "bucket" argument is the same
// string as the bucket's own identity, a KMS-style child that reads a role
// or a target group is the same shape. Reading the parent - which is
// admitted and, being taggable, discoverable by marker on its own - answers
// "does this child exist" with no memory of the child required.
//
// This file is the mechanical half: which untaggable admitted types have
// that shape, and through which of their own arguments. It answers the
// question from data this package already carries (DefaultTable's
// Components), not from a second, hand-maintained list - the whole point
// raised by live/LIMITATIONS.md's "Untaggable types cannot be removed by the
// sweep" entry growing by hand at every admission batch. See
// tools/survey-gen/parent_render.go for where this feeds the doc, and
// internal/live/discovery's parent-read sweep leg for where it feeds a run.
package identity

import (
	"sort"
	"strings"
)

// ParentLink is one way a type's identity Components read a value that is
// also how another admitted type identifies itself.
type ParentLink struct {
	// Attr is the argument on the child's own resource block that supplies
	// the value - the config argument one of its identity [Component]s
	// reads.
	Attr string

	// Parent is the admitted type whose identity this argument's value
	// matches.
	Parent string
}

// ParentOf reports every parent link typeName's identity Components carry,
// restricted to the types named true in parents - the caller's answer to
// "which admitted types are themselves taggable", since a parent that
// cannot carry an ownership marker cannot anchor a read either, and this
// package touches no provider schema to find out on its own (see the
// package doc's "cloud-free and process-free"). Callers with schemas to
// hand compute it live (a top-level tags argument, [markerCapable] in
// internal/live/discovery's terms); tools/survey-gen computes the same fact
// from live/survey-full.json's taggable signal. Both land on the same
// answer for the admitted table as it stands today; this package asserts
// neither and takes the set as given.
//
// A ServerAssigned type, or one absent from DefaultTable, has no Components
// and so no links - every entry here is one of the "Composed identities" or
// "Client-named" rows.
//
// Two conventions decide a match, tried in order so that a type whose name
// extends several admitted types' names (aws_iam_role_policy_attachment
// extends both aws_iam_role and the untaggable aws_iam_role_policy) never
// picks the wrong one:
//
//  1. typeName's own name begins with an eligible parent's name plus an
//     underscore (aws_s3_bucket_policy begins with aws_s3_bucket_). This is
//     the more specific test - a fact about two whole type names rather
//     than about one argument's spelling - so it is tried first, and the
//     longest matching parent name wins when more than one is a prefix.
//     Once a parent is named this way, the attribute reported is whichever
//     of the type's own Components literally supplies that parent's own
//     IdentityAttrs, or follows the naming convention in rule 2 for it.
//  2. Failing that, an individual argument follows the *_id / *_arn / *_url
//     naming convention against an eligible parent's name on its own
//     (aws_route's route_table_id, aws_route53_record's zone_id) - the same
//     rule tools/survey-gen/classify.go's parentRef applies to the whole
//     provider roster, here narrowed to the admitted, eligible subset and
//     to the shortest matching parent name.
//
// A literal identity-attribute match ("arn" naming both a component and an
// eligible parent's IdentityAttrs) is deliberately never tried against every
// eligible parent at once: "arn" and "name" are common enough across the
// admitted table that picking whichever type happens to sort first would be
// a guess, not a derivation. It is only used, in rule 1, to confirm the
// *argument* once the type-name convention has already named the *parent*.
func ParentOf(typeName string, parents map[string]bool) []ParentLink {
	self, ok := DefaultTable[typeName]
	if !ok || self.ServerAssigned || len(self.Components) == 0 {
		return nil
	}

	if parent, ok := parentByTypeName(typeName, parents); ok {
		if attr, ok := attrFor(self, parent); ok {
			return []ParentLink{{Attr: attr, Parent: parent}}
		}
		// The type name says there is a parent, but no component's argument
		// can be pinned to it: report nothing rather than a guess at which
		// one it might be.
		return nil
	}

	seen := map[string]bool{}
	var out []ParentLink
	for _, c := range self.Components {
		for _, attr := range c.Attrs {
			parent, ok := parentByConvention(attr, typeName, parents)
			if !ok || seen[parent] {
				continue
			}
			seen[parent] = true
			out = append(out, ParentLink{Attr: attr, Parent: parent})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Parent < out[j].Parent })
	return out
}

// SingleParentComponent reports the one case ParentOf's callers can act on
// without a second per-type judgment: typeName's identity Components carry
// exactly one attribute-supplying component, and it is the one ParentOf
// resolves to a parent. That is the "named-singleton child" shape
// table.go's own comments already name for aws_s3_bucket_policy and
// aws_sns_topic_policy - a type whose whole identity is the parent's, with
// nothing else the child chooses - and it is the shape a parent read alone
// settles the child's identity for, with no further argument to guess at.
//
// A type with a second attribute-supplying component (aws_iam_role_policy's
// own policy name, aws_route's destination) is excluded even when one of
// its components does link to a parent: reading the parent answers what the
// child's parent-half is, not what its other, free-standing half is, so
// existence still is not fully determined by the parent alone.
func SingleParentComponent(typeName string, parents map[string]bool) (ParentLink, bool) {
	self, ok := DefaultTable[typeName]
	if !ok || self.ServerAssigned {
		return ParentLink{}, false
	}
	var attrComponents int
	for _, c := range self.Components {
		if len(c.Attrs) > 0 {
			attrComponents++
		}
	}
	if attrComponents != 1 {
		return ParentLink{}, false
	}
	links := ParentOf(typeName, parents)
	if len(links) != 1 {
		return ParentLink{}, false
	}
	return links[0], true
}

// parentReadRemovable is the single-parent-component types this pass also
// trusts to remove once a parent read finds the child, rather than only
// report it. It is the one hand-curated fact this file allows itself: given
// a type, [SingleParentComponent] already derives that a parent read hands
// back the child's whole identity - what it cannot derive is whether the
// provider's own read of that identity distinguishes "the child exists"
// from "the child does not", which is a fact about the provider's API
// shape rather than about the identity table. See
// live/LIMITATIONS.md, "Some untaggable types are swept via a parent read
// instead", for the operator-facing version of the same distinction.
//
// aws_s3_bucket_policy is the only entry today. S3's GetBucketPolicy
// returns a clean NoSuchBucketPolicy when a bucket carries none, so "found"
// and "not found" are the two unambiguous answers a removal sweep needs,
// and the bucket name - already known from the parent - is the policy's
// whole identity end to end; internal/live/discovery's gated e2e exercises
// exactly this against floci. The rest of [SingleParentComponent]'s set
// stays report-only for now: aws_sns_topic_policy and aws_sqs_queue_policy
// are structurally identical but unverified here against a real
// GetTopicAttributes/GetQueueAttributes "no policy" response, and the
// other four S3 bucket children each carry their own question - versioning
// and default server-side encryption both have a provider-side default
// state, so a read that "finds" one does not by itself mean a
// configuration ever declared it, the same ambiguity
// live/LIMITATIONS.md's import-derived-state entry already names for other
// types. Broadening this set is follow-on work, one provider-behavior
// verification at a time, not a blanket promotion of everything
// [SingleParentComponent] allows.
var parentReadRemovable = map[string]bool{
	"aws_s3_bucket_policy": true,
}

// ParentReadRemovable reports whether a parent read for typeName may also
// remove the child it finds, rather than only report it. False for every
// type outside the small set above, including one where
// [SingleParentComponent] is true - being fully identified by the parent is
// necessary for removal and is not, on its own, sufficient.
func ParentReadRemovable(typeName string) bool {
	return parentReadRemovable[typeName]
}

// foldParentTypes is issue #68's fold-child extension: a declared
// property-child whose whole identity is a verbatim duplicate of an
// already-admitted parent's own identity Components - not one scalar
// argument the way [SingleParentComponent] requires, but the parent's whole
// composite tuple. aws_api_gateway_integration is the shape this batch
// proves: its Components (rest_api_id, resource_id, http_method) are
// exactly aws_api_gateway_method's own three, so rendering the method's
// identity - even though the method is untaggable and itself
// [ClassParentDerived] rather than concrete, which is what puts this case
// outside [SingleParentComponent]'s and [ParentOf]'s "eligible, taggable
// parent" model entirely - settles the integration's identity completely
// too.
//
// This is the one hand-curated fact this file allows itself for the shape,
// the same discipline [parentReadRemovable] holds itself to: a type belongs
// here only once someone has checked, component by component against
// live/survey-full.json or the provider's documented Import section (see
// internal/live/identity/table.go's "Fold-children" section comment), that
// the child's Components are exactly the parent's own and nothing more. A
// fold-child needing even one further argument the parent's identity does
// not supply (aws_api_gateway_integration_response's own status_code,
// aws_api_gateway_method_settings's own method_path) is deliberately absent:
// a parent read alone cannot recover an argument that lives nowhere but the
// child's own deleted block, so there is no honest entry to add for it here,
// and removal-sweep coverage for those stays the same accepted gap
// live/LIMITATIONS.md's "Untaggable types cannot be removed by the sweep"
// entry already carries for aws_api_gateway_method itself. Broadening this
// map is follow-on work, one type at a time, the same as
// [parentReadRemovable]'s own growth rule.
var foldParentTypes = map[string]string{
	"aws_api_gateway_integration": "aws_api_gateway_method",
}

// FoldParentOf reports the admitted parent type whose own identity
// Components, once rendered, are typeName's whole identity too - the
// [foldParentTypes] map above. Unlike [ParentOf] and [SingleParentComponent],
// this is not derived from typeName's own Components at request time: the
// parent named here may itself be untaggable and [ClassParentDerived]
// (composite, resolved only once its own parents' live values are known),
// which is exactly the case [SingleParentComponent]'s "taggable eligible
// parent" model cannot express, so there is nothing to derive it from short
// of the hand check the map's own doc comment describes.
func FoldParentOf(typeName string) (string, bool) {
	parent, ok := foldParentTypes[typeName]
	return parent, ok
}

// FoldParentTypes returns the [foldParentTypes] map's own keys, sorted -
// every type this pass's fold-child leg (internal/live/discovery's
// foldChildReadSweep) acts on.
func FoldParentTypes() map[string]string {
	out := make(map[string]string, len(foldParentTypes))
	for k, v := range foldParentTypes {
		out[k] = v
	}
	return out
}

// parentByTypeName reports the longest eligible admitted type whose name,
// plus an underscore, prefixes typeName's own - rule 1 of [ParentOf]'s doc
// comment.
func parentByTypeName(typeName string, parents map[string]bool) (string, bool) {
	best := ""
	for t := range parents {
		if !parents[t] || t == typeName {
			continue
		}
		if _, ok := DefaultTable[t]; !ok {
			continue
		}
		if strings.HasPrefix(typeName, t+"_") && len(t) > len(best) {
			best = t
		}
	}
	return best, best != ""
}

// attrFor finds which of self's identity Components supplies parent's
// value, once parent is already fixed (by [parentByTypeName]) so there is
// no ambiguity left to resolve: the first component whose argument either
// literally names one of parent's IdentityAttrs, or follows the *_id /
// *_arn / *_url convention for parent's own name.
func attrFor(self TypeIdentity, parent string) (string, bool) {
	p, ok := DefaultTable[parent]
	if !ok {
		return "", false
	}
	for _, c := range self.Components {
		for _, attr := range c.Attrs {
			if p.hasIdentityAttr(attr) {
				return attr, true
			}
			if got, ok := parentByConvention(attr, self.Type, map[string]bool{parent: true}); ok && got == parent {
				return attr, true
			}
		}
	}
	return "", false
}

// parentByConvention reports the eligible admitted type an argument name
// follows the AWS foreign-key convention for: strip a trailing _id, _arn or
// _url (or leave the name whole, for the bare-noun style "bucket" and
// "role" use), then look for an eligible type whose own name is exactly
// "aws_" plus what is left, or ends with an underscore plus it. Several
// candidates ending the same way is resolved by shortest name, the same
// tie-break tools/survey-gen/classify.go's parentRef uses over the whole
// provider roster - rule 2 of [ParentOf]'s doc comment.
func parentByConvention(attr, self string, parents map[string]bool) (string, bool) {
	base := attr
	for _, suf := range []string{"_id", "_arn", "_url"} {
		if trimmed, ok := strings.CutSuffix(attr, suf); ok {
			base = trimmed
			break
		}
	}
	if base == "" {
		return "", false
	}

	best := ""
	for t := range parents {
		if !parents[t] || t == self {
			continue
		}
		if _, ok := DefaultTable[t]; !ok {
			continue
		}
		if t == "aws_"+base {
			return t, true
		}
		if strings.HasSuffix(t, "_"+base) && (best == "" || len(t) < len(best)) {
			best = t
		}
	}
	return best, best != ""
}
