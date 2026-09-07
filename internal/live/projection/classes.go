// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is this package's whole answer to "what does projection do with
// an [identity.Class]". GitHub issue #810: the answer used to be spread
// across four branch sites in build.go and readconcurrency.go, so a new
// class was handled by whichever of them happened to name it and silently
// defaulted everywhere else. Here it is one row per class, and
// classes_test.go's TestClassTableIsTotal fails the moment
// [identity.AllClasses] grows a class this table has no row for.
//
// Each field is one decision, and each field's value came verbatim from the
// site that used to make it; the sites now read the field.

// classHandler is what this package does with one [identity.Class]. A field
// per decision, not a method per class: the four sites run at four different
// points of a build and share no state, so there is nothing for a method set
// to hold.
type classHandler struct {
	// holdUndeclared says an UNDECLARED resolution of this class is held
	// out of [builder.applyRecordFirst] and materialized in a final pass
	// instead. Only concrete resolutions can be: the hold exists because an
	// undeclared entry always has a record by construction, which
	// materializeFromRecord would otherwise claim ahead of every declared
	// instance competing for the same import identity. See the long comment
	// at the hold site in [builder.run].
	holdUndeclared bool

	// ownRecordDoor says this class already comes through the record
	// store's own door, so [builder.applyRecordFirst] and
	// [builder.startRecordFirstPrefetch] leave it alone: record-backed has
	// no cloud object for a record to name, and record-located already
	// reads - or, under `markers = record`, deliberately never verifies -
	// the identical record through [builder.materializeLocated].
	ownRecordDoor bool

	// orderWork appends one resolution to the work list [orderWork] routes
	// this class to. Routing located explicitly, rather than letting it
	// fall to discovery, is load-bearing: see orderWork's own doc comment
	// and TestOrderWorkRoutesLocatedExplicitly.
	orderWork func(w *orderWorkLists, r identity.Resolution)
}

// orderWorkLists is [orderWork]'s accumulator, one field per work list it
// splits resolutions into. It exists so that a table entry can name its
// destination list; orderWork's own signature and return values are
// unchanged.
type orderWorkLists struct {
	concrete       []identity.Resolution
	pending        []identity.Resolution
	needsDiscovery []identity.Resolution
	recordBacked   []identity.Resolution
	located        []identity.Resolution
}

// classTable is total over [identity.AllClasses], and classes_test.go is
// what holds it total.
var classTable = map[identity.Class]classHandler{
	identity.ClassConcrete: {
		holdUndeclared: true,
		orderWork: func(w *orderWorkLists, r identity.Resolution) {
			w.concrete = append(w.concrete, r)
		},
	},
	identity.ClassParentDerived: {
		orderWork: func(w *orderWorkLists, r identity.Resolution) {
			w.pending = append(w.pending, r)
		},
	},
	identity.ClassNeedsDiscovery: {
		orderWork: func(w *orderWorkLists, r identity.Resolution) {
			w.needsDiscovery = append(w.needsDiscovery, r)
		},
	},
	identity.ClassRecordBacked: {
		ownRecordDoor: true,
		orderWork: func(w *orderWorkLists, r identity.Resolution) {
			w.recordBacked = append(w.recordBacked, r)
		},
	},
	identity.ClassRecordLocated: {
		ownRecordDoor: true,
		orderWork: func(w *orderWorkLists, r identity.Resolution) {
			w.located = append(w.located, r)
		},
	},
}

// classFor is the table lookup, and it carries the exact fallback the
// branch sites used to have: every one of them treated a class it did not
// name the way it treated needs-discovery - orderWork's `default:` arm
// routed it to the omission list, and the other three sites did nothing
// special for it, which is also the needs-discovery row. A Resolution whose
// Class is some string no version of this package declares therefore still
// behaves exactly as it did before the table existed, rather than reaching
// a nil handler.
func classFor(c identity.Class) classHandler {
	if h, ok := classTable[c]; ok {
		return h
	}
	return classTable[identity.ClassNeedsDiscovery]
}
