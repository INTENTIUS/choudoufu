// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the record store answering the OTHER question (issue #270).
//
// A record-BACKED resource has no cloud object at all: the record is the
// object, and deleting the record is deleting the thing. That is why
// builder.discoverOrphanedRecords may propose destroying a kind=object key
// with no configuration behind it - for that class, the key IS the
// estate's inventory.
//
// A record-LOCATED resource is the opposite shape. The object exists in the
// cloud; what it has nowhere to carry is a marker. The record answers "which
// object is this" and nothing else. Reading it as "may I delete this" would
// turn a lost or stale key into a cloud deletion, which is precisely the
// failure the maintainer ruling of 2026-08-17 forbids: an ID is not a
// permission. Since GitHub issue #364's envelope collapse, the discipline
// that keeps that true is the kind field: [RecordStore.getIdentity] never
// enumerates anything, and [builder.discoverOrphanedRecords] proposes a
// destroy only for a kind=object key - see [recordKindIdentity]'s comment.

// materializeLocated is [builder.materialize]'s front door for GitHub issue
// #270's record-located instances (identity.ClassRecordLocated).
//
// It is deliberately thin, and the thinness is the design. All it does is
// answer the one question the configuration cannot: which live object is
// this? Once [RecordStore.getIdentity] has returned an import identity, the
// instance is handed to [builder.materialize] as an ordinary [wanted], and
// everything downstream of wanted.importID is unchanged - the same import,
// the same read, the same ownership rule, the same encoding, the same
// dependency computation. A located resource is not a special kind of
// prior-state entry; it is an ordinary one whose identity string arrived
// from a different place.
//
// That is the opposite of [builder.materializeRecord], which bypasses
// importAndRead entirely because a record-backed instance has no cloud
// object to read. Here the cloud object is the whole point.
//
// # The three answers, and why "not found" is not an error
//
// A located record that does not exist produces an ordinary
// [ReasonAbsent] omission, so the plan proposes a CREATE. That is the same
// answer for two different situations, and making them indistinguishable is
// the ruling rather than a shortcut: an instance that has never been
// created and one whose record was LOST both want a create proposed, and
// internal/live/foreign then surfaces the live object as unclaimed rather
// than as an orphan to destroy. The failure mode is an announced duplicate,
// never a silent deletion. Issue #270 states the trade in those terms and
// accepts it.
//
// A store that cannot be read at all, or a record that decodes to another
// address, is an error - [RecordStore.getIdentity]'s own refusals - because
// continuing past either would bind this instance to an identity that is
// not its, and a wrong identity is invisible to every verdict-level check.
func (b *builder) materializeLocated(ctx context.Context, addr addrs.AbsResourceInstance) {
	if b.opts.RecordStore == nil {
		// Reachable only if a caller resolves a located type without also
		// configuring a store. internal/live/lint's admission gate makes
		// that impossible for a configuration - identity.ClassRecordLocated
		// is produced only when the root module's live block declares a
		// record_store - so this is an internal inconsistency rather than an
		// operator's mistake, and it is stated as one. It is also the
		// backstop that lets tools/estate-plan demote the markerless-type
		// refusal safely: the demotion is only sound while a run that
		// reaches here without a store stops, loudly, naming what is
		// missing.
		detail := fmt.Sprintf(
			"%s resolved to a record-located identity, but no record store was configured for this projection. This is an internal inconsistency: a type whose live object carries no ownership marker should never reach here without a live block's record_store, which is the only thing that can say which object it is.",
			addr,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryLocatedNoStore, detail))
		b.omitFailed(addr, detail)
		return
	}

	typeName := addr.Resource.Resource.Type

	rec, version, keyExists, identityFound, err := b.opts.RecordStore.GetIdentity(ctx, addr)
	if err != nil {
		detail := fmt.Sprintf("Reading the located record for %s failed: %s.", addr, err)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read a located record", detail))
		b.omitFailed(addr, detail)
		return
	}
	if keyExists {
		b.recordEnvelopeVersion(addr, version)
	}
	if !identityFound {
		b.omit(addr, ReasonAbsent,
			fmt.Sprintf(
				"No record of which live %s %s owns exists yet, so this resource has not been created yet, or its record was lost. Either way the plan will propose creating it: a %s carries no ownership marker, so nothing else can say which object it is. If the object does exist, it is reported as unclaimed rather than destroyed.",
				typeName, addr, typeName,
			),
			fmt.Sprintf("no located record exists yet saying which live %s it owns.", typeName),
		)
		return
	}

	// values is what carries a COMPOSITE identity through, and it needs no
	// new transport: [wanted.values] is the identity-attribute-per-component
	// form [identity.Resolution.IdentityValues] already holds for a concrete
	// instance, and importTarget/identityFromValues already turn it into the
	// provider's own identity object, checked against the provider's own
	// identity schema. A single-string record leaves it nil and takes the
	// importID path exactly as before.
	b.materialize(ctx, wanted{
		addr:     addr,
		importID: rec.ImportID,
		values:   rec.Components,
		located:  true,
	})
}

