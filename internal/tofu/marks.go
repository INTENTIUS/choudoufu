// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/zclconf/go-cty/cty"
)

// sensitiveMarksEqual filters out any non-sensitive marks and then uses
// marksEqual to compare the resulting lists.
//
// Fork (choudoufu, GitHub issue #1067's second estate): both sides are
// first reduced to their minimal cover by [minimalSensitiveCover], so a
// mark on a path whose ancestor already carries one does not count as a
// difference. Upstream compares the raw path sets, which is exact
// bookkeeping when the prior comes from a state file that recorded the
// planned paths verbatim on the last apply. This fork's live-marker plan
// has no state file: the prior is projected from the provider's own read,
// marked from the schema (the whole `data` map of a kubernetes_secret_v1,
// say), while the planned side is marked from the schema AND from the
// configuration, which can mark values INSIDE that map (each key fed by a
// sensitive variable, or by a format() over one). The two sets then
// differ on paths the ancestor mark already covers, and the resource
// planned a sensitivity-only in-place update on every run, forever,
// annotated by the renderer as unchanged. A nested mark under a marked
// ancestor adds nothing to how the value renders, propagates or is
// persisted, so the cover is what sensitivity equality means; the case
// where the cover really moves - an ancestor newly marked, or a nested
// mark with no ancestor - still compares unequal.
func sensitiveMarksEqual(a, b []cty.PathValueMarks) bool {
	a = minimalSensitiveCover(filterNonTargetMarks(a, marks.Sensitive))
	b = minimalSensitiveCover(filterNonTargetMarks(b, marks.Sensitive))
	return marksEqual(a, b)
}

// minimalSensitiveCover drops every entry whose path has a proper prefix
// among the other entries' paths: the prefix's mark already covers it.
// The input is assumed already filtered to one mark kind.
func minimalSensitiveCover(pvms []cty.PathValueMarks) []cty.PathValueMarks {
	out := make([]cty.PathValueMarks, 0, len(pvms))
	for _, p := range pvms {
		covered := false
		for _, q := range pvms {
			if len(q.Path) < len(p.Path) && q.Path.Equals(p.Path[:len(q.Path)]) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	return out
}

// filterNonTargetMarks makes a copy of PathValueMarks and filters out every
// non-target mark.
func filterNonTargetMarks(pvms []cty.PathValueMarks, target interface{}) []cty.PathValueMarks {
	pvmsCopy := make([]cty.PathValueMarks, 0, len(pvms))

	for _, pvm := range pvms {
		pvmCopy := copyPathValueMarks(pvm)

		// Remove non-target marks
		for k := range pvm.Marks {
			if k != target {
				delete(pvmCopy.Marks, k)
			}
		}

		// Add path if it still has marks
		if len(pvmCopy.Marks) != 0 {
			pvmsCopy = append(pvmsCopy, pvmCopy)
		}
	}

	return pvmsCopy
}

// marksEqual compares 2 unordered sets of PathValue marks for equality, with
// the comparison using the cty.PathValueMarks.Equal method.
func marksEqual(a, b []cty.PathValueMarks) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}

	if len(a) != len(b) {
		return false
	}

	less := func(s []cty.PathValueMarks) func(i, j int) bool {
		return func(i, j int) bool {
			// the sort only needs to be consistent, so use the GoString format
			// to get a comparable value
			return fmt.Sprintf("%#v", s[i]) < fmt.Sprintf("%#v", s[j])
		}
	}

	sort.Slice(a, less(a))
	sort.Slice(b, less(b))

	for i := 0; i < len(a); i++ {
		if !a[i].Equal(b[i]) {
			return false
		}
	}

	return true
}

func copyPathValueMarks(marks cty.PathValueMarks) cty.PathValueMarks {
	newMarks := make(cty.ValueMarks, len(marks.Marks))
	result := cty.PathValueMarks{Path: marks.Path}
	for k, v := range marks.Marks {
		newMarks[k] = v
	}
	result.Marks = newMarks
	return result
}

// combinePathValueMarks will combine the marks from two sets of marks with paths, ensuring that we don't duplicate marks
// for the same path, but instead combine the marks for the same path
// This ensures that we don't lose user marks when combining 2 different sets of marks for the same path
func combinePathValueMarks(marks []cty.PathValueMarks, other []cty.PathValueMarks) []cty.PathValueMarks {
	// skip some work if we don't have any marks in either of the lists
	if len(marks) == 0 {
		return other
	}
	if len(other) == 0 {
		return marks
	}

	combined := make([]cty.PathValueMarks, 0, len(marks))
	// construct the initial set of marks
	combined = append(combined, marks...)

	// check if we've already inserted this by looping over and calling .Equals().
	// This isn't so nice but there is no nice comparison for cty.PathValueMarks
	// so we have to do it this way
	for _, mark := range other {
		exists := false
		for i, existing := range combined {
			if mark.Path.Equals(existing.Path) {
				// if we found a matching path, we should combine the marks and update the existing item
				dupe := copyPathValueMarks(existing)
				for k, v := range mark.Marks {
					dupe.Marks[k] = v
				}
				combined[i] = dupe
				exists = true
				break
			}
		}
		// Otherwise we haven't seen this path before, so we should add it to the list
		// no merging required
		if !exists {
			combined = append(combined, mark)
		}
	}

	return combined
}

// removeEphemeralMarks is meant to remove the marks.Ephemeral from any cty.PathValueMarks.
// This is needed to remove the aforementioned mark from the attributes of a value
// before marking the whole value with marks.Ephemeral.
func removeEphemeralMarks(ms []cty.PathValueMarks) []cty.PathValueMarks {
	res := make([]cty.PathValueMarks, len(ms))
	for i, mark := range ms {
		// Since we are preparing to mark the whole value as ephemeral, we want to remove any other
		// possible downstream ephemeral marks to avoid having the same mark on multiple layers.
		delete(mark.Marks, marks.Ephemeral)
		res[i] = mark
	}
	return res
}
