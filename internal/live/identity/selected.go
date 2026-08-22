// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the operator's way into the mechanism located.go built.
//
// [LocatedType] answers "may this type go the record-located route because
// it has nowhere else to go": its first condition is membership in
// [MarkerlessTypes] with no ratified row, which is a fact about the type,
// not a choice anybody made. HANDOFF.md's third principle is the other
// direction - an operator with a tag budget or a tag policy CHOOSES to hold
// an identity in a record for a type that could perfectly well carry a
// marker - and that is what [SelectedLocatedRefusal] answers.
//
// # What the selected route skips, and what it must not
//
// It skips exactly the schema-independent membership test, condition 1: the
// type is markerless and has no row. That test is what makes the automatic
// route automatic, and it is the only thing an operator's explicit choice
// replaces. Everything a wrong identity could come from stays:
//
//	condition 0  [NotImportable]. A located record exists so a LATER run can
//	             import the object back. A type the provider will not import
//	             cannot be served by any record, chosen or not, and admitting
//	             one trades a plan refusal for an apply-time failure with the
//	             object already live (issue #331).
//
//	condition 3  [LocatedIdentityPlanFor] recordable. The record must be able
//	             to hold the WHOLE identity. Recording part of a composite one
//	             is a wrong identity wearing the shape of a missing one, and
//	             [IDNotProvenWholeTypes] - which is derived over every type in
//	             the scraped import grammar, not only the markerless ones, for
//	             the reason tools/row-gen's idnotwhole.go states - is what
//	             refuses the types whose exported `id` no source proves whole.
//
// Neither is weakened for any type, selected or not. That is HANDOFF.md's
// safety rule: a refusal is loud and reversible, a wrong identity is silent
// and adopts or displaces a real object.
//
// # Why condition 2 is replaced rather than kept
//
// [LocatedType]'s condition 2 refuses a type any of whose schema attributes
// is Sensitive. For the AUTOMATIC route that is the right blanket: those
// types have no other route, so admitting one is this fork's decision to
// manage credential material that a refusal would otherwise have kept out.
//
// A SELECTED type is already admitted, by its ratified row, and already
// applied with every one of its attributes. Routing it through a record
// changes where the answer to "which object is this" comes from - a stored
// identity instead of a tag sweep - and nothing else: both paths hand the
// same import identity to the same importAndRead, so the same attributes
// come back and the same ones do not. Keeping the blanket would refuse a
// selection on the strength of an attribute the selection does not touch.
//
// What the selection DOES newly write is the identity itself, into the
// estate's record store, in clear. So the rule that replaces the blanket is
// the blanket narrowed to exactly that: the attributes the record would
// hold. It is the case the issue #365 credential-material census found and
// named - "its recorded identity IS the secret" - derived from the same
// Sensitive-and-not-Deprecated evidence rule, over the attributes
// [LocatedIdentityPlanFor] just said would be recorded.
//
// It names no resource type, here or in anything it reads.

// SelectionFor is the root module's `markers "record"` selection, or nil
// when the configuration declares none.
//
// It is read from the configuration by every layer that acts on it - this
// package's resolver, internal/live/lint, internal/live/stamp,
// internal/live/projection's write-back - rather than threaded from one, for
// [recordStoreConfiguredIn]'s reason: the four must never disagree about
// which resources are selected, and a value passed between them is one more
// thing a call site can forget. The configuration is the authority and each
// layer asks it directly.
//
// Only the ROOT module's live block is read. A live block in a child module
// is refused outright by internal/live/lint's RuleChildLiveConfig, and every
// other consumer of a live block already reads the root's only.
//
// Addresses that do not parse are dropped, which selects LESS rather than
// more: an unusable entry leaves its resource marked exactly as it was.
// internal/live/lint refuses each one by name, so a run never reaches here
// with a broken list unnoticed.
func SelectionFor(cfg *configs.Config) *strict.Selection {
	types, addresses, ok := selectionLists(cfg)
	if !ok {
		return nil
	}
	sel, _ := strict.ParseSelection(types, addresses)
	return sel
}

// selectionLists is the raw `markers "record"` lists from cfg's root module,
// with ok false when there is no such block. Split out so internal/live/lint
// can re-parse with the problems, which [SelectionFor] discards.
func selectionLists(cfg *configs.Config) (types, addresses []string, ok bool) {
	if cfg == nil || cfg.Module == nil || cfg.Module.Live == nil || cfg.Module.Live.Strict == nil {
		return nil, nil, false
	}
	m := cfg.Module.Live.Strict.MarkersRecord
	if m == nil {
		return nil, nil, false
	}
	return m.Types, m.Addresses, true
}

