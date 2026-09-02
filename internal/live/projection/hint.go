// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import "time"

// Hint is what a caller may learn from the most recent guided-discovery
// hint the estate's record store carries (hint_store.go, issue #109): a
// hint about what the estate looked like as of WrittenAt, never a record it
// can act on directly.
//
// The shape is deliberately reduced - a type roster and a timestamp, with
// no field an attribute value, an identifier or a marker could travel in -
// and [TestHint_reducedShapeOnly] keeps it from growing. A caller
// (internal/live/discovery's guided mode) uses a Hint to decide what to
// look at first and what to skip on a routine pass - never to decide what
// the plan says. The read side ([ReadHintStore]) returns (nil, error)
// rather than a partial Hint on any problem, and the caller's contract is
// to treat every such error as "fall back to today's full enumeration",
// never to surface it as a run failure - staleness and absence are this
// cache's whole contract.
type Hint struct {
	// Estate is the estate name the hint recorded.
	Estate string

	// WrittenAt is when the hint was built. Nothing in this package gates
	// on it, but a caller deciding how much to trust the hint
	// (internal/live/discovery's staleness check) needs it to decide with.
	WrittenAt time.Time

	// Types is the set of resource types the writing run's final state
	// spanned. A type's absence here means "that run recorded nothing of
	// it", not "this estate has none" - see the staleness contract above.
	Types map[string]bool
}
