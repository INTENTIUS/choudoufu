// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"

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
func admitted(resourceType string, schemas map[string]providers.Schema, signal *identity.ConfigSignal) bool {
	if _, ok := admittedTypesV0[resourceType]; ok {
		return true
	}
	if markerlessVetoed(resourceType) {
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
			"simply not turned it on, which is the only reason %s is refused here. Declare a "+
			"record_store in the live block and it is admitted:\n\n"+
			"  terraform {\n"+
			"    live {\n"+
			"      estate = \"my-estate\"\n"+
			"      record_store \"ssm\" {}\n"+
			"    }\n"+
			"  }\n\n"+
			"The label picks the backend: \"ssm\", \"s3\" (which needs a bucket argument), or "+
			"\"local\" (a directory beside the module). The record answers only which object "+
			"this is; it is never read as permission to delete anything, and losing it "+
			"proposes a create rather than a destroy.",
		resourceType, identity.MarkerlessReason, UnfindableClause, markerlessLocatedSupportExists, resourceType,
	)
}