// LocatedRecordFrom derives the [LocatedRecord] one applied object of
// resourceType would be written as, from the same evidence
// [identity.LocatedIdentityPlanFor] already uses to decide whether the type
// is recordable at all: a composite identity comes back as components, a
// composed one as the documented import ID string, and every other
// recordable type as its own "id" attribute.
//
// It exists so that every writer of a located identity - [writeBackRecordEnvelopes]
// after an apply, and liveimport's Approve at migrate time (GitHub issue
// #365 slice 2) - derives one from the identical rule instead of each
// carrying its own copy that can drift from the other. Nothing here names a
// resource type; the three-way switch is on the SHAPE
// [identity.LocatedIdentityPlanFor] reports for resourceType, which is
// itself read from schema, not from a list.
//
// The second return is false whenever no usable identity could be derived -
// the type is not recordable at all, or the specific object carries an
// empty or missing identity attribute - and obj is then not written
// anywhere; every caller treats false as its own "cannot record" error.
//
// GitHub issue #364 unit A2 widened this function's callers past the
// located and markers-record-selected routes, both of which were already
// gated by [identity.LocatedType] / [identity.SelectedLocatedType] before
// reaching here, to every stamped and untaggable instance a migration or an
// apply writes back - populations neither of those gates ever screened. So
// the sensitivity half of that gate, [identity.RecordableIdentitySchema],
// is checked here directly rather than trusted to every caller: a record
// must never carry a secret, and folding the check into the one function
// every writer already calls is what keeps that true without each new call
// site having to remember to ask a second question.
func LocatedRecordFrom(resourceType string, schema providers.Schema, obj cty.Value) (LocatedRecord, bool) {
	if !identity.RecordableIdentitySchema(resourceType, schema) {
		return locatedRatifiedComponentsRecord(resourceType, schema, obj)
	}
	plan, recordable := identity.LocatedIdentityPlanFor(resourceType, schema)
	rec := LocatedRecord{}
	if recordable {
		switch {
		case plan.Composite():
			// A composite identity is recorded as an OBJECT and with no
			// string at all. "id" holds the bare leaf for such a type, so
			// recording it as the import ID would store a fragment that
			// reads back as a whole identity - the exact defect this
			// branch exists to close.
			rec.Components, recordable = identity.LocatedIdentity(obj, plan.Components)
			if recordable {
				// GitHub issue #397 sibling wall (corpus-alb-complete's
				// aws_lb_target_group_attachment lambda-target ports): the
				// required components alone already name the object per the
				// provider's own wire identity schema, but an OPTIONAL one
				// this instance happens to carry - port, for an
				// instance-target attachment - is what disambiguates it
				// from a sibling that shares the same required components
				// at a different port. Recording it whenever it is there
				// costs nothing (identityFromValues already leaves an
				// unsupplied optional attribute null rather than refusing)
				// and closes the collision a required-only record would
				// otherwise risk; recording NOTHING for it when the object
				// genuinely has none - a lambda target's port - is the
				// whole point of this unit rather than a gap in it.
				for name, v := range identity.LocatedIdentityOptional(obj, plan.OptionalComponents) {
					rec.Components[name] = v
				}
			}
		case plan.Composed():
			// The provider serves no identity object for this type, so
			// there is no object to record - but its own Import section
			// states the string's grammar, and issue #337's
			// [identity.DocumentedImportIDs] resolved every segment
			// against this very schema. The string composed here is the
			// documented import ID, read rather than invented.
			rec.ImportID, recordable = identity.LocatedComposedImportID(obj, plan.ImportIDParts, plan.ImportIDVariadicGroup, plan.ImportIDAlternatives, plan.ImportIDSeparator)
		case plan.Named():
			// The wire identity schema said nothing usable, and a ratified
			// row names a different attribute than "id" as the type's
			// whole identity (GitHub issue #332: aws_default_route_table
			// imports by its parent VPC's id, not by its own). Recording
			// the bare "id" here would be a wrong identity, invisible to
			// every verdict-level check until the next run tries to
			// import it and the provider says so.
			rec.ImportID, recordable = identity.LocatedNamedAttr(obj, plan.Attr)
		default:
			rec.ImportID, recordable = identity.LocatedImportID(obj)
		}
	}
	if !recordable || rec.Empty() {
		return LocatedRecord{}, false
	}
	return rec, true
}

