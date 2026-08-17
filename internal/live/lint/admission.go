// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
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
