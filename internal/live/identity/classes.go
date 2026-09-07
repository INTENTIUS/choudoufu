// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// This file holds the class LIST, and nothing about what any consuming
// package does with a class. GitHub issue #810: 25 fork files branched on
// [Class], so adding a class was a 25-file change that nothing made fail
// loudly - the gauntlet found the packages that had not handled it, one
// estate at a time. The fix is a per-class handler table in each consuming
// package (internal/live/projection/classes.go and its equivalents), plus a
// test in each of those packages asserting its table is total over this
// list. Go's own type system cannot do this for us: a Class is a string
// with no exhaustiveness check, and this package cannot own a table that
// knows how projection materializes or how mv finds, because those packages
// import this one and not the other way round.
//
// So the list is the seam. It is the smallest thing this package can export
// that lets every consumer's guard fail on a class it has not handled: one
// slice, no knowledge of any consumer, and [ClassTableGaps] so that the
// five guards make the same assertion instead of five copies that can rot
// apart.

// AllClasses returns every [Class] this package declares, in declaration
// order. The returned slice is freshly allocated, so a caller may sort or
// filter it without disturbing anyone else's.
//
// Adding a Class means adding it here too, and TestAllClassesMatchesTheConstBlock
// is what makes forgetting fail: it reads the const block out of
// identity.go's own source and compares. Once it is here, every consuming
// package's own class-table guard goes red until that package decides what
// the new class does.
func AllClasses() []Class {
	return []Class{
		ClassConcrete,
		ClassParentDerived,
		ClassNeedsDiscovery,
		ClassRecordBacked,
		ClassRecordLocated,
	}
}

// ClassTableGaps compares a consuming package's per-class handler table
// against [AllClasses]: missing holds the classes with no entry, unknown
// holds entries whose key is not a declared class. Both are in [AllClasses]
// order for missing and map-iteration order for unknown, which is why a
// caller that prints unknown should sort it.
//
// A table that is total over the class list returns two empty slices. That
// is the whole assertion each consuming package's classes_test.go makes;
// this function exists so that the five of them cannot drift apart.
func ClassTableGaps[T any](table map[Class]T) (missing, unknown []Class) {
	all := AllClasses()
	declared := make(map[Class]bool, len(all))
	for _, c := range all {
		declared[c] = true
		if _, ok := table[c]; !ok {
			missing = append(missing, c)
		}
	}
	for c := range table {
		if !declared[c] {
			unknown = append(unknown, c)
		}
	}
	return missing, unknown
}