// locatedRatifiedComponentsRecord is [LocatedRecordFrom]'s fallback for a
// type neither the provider's own wire identity schema nor the documented
// import ID grammar can already record ([identity.RecordableIdentitySchema]
// answered false): when the type's ratified table row still carries a
// Components composite - the same Literal/Attrs/Block/OmitIfAbsent chain
// the static evaluator and GitHub issue #388's plan-node seam already
// derive an identity from, just never plumbed into a WRITTEN record before
// this - it is evaluated here directly against obj, the real applied
// object migrate or an apply already has in hand, with no HCL involved.
//
// This is what closes issue #364's own untaggable-with-no-record gap for a
// type like aws_route53_record: it is untaggable, has a genuine ratified
// composite identity (zone_id/name/type[/set_identifier]), but
// [identity.LocatedIdentityPlanFor] answers false for it because AWS serves
// no wire identity schema for the type and tools/importdocs-gen's prose
// parser did not resolve its "X or X_SETIDENTIFIER" Import section into a
// [identity.DocumentedImportIDs] grammar. Measured 2026-08-24 against
// table_generated.go/idnotwhole_generated.go/docimportid_generated.go:
// about 70 ratified types share this exact shape (a 2+-Attrs Components
// row, in IDNotProvenWholeTypes, absent from docimportid_generated.go), so
// this reaches all of them, not only the one this unit's estate exercises -
// nothing here names a resource type.
//
// Only [identity.ComponentsFromValue]'s composed-STRING form is used, never
// its values map: [LocatedRecord.Components] is documented as the
// PROVIDER'S OWN wire identity object, read back at plan time through
// providers.ImportTarget{Identity: ...} - a call a provider with no wire
// identity schema for this type (the precondition for reaching this
// function at all) cannot answer. A type whose components chain resolves
// to no string at all ([identity.TypeIdentity.IdentityObjectOnly]) is
// therefore left unrecordable here rather than risk that mismatch; that is
// not a regression; it was unrecordable before this function existed too.
//
// [identity.SensitiveComponentsAttr] is this function's own version of
// [sensitiveIdentityAttr]: obj already arrives here with every mark
// stripped (every caller unmarks deep before this point, same as
// [LocatedIdentityPlanFor]'s callers), so the schema is the only place left
// to ask whether a component this would record is secret.
func locatedRatifiedComponentsRecord(resourceType string, schema providers.Schema, obj cty.Value) (LocatedRecord, bool) {
	ti, ok := identity.LookupType(resourceType)
	if !ok || len(ti.Components) == 0 {
		return LocatedRecord{}, false
	}
	if identity.SensitiveComponentsAttr(ti, schema) != "" {
		return LocatedRecord{}, false
	}
	importID, _, ok := identity.ComponentsFromValue(ti, obj)
	if !ok || importID == "" {
		return LocatedRecord{}, false
	}
	return LocatedRecord{ImportID: importID}, true
}

// SummaryLocatedNoStore is the summary of the refusal
// [builder.materializeLocated] raises when a located instance reaches the
// projection with no store to look it up in. Named for the same reason
// [SummaryOutsideEstate] is: internal/live/refusalscan requires every
// diagnostic this fork raises to have a registry entry, and the entry and
// the diagnostic have to name one string.
const SummaryLocatedNoStore = "Record-located instance with no record store"

// LocatedRecord is one record-located instance's identity: exactly one of
// the two forms [identityPayload] documents.
//
// It is a struct rather than two parameters because two independently
// written branches extending the same call by position is a merge git
// resolves silently and wrongly; a named field cannot land in the wrong
// slot.
type LocatedRecord struct {
	// ImportID is the identity of a type identified by one string.
	ImportID string

	// Components is the identity of a type identified by several, one
	// string per identity-schema attribute.
	Components map[string]string
}

// Empty reports whether this record says nothing about which object an
// instance owns, which is the one thing a stored record may never do.
func (r LocatedRecord) Empty() bool {
	return r.ImportID == "" && len(r.Components) == 0
}
