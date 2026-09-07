// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is this package's whole answer to "what does ratification do
// with an [identity.Class]". GitHub issue #810: one equality test decides
// it, which is exactly the shape that adds a class quietly - the test says
// false for a class it does not name, and false here is "write the marker".
// Here it is one row per class, and classes_test.go's TestClassTableIsTotal
// fails the moment [identity.AllClasses] grows a class this table has no row
// for, so a new class has to state which side of gate 4 it is on rather than
// inheriting the writing side by omission.

// classHandler is what ratification does with one [identity.Class].
type classHandler struct {
	// needsDiscovery says an instance of this class has no identity of its
	// own to write down: it is server-assigned, and only a marker sweep can
	// say which live object it is. It is gate 4's class half - see
	// [Ratification.instanceNeedsDiscovery], whose own doc comment gives the
	// direction this gate has to fail in - and it is a fact about the
	// resolved INSTANCE, not about its type, which
	// [serverAssignedType] answers separately from the identity table.
	needsDiscovery bool
}

// classTable is total over [identity.AllClasses], and classes_test.go is
// what holds it total.
//
// The site reads it with a plain map index, because the zero handler IS its
// old behaviour: the test it replaces compared the class for equality with
// ClassNeedsDiscovery, so an unrecognized class answered false and still
// does.
var classTable = map[identity.Class]classHandler{
	identity.ClassConcrete:      {},
	identity.ClassParentDerived: {},
	identity.ClassNeedsDiscovery: {
		needsDiscovery: true,
	},
	identity.ClassRecordBacked:  {},
	identity.ClassRecordLocated: {},
}
