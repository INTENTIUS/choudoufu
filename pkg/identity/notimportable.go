// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// NotImportable is issue #331's veto, asked as one question by every
// admission route rather than re-implemented at each of them.
//
// A type here has no classic Importer at all - see [NotImportableReason],
// which tools/survey-gen derives by calling ImportResourceState against the
// pinned provider, not by inferring it from a schema. That is the fact no
// other signal carries: a wire identity schema, a taggable marker path and a
// recordable `id` can each be perfectly correct while the very first
// ImportResourceState a later run makes answers "resource ... doesn't
// support import". Admitting such a type is the trade this fork is forbidden
// to make - a plan refusal traded for an apply refusal - and it is worse than
// the usual form of that trade, because the apply that fails is not the first
// one. The first apply creates the object and records it; every plan after
// that fails permanently, with the object already live.
//
// # Why this is a function and not three lookups
//
// The roster arrived (2026-08-20) wired into internal/live/lint's admitted()
// alone, and an audit the following night found at least two further routes
// that reach admission without passing through it. The map is easy to read
// from anywhere and that is exactly the problem: a fourth route added later
// would look complete without it. So the rule is stated once, here, and the
// routes call it:
//
//   - the schema fallback, [synthesizeTypeIdentity], which is what
//     internal/live/lint's admitted(), [resolver.lookupType] and
//     internal/live/liveimport's admittedByProviderSchema all ask; and
//
//   - [LocatedType], the record-located route, which asks no schema fallback
//     at all and so has to consult this itself.
//
// live/notimportable_routes_test.go holds the two together by measuring
// every route against this predicate over the whole provider surface.
//
// # Table wins, as it does for the markerless veto
//
// A type with a ratified row is a batch's explicit, evidenced decision, and
// this predicate must not contradict one that is still standing; retracting
// such a row is tools/row-gen -emit's job (it retracted two when the roster
// was first derived). The two sets are disjoint today and this guard is what
// keeps the ordering safe if that ever stops being true.
func NotImportable(resourceType string) bool {
	if _, ok := LookupType(resourceType); ok {
		return false
	}
	_, vetoed := NotImportableTypes[resourceType]
	return vetoed
}