// SelectedLocatedType reports whether an operator's `markers = record`
// selection may be honoured for resourceType: the record can hold its whole
// identity and a later run can import the object back from it.
//
// See [SelectedLocatedRefusal] for which conditions are checked and which
// one of [LocatedType]'s four is deliberately not.
func SelectedLocatedType(resourceType string, schemas map[string]providers.Schema) bool {
	return SelectedLocatedRefusal(resourceType, schemas) == ""
}

// SelectedLocatedRefusal is the reason an operator's `markers = record`
// selection cannot be honoured for resourceType, or "" when it can.
//
// The sentence names the condition that failed, because "this type cannot"
// with no reason is a refusal an operator cannot act on and the two
// conditions have opposite next steps: a type the provider will not import
// is a permanent no, and a type whose `id` is not proven whole is a
// documentation gap that a provider release or a scrape can close.
//
// # Failing closed
//
// schemas is what the run holds. No schema for this type means the two
// schema-read conditions cannot run, and a predicate that cannot run must
// not admit - the same direction [LocatedType] fails in, for the same
// reason. The consequence is worth stating because it is what keeps the
// layers consistent: with no schema the selection is not honoured, so
// internal/live/stamp writes the marker exactly as it always did and the
// resolver classifies the instance exactly as it always did. A run with no
// schemas gets today's behavior, not a half-applied selection.
func SelectedLocatedRefusal(resourceType string, schemas map[string]providers.Schema) string {
	if NotImportable(resourceType) {
		return fmt.Sprintf(
			"the provider does not support importing %s. A record holds an identity so that a LATER run can import the object back, so a type that cannot be imported cannot be found again from any record - the marker is the only handle it will ever have, and withholding it would create objects no run can see.",
			resourceType,
		)
	}

	schema, ok := schemas[resourceType]
	if !ok || schema.Block == nil {
		return fmt.Sprintf(
			"this run holds no schema for %s, and whether a record can hold its whole identity is a question only the schema answers. The selection is not applied: %s keeps its ownership marker.",
			resourceType, resourceType,
		)
	}

	plan, recordable := LocatedIdentityPlanFor(resourceType, schema)
	if !recordable {
		return fmt.Sprintf(
			"a record cannot hold the whole identity of a %s. Its documented import string is composite and nothing proves the exported \"id\" attribute is the whole of it, so a record written from \"id\" would hold a fragment - and a fragment read back by a later import is a WRONG identity, not a missing one. Leave this resource its ownership marker.",
			resourceType,
		)
	}

	if attr := sensitiveIdentityAttr(plan, schema); attr != "" {
		return fmt.Sprintf(
			"the identity a record would hold for a %s is its %q attribute, which the provider marks sensitive. Writing it to the estate's record store would put secret material there in clear - and unlike a secret this fork is asked to KEEP, which strict { secrets = \"store\" } covers, this one is what a later run would import BY, so a store that cannot be read in the clear leaves the resource unfindable. Leave this resource its ownership marker.",
			resourceType, attr,
		)
	}

	return ""
}

// sensitiveIdentityAttr names the first attribute a located record would
// hold for this type that the provider marks Sensitive and does not mark
// Deprecated, or "" when none is.
//
// The evidence rule is [credentialMaterial]'s, deliberately: Sensitive minus
// Deprecated, for that function's own stated reason. What differs is the
// SCOPE - every attribute reachable from the block there, only the ones
// [LocatedIdentityPlanFor] just said would be written here - and this file's
// header is why.
//
// Only top-level attributes are consulted, which is not a narrowing: a
// located record's every form reads top-level string attributes off the
// applied object (see [LocatedImportID] and [LocatedIdentity]), so an
// attribute nested inside a block or an object type is not one this can
// record.
func sensitiveIdentityAttr(plan LocatedIdentityPlan, schema providers.Schema) string {
	names := []string{locatedImportIDAttr}
	switch {
	case plan.Composite():
		names = plan.Components
	case plan.Composed():
		names = plan.ImportIDParts
	}
	for _, name := range names {
		a := schema.Block.Attributes[name]
		if a != nil && a.Sensitive && !a.Deprecated {
			return name
		}
	}
	return ""
}
