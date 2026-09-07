// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is this package's whole answer to "what do the live commands do
// with an [identity.Class]". GitHub issue #810: the answer used to be five
// branch sites across live_mode.go, live_plan.go and live_ls.go, each
// naming the one or two classes it cared about, so a new class was handled
// by whichever of them happened to mention it. Here it is one row per class,
// and live_classes_test.go's TestLiveClassTableIsTotal fails the moment
// [identity.AllClasses] grows a class this table has no row for.
//
// The file is live_-prefixed because that is how this package's fork-owned
// files are named (live_plan.go, live_ls.go, live_mode.go); internal/command
// is otherwise upstream's.

// liveClassHandler is what this package's live commands do with one
// [identity.Class]: a field per decision, each one lifted from the site that
// used to make it.
type liveClassHandler struct {
	// cacheVouch says an instance of this class counts towards issue
	// #692's cache-vouching type list, whose whole point is the
	// client-named majority a cache hit wants to skip a read for. See
	// [cacheVouchTypesFor].
	cacheVouch bool

	// boundFromRecord says a bound instance of this class got its identity
	// from the estate's record store, so live-plan's bound report labels it
	// [views.LivePlanBoundRecord]. A pre-sweep marker still wins over this:
	// the report asks that question first.
	boundFromRecord bool

	// needsDiscovery says this class IS identity's needs-discovery class,
	// which [downgradedToDiscovery] compares a second resolution pass
	// against a first one for. It is deliberately a property of the class
	// and not of the instance: the measurement that function makes is "did
	// this address move INTO that class between passes".
	needsDiscovery bool

	// formulaParents says an instance of this class carries a formula whose
	// parents [expandFormulaParents] must pull in alongside it, so that a
	// parent-derived identity can be rendered from its parents' live IDs.
	formulaParents bool

	// lsRung and lsDetail are [liveLsRung]'s answer for a declared instance
	// of this class that a tag listing could not find: the rung name and the
	// operator-facing sentence saying why the tags were never going to show
	// it. An empty lsRung means the class settles nothing on its own and
	// liveLsRung goes on to ask the instance's provider schema instead -
	// which is the ordinary taggable case, where not being found is a real
	// gap rather than a rung.
	lsRung   string
	lsDetail string
}

// liveClassTable is total over [identity.AllClasses], and
// live_classes_test.go is what holds it total.
//
// Every site reads it with a plain map index, and the zero handler is the
// right answer for a Class no version of this package declares: each of the
// five sites used to test a class for equality with one it named, so an
// unrecognized class was false at all five, which is exactly what the zero
// handler says.
var liveClassTable = map[identity.Class]liveClassHandler{
	identity.ClassConcrete: {
		cacheVouch: true,
	},
	identity.ClassParentDerived: {
		formulaParents: true,
	},
	identity.ClassNeedsDiscovery: {
		needsDiscovery: true,
	},
	identity.ClassRecordBacked: {
		boundFromRecord: true,
		lsRung:          "record",
		lsDetail:        "This instance's identity is the estate's own persisted record, not a cloud object with tags to read - live/MARKERS.md's tier definitions (#417) name this the record-carried tier. It can never appear in this listing.",
	},
	identity.ClassRecordLocated: {
		boundFromRecord: true,
		lsRung:          "record",
		lsDetail:        "This instance's cloud object carries no ownership marker at all; its identity comes from the estate's record store instead (GitHub issue #270). It can never appear in this listing by its tags.",
	},
}
