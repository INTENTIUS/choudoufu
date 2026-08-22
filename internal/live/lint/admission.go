// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// admitted reports whether the given provider-local resource type may appear
// in a stateless configuration: first by the generated table, then by
// [identity.MarkerlessTypes]' standing veto, and - only when the caller
// supplied provider schemas and the veto did not fire - by whatever
// [identity.SynthesizeTypeIdentity] can derive from those schemas and the
// configuration's own naming signal.
//
// The table lookup runs first and unconditionally, so a type the table
// already covers never depends on schemas being present at all, and the
// veto cannot contradict a ratified row. Where the two disagree the row
// wins - which is what kept the veto's arrival from retracting anything on
// its own, while 77 vetoed types were still admitted.
//
// The two sets are disjoint now: tools/row-gen's -emit filters the emitted
// rows by the same roster, and live/admission_coverage_test.go's
// markerlessAdmittedOverlapMax holds that at zero (#249). So this ordering
// decides nothing today. It stays because it is the safe direction if the
// overlap ever returns: a row that reached the table by a route -emit does
// not filter still describes support a ratification batch signed off on,
// and this function is not where that argument should be had.
//
// The veto sits ahead of the schema fallback and not behind it because a
// type row-gen retracts leaves the table and lands in front of
// [identity.SynthesizeTypeIdentity], which would re-admit some of them from
// the provider's identity schema alone - plan-and-create-only support, for
// a type whose whole problem is that no later run can find the object
// again. Consulted after the fallback the veto would be a no-op for exactly
// the types it exists to refuse; consulted here it is not.
//
// The remaining asymmetry is the one this function has always had: the
// fallback only ever admits a type the table refuses, so a caller with no
// schemas gets exactly the table's answer minus the veto, and a caller with
// schemas gets that plus whatever the schemas additionally justify.
//
// [notImportableVetoed] below is REDUNDANT with the fallback and is kept
// deliberately, so read this before deleting it as dead: the issue #331 veto
// now lives inside [identity.SynthesizeTypeIdentity] itself, because lint is
// not the only route to admission and the other routes reach the fallback
// without passing through this function. Both lines therefore consult one
// rule, [identity.NotImportable] - they cannot disagree, and the one here is
// what keeps this function's own verdict readable in the order the comments
// above describe.
func admitted(resourceType string, schemas map[string]providers.Schema, signal *identity.ConfigSignal) bool {
	if _, ok := admittedTypesV0[resourceType]; ok {
		return true
	}
	if markerlessVetoed(resourceType) {
		return false
	}
	if notImportableVetoed(resourceType) {
		return false
	}
	if len(schemas) == 0 {
		return false
	}
	_, ok := identity.SynthesizeTypeIdentity(resourceType, schemas, signal)
	return ok
}

// markerlessVetoed reports whether resourceType is refused by
// [identity.MarkerlessTypes]' standing rule rather than merely absent from
// the generated table: the provider mints the identity and the type carries
// no tags argument, so there is nowhere to write the marker that is the only
// remaining handle. See [identity.MarkerlessReason].
//
// The table lookup is repeated here rather than hoisted into the caller so
// that this predicate answers the same question [admitted] asks in the same
// order, and cannot drift into claiming a ratified type is vetoed. A type in
// both is a type row-gen has not retracted yet, and its row is what ships.
//
// [checkManagedResources] reads it a second time to pick which refusal to
// raise: [RuleMarkerlessType] where this is true, [RuleUnadmittedType] where
// it is not. The two must not be merged. RuleUnadmittedType's closing clause
// invites the reader to open an issue naming the type and its import ID,
// which is an invitation to a ratification this rule has already refused on
// evidence.
func markerlessVetoed(resourceType string) bool {
	if _, ok := admittedTypesV0[resourceType]; ok {
		return false
	}
	_, vetoed := identity.MarkerlessTypes[resourceType]
	return vetoed
}

