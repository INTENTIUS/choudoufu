// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is this package's whole answer to "what does discovery do with
// an [identity.Class]". GitHub issue #810: the answer used to be twelve
// class comparisons at ten branch sites across seven files - five of them
// spelled `!= identity.ClassConcrete` in a loop that then skipped the
// resolution - so a new class was handled by whichever site happened to name
// it and was silently skipped everywhere else. Here it is one row per class,
// and classes_test.go's TestClassTableIsTotal fails the moment
// [identity.AllClasses] grows a class this table has no row for.
//
// Each field is one decision, and each field's value came verbatim from the
// site that used to make it; the sites now read the field.
//
// The five concrete-only booleans are deliberately five fields and not one.
// They agree today because every one of them answers true for exactly
// [identity.ClassConcrete], but they ask different questions - being a
// parent worth a list call is not the same as being able to answer a
// formula's lookup, which is not the same as carrying an identity worth
// comparing with the configuration's - and a new class has to answer each
// of them on its own terms. Collapsing them to one boolean would decide four
// of those questions by the answer to the fifth.

// classHandler is what discovery does with one [identity.Class]: a field per
// decision, each one lifted from the site that used to make it. A field per
// decision rather than a method per class, for the same reason projection's
// table has one: the sites run at different points of a sweep and share no
// state.
type classHandler struct {
	// needsDiscovery says this class IS identity's needs-discovery class,
	// the one whose instances join the binding demand a marker scan
	// answers. Read in [declared.scan]'s resolution loop, which skips every
	// other class before it ever looks at RecordBackedAddrs.
	needsDiscovery bool

	// scopesChildList says a resolution of this class, when it also carries
	// an ImportID, is a bound parent instance whose live value scopes a
	// child listing: the parent-read legs' candidate filter
	// ([parentReadSweepType], [readParentListChildrenType]) and the Cloud
	// Control scoped leg's ([cloudControlScopedSweepType]) all ask this
	// same question of every resolution in the result before spending a
	// list call on it.
	scopesChildList bool

	// answersParentLookup says a resolution of this class can supply a
	// parent's live identity value to a formula lookup - the closures
	// [declaredChildImportIDs] and [foldChildReadSweep] each build over the
	// pass's resolutions, which answer false for a parent they cannot read
	// a settled value off rather than rendering a formula against a guess.
	answersParentLookup bool

	// comparableIdentity says this class carries an identity the
	// configuration's computed identity can be compared against, so
	// [declared.displacedFrom] can reach a verdict at all. False is
	// displaced.go's "nothing to compare", one of the six ways that
	// function declines to answer, and it answers [verdictOwnObject].
	comparableIdentity bool

	// boundResolution says this class is what a provider-configuration
	// pass's binding produces, so multiprovider.go's pickBase prefers a
	// copy of this class over a copy that is not one when merging the
	// passes' views of the same address. It is the "more resolved" test,
	// and only the one pass that owns the address can produce it.
	boundResolution bool

	// declaredImportID is this class's arm of [declaredImportID]: one
	// resolution's import identity in the parent-read legs' composed form,
	// or false when this class cannot name one. nil is the switch's
	// fall-through - "anything else has no identity to offer" - and
	// [declaredImportID] still answers "", false for it.
	declaredImportID func(typeName string, r identity.Resolution, lookup func(addrs.AbsResourceInstance, string) (string, bool)) (string, bool)

	// renderIdentityValues is this class's arm of [renderIdentityValues]:
	// one resolution's identity as a map of argument name to value. nil is
	// that switch's `default:` arm, and [renderIdentityValues] still answers
	// nil, false for it.
	renderIdentityValues func(r identity.Resolution, lookup func(addrs.AbsResourceInstance, string) (string, bool)) (map[string]string, bool)
}

// classTable is total over [identity.AllClasses], and classes_test.go is
// what holds it total.
//
// Every site reads it with a plain map index, because the zero handler IS
// their old behaviour: each boolean site compared a class for equality
// against one it named, so a class no version of this package declares
// answered false at all five and still does, and the two function fields are
// nil for a class the switch they came from had no case arm for, which is
// that switch's fall-through and `default:` respectively.
var classTable = map[identity.Class]classHandler{
	identity.ClassConcrete: {
		scopesChildList:     true,
		answersParentLookup: true,
		comparableIdentity:  true,
		boundResolution:     true,
		declaredImportID: func(typeName string, r identity.Resolution, lookup func(addrs.AbsResourceInstance, string) (string, bool)) (string, bool) {
			if r.ImportID != "" {
				return r.ImportID, true
			}
			if len(r.IdentityValues) > 0 {
				return composeImportIDFromComponents(typeName, r.IdentityValues)
			}
			return "", false
		},
		renderIdentityValues: func(r identity.Resolution, lookup func(addrs.AbsResourceInstance, string) (string, bool)) (map[string]string, bool) {
			if len(r.IdentityValues) == 0 {
				return nil, false
			}
			return r.IdentityValues, true
		},
	},
	identity.ClassParentDerived: {
		declaredImportID: func(typeName string, r identity.Resolution, lookup func(addrs.AbsResourceInstance, string) (string, bool)) (string, bool) {
			if r.Formula == nil {
				return "", false
			}
			if vals, ok := r.Formula.RenderAttrs(lookup); ok && len(vals) > 0 {
				if id, ok := composeImportIDFromComponents(typeName, vals); ok {
					return id, true
				}
			}
			if id, ok := r.Formula.Render(lookup); ok && id != "" {
				return id, true
			}
			return "", false
		},
		renderIdentityValues: func(r identity.Resolution, lookup func(addrs.AbsResourceInstance, string) (string, bool)) (map[string]string, bool) {
			return r.Formula.RenderAttrs(lookup)
		},
	},
	identity.ClassNeedsDiscovery: {
		needsDiscovery: true,
	},
	identity.ClassRecordBacked:  {},
	identity.ClassRecordLocated: {},
}
