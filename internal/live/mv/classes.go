// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is this package's whole answer to "what does a rename do with
// an [identity.Class]". GitHub issue #810: the answer used to be three
// equality tests inside [mover.find] and [mover.materialize], each naming
// one class, so a new class silently took the ordinary marker-rewrite path -
// the path that is only correct for a resource whose identity is a tag.
// Here it is one row per class, and classes_test.go's TestClassTableIsTotal
// fails the moment [identity.AllClasses] grows a class this table has no row
// for.

// classHandler is what a rename does with one [identity.Class]: a field per
// decision, each one lifted from the site that used to make it.
type classHandler struct {
	// renameMovesRecord says what says which live object this instance owns
	// is a key in the estate's record store rather than a tag, so renaming
	// it means moving that key - a different operation from either of
	// [mover.find]'s paths, and one live-mv does not do yet. GitHub issue
	// #270. The refusal is raised up front, before any search: see the
	// refusal site's own comment for why failing later would be worse.
	renameMovesRecord bool

	// findByListing says this instance's identity is provider-assigned, so
	// [mover.find] cannot compute what to look for and searches by listing
	// the type (or, where the provider cannot list it, by the estate's
	// record) instead of by the identity path.
	findByListing bool

	// materializeWholeList says a rendered identity for this instance needs
	// the parents' live IDs, so [mover.materialize] hands the projection
	// builder the WHOLE resolution list rather than this one resolution.
	materializeWholeList bool
}

// classTable is total over [identity.AllClasses], and classes_test.go is
// what holds it total.
//
// Both sites read it with a plain map index, because the zero handler IS
// their old behaviour: each of the three compared a class for equality
// against one it named, so a class no version of this package declares
// answered false at all three and still does.
var classTable = map[identity.Class]classHandler{
	identity.ClassConcrete: {},
	identity.ClassParentDerived: {
		materializeWholeList: true,
	},
	identity.ClassNeedsDiscovery: {
		findByListing: true,
	},
	identity.ClassRecordBacked: {},
	identity.ClassRecordLocated: {
		renameMovesRecord: true,
	},
}