// notImportableVetoed reports whether resourceType is refused because the
// provider reports no classic Importer for it at all (issue #331) - a fact
// [identity.SynthesizeTypeIdentity]'s schema fallback cannot see, since it
// derives an identity from the schemas and never asks whether
// ImportResourceState itself works. A wire identity schema or a taggable
// marker path can both be genuinely correct and the type can still fail
// "resource ... doesn't support import" the moment a real migrate calls it -
// [identity.NotImportableTypes] is tools/survey-gen's own probe of that
// question, not an inference from something else.
//
// It delegates rather than reading the roster, which is the difference
// between this predicate and [markerlessVetoed] beside it. The veto lives in
// [identity.NotImportable] because lint is not the only route to admission -
// internal/live/identity's own resolver and internal/live/liveimport's
// ratify both reach the schema fallback without ever calling this function -
// and a rule stated once in the layer all three share cannot be half-applied
// the way three copies of it were. Table-wins-over-veto comes with it: see
// [identity.NotImportable], which applies the same ordering [markerlessVetoed]
// does here, over [identity.DefaultTable] rather than over admittedTypesV0.
// The two tables differ only in the RECORD_ADMITTED rows, and lint has
// already refused those, by class, well before this line.
func notImportableVetoed(resourceType string) bool {
	return identity.NotImportable(resourceType)
}

// markerlessLocatedSupportExists is the load-bearing claim of the located
// variant of the markerless refusal, factored out so a test can pin it by
// value - the same device [recordStoreSupportExists] uses in
// logical_type.go, and for the same reason: the failure mode this wording
// exists to prevent is a false "not yet", and there are unbounded ways to
// spell one and only one thing worth asserting, which is that the support
// is here NOW.
const markerlessLocatedSupportExists = "That support exists"

// markerlessLocatedDetail is the [RuleMarkerlessType] refusal for a type
// the record store COULD locate, raised only when no record_store is
// declared.
//
// It is the plan-time error GitHub issue #270 requires, and the reason it
// has to exist is the same reason internal/live/projection raises
// "Record-backed instance with no record store": once tools/estate-plan can
// demote markerless-type to a pre-onboarding finding, an operator who
// writes the live block and forgets the store must still be stopped here,
// by name, with the missing thing named. Demoting a refusal that had no
// such backstop would be trading a refusal for a silent failure.
//
// The wording deliberately does NOT reuse the permanent markerless
// sentence. That one says "No configuration edit changes that, and no
// future batch reaches it", which is true of a type whose identity nothing
// can hold and false of this one, where a single block is the whole fix.
func markerlessLocatedDetail(resourceType string) string {
	return fmt.Sprintf(
		"resource type %q has nowhere to carry an ownership marker: %s. Applying a block of "+
			"this type %s That is a fact about the MARKER, which answers \"may I delete "+
			"this\" - it is not a fact about the identity, which answers \"which object is "+
			"this\". For an object this estate created, choudoufu minted the identity, and a "+
			"per-estate record can hold it. "+
			"%s - GitHub issue #270's record-located identity - and this configuration has "+
			"no live block, which is the only reason %s is refused here. "+
			impliedRecordStoreRemedy+
			" The record answers only which object "+
			"this is; it is never read as permission to delete anything, and losing it "+
			"proposes a create rather than a destroy.",
		resourceType, identity.MarkerlessReason, UnfindableClause, markerlessLocatedSupportExists, resourceType,
	)
}

// blockHasNoInstances reports whether ONE resource block - identified by its
// own module path and resource address, not merely its type - is provably
// producing zero live instances this run, so [RuleUnadmittedType] and
// [RuleMarkerlessType] have nothing to say about it: an admission verdict is
// about whether an INSTANCE of a type can be identified on the live system,
// and a block with no instances has none needing that.
//
// "Provably" means read off signal, never guessed: [identity.ScanConfig]
// walks the whole configuration with the same count/for_each expansion
// identity resolution trusts (internal/live/identity/signal.go's
// collectSignalInto), and issue #219 already hardened that walk so a
// count/for_each that evaluates to zero or empty contributes nothing to it
// - not even a placeholder. An absence in signal for this exact block is
// therefore not silence about an unresolved count; it is the same "zero
// instances" answer identity resolution itself would give, computed by the
// one walk both layers already share, so lint and resolution can never
// disagree about which blocks have anything to identify.
//
// signal == nil (no provider schemas were available - checkConfig only
// builds one when schemas are present, see its own comment) always answers
// false: with nothing to consult this function has nothing to add, and the
// existing, understood, conservative default - refuse when uncertain -
// stands unchanged.
//
// Scoped to the one block, not the type: a second block of the same type
// elsewhere in the same or a sibling module can carry real instances while
// this one carries none, and a type-wide answer would let that block's
// admission verdict leak onto this one.
func blockHasNoInstances(signal *identity.ConfigSignal, path addrs.Module, resourceAddr addrs.Resource, resourceType string) bool {
	if signal == nil {
		return false
	}
	for _, inst := range signal.Instances(resourceType) {
		if inst.Module.IsForModule(path) && inst.Resource.Resource.Equal(resourceAddr) {
			return false
		}
	}
	return true
}
